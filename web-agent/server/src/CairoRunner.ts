import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import type { AgentSession, JsonValue } from '../../shared/protocol.js';
import type { ServerConfig } from './config.js';
import { SessionStore } from './sessionStore.js';

export interface Runner {
  sendMessage(session: AgentSession, prompt: string): Promise<void>;
  cancel(session: AgentSession): Promise<void>;
}

interface RunningProcess {
  child: ChildProcessWithoutNullStreams;
  stdoutBuffer: string;
  stderrBuffer: string;
  assistantBuffer: string;
  runningPrompt: boolean;
  intentionallyStopped: boolean;
  timeout?: NodeJS.Timeout;
}

interface CairoJsonLine {
  type?: string;
  payload?: Record<string, unknown>;
}

export class CairoRunner implements Runner {
  private readonly processes = new Map<string, RunningProcess>();

  constructor(
    private readonly store: SessionStore,
    private readonly config: ServerConfig,
  ) {}

  async sendMessage(sessionSnapshot: AgentSession, prompt: string): Promise<void> {
    const session = this.store.mustGetMutable(sessionSnapshot.id);
    if (session.status === 'running') {
      throw new Error('session is already running');
    }

    const proc = this.ensureProcess(session);
    proc.assistantBuffer = '';
    proc.runningPrompt = true;
    this.store.setStatus(session.id, 'running');
    this.store.addEvent(session.id, 'status', { status: 'running' });

    proc.timeout = setTimeout(() => {
      this.store.addEvent(session.id, 'error', {
        message: `Cairo exceeded CAIRO_WEB_MAX_RUNTIME_SECONDS (${this.config.maxRuntimeSeconds})`,
      });
      proc.intentionallyStopped = true;
      terminateProcessTree(proc.child);
      this.processes.delete(session.id);
      this.store.setStatus(session.id, 'error');
      this.store.addEvent(session.id, 'status', { status: 'error' });
    }, this.config.maxRuntimeSeconds * 1000);

    const input = encodePromptForStdin(prompt);
    proc.child.stdin.write(`${input}\n`);
  }

  async cancel(sessionSnapshot: AgentSession): Promise<void> {
    const session = this.store.mustGetMutable(sessionSnapshot.id);
    const proc = this.processes.get(session.id);
    if (!proc) {
      this.store.setStatus(session.id, 'cancelled');
      this.store.addEvent(session.id, 'status', { status: 'cancelled' });
      return;
    }

    proc.intentionallyStopped = true;
    if (proc.timeout) clearTimeout(proc.timeout);
    terminateProcessTree(proc.child);
    this.processes.delete(session.id);
    this.store.setStatus(session.id, 'cancelled');
    this.store.addEvent(session.id, 'status', { status: 'cancelled' });
  }

  private ensureProcess(session: AgentSession): RunningProcess {
    const existing = this.processes.get(session.id);
    if (existing) return existing;

    const args = ['-vscode'];
    if (session.cairoSessionId) {
      args.push('-session', String(session.cairoSessionId));
    } else {
      args.push('-new');
    }

    const childEnv = { ...process.env };
    delete childEnv.CAIRO_WEB_TOKEN;

    const child = spawn(this.config.cairoCliPath, args, {
      cwd: session.workspacePath,
      detached: process.platform !== 'win32',
      env: childEnv,
      stdio: ['pipe', 'pipe', 'pipe'],
    });

    const proc: RunningProcess = {
      child,
      stdoutBuffer: '',
      stderrBuffer: '',
      assistantBuffer: '',
      runningPrompt: false,
      intentionallyStopped: false,
    };

    child.stdout.on('data', (data: Buffer) => this.handleStdout(session.id, proc, data.toString('utf8')));
    child.stderr.on('data', (data: Buffer) => this.handleStderr(session.id, proc, data.toString('utf8')));
    child.on('error', (err) => {
      this.store.addEvent(session.id, 'error', { message: err.message });
      this.store.setStatus(session.id, 'error');
    });
    child.on('close', (code, signal) => {
      if (proc.timeout) clearTimeout(proc.timeout);
      this.processes.delete(session.id);
      if (proc.intentionallyStopped) return;

      if (code === 0) {
        this.store.setStatus(session.id, 'complete');
        this.store.addEvent(session.id, 'done', { code });
      } else {
        this.store.setStatus(session.id, 'error');
        this.store.addEvent(session.id, 'error', { code, signal, message: 'Cairo process exited unexpectedly' });
      }
    });

    this.processes.set(session.id, proc);
    this.store.addEvent(session.id, 'status', {
      status: 'starting',
      command: this.config.cairoCliPath,
      args,
      cwd: session.workspacePath,
    });
    return proc;
  }

  private handleStdout(sessionId: string, proc: RunningProcess, text: string): void {
    proc.stdoutBuffer += text;
    const lines = proc.stdoutBuffer.split(/\r?\n/);
    proc.stdoutBuffer = lines.pop() || '';
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      try {
        this.handleJsonLine(sessionId, proc, JSON.parse(trimmed) as CairoJsonLine);
      } catch {
        this.store.addEvent(sessionId, 'stdout', line);
      }
    }
  }

  private handleStderr(sessionId: string, proc: RunningProcess, text: string): void {
    proc.stderrBuffer += text;
    const lines = proc.stderrBuffer.split(/\r?\n/);
    proc.stderrBuffer = lines.pop() || '';
    for (const line of lines) {
      if (line.trim()) this.store.addEvent(sessionId, 'stderr', line);
    }
  }

  private handleJsonLine(sessionId: string, proc: RunningProcess, line: CairoJsonLine): void {
    const type = line.type || 'stdout';
    const payload = toJsonValue(line.payload || {});

    switch (type) {
      case 'ready': {
        const cairoSessionId = Number(line.payload?.session_id);
        const cairoRole = typeof line.payload?.role === 'string' ? line.payload.role : undefined;
        const cairoCwd = typeof line.payload?.cwd === 'string' ? line.payload.cwd : undefined;
        if (Number.isFinite(cairoSessionId) && cairoSessionId > 0) {
          this.store.update(sessionId, (session) => {
            session.cairoSessionId = cairoSessionId;
            session.cairoRole = cairoRole;
            session.cairoCwd = cairoCwd;
          });
        } else if (cairoRole || cairoCwd) {
          this.store.update(sessionId, (session) => {
            session.cairoRole = cairoRole;
            session.cairoCwd = cairoCwd;
          });
        }
        if (!proc.runningPrompt) {
          this.store.setStatus(sessionId, 'idle');
        }
        this.store.addEvent(sessionId, 'status', { status: 'ready', payload });
        break;
      }
      case 'tool_start':
      case 'tool_update':
      case 'tool_end':
        this.store.addEvent(sessionId, 'tool', { event: type, payload });
        break;
      case 'command_end':
        clearPromptTimeout(proc);
        proc.runningPrompt = false;
        this.store.setStatus(sessionId, 'idle');
        this.store.addEvent(sessionId, 'tool', { event: type, payload });
        this.store.addEvent(sessionId, 'done', { status: 'idle' });
        break;
      case 'agent_start':
      case 'turn_start':
      case 'turn_end':
      case 'thinking':
        this.store.addEvent(sessionId, 'status', { event: type, payload });
        break;
      case 'tokens': {
        const token = typeof line.payload?.token === 'string' ? line.payload.token : '';
        proc.assistantBuffer += token;
        this.store.addEvent(sessionId, 'token', token);
        break;
      }
      case 'system': {
        const text = typeof line.payload?.text === 'string' ? line.payload.text : '';
        if (text.trim()) this.store.addMessage(sessionId, 'system', text);
        this.store.addEvent(sessionId, 'stdout', { event: type, payload });
        break;
      }
      case 'agent_end':
        clearPromptTimeout(proc);
        proc.runningPrompt = false;
        if (proc.assistantBuffer.trim()) {
          this.store.addMessage(sessionId, 'assistant', proc.assistantBuffer);
        }
        proc.assistantBuffer = '';
        this.store.setStatus(sessionId, 'idle');
        this.store.addEvent(sessionId, 'done', { status: 'idle' });
        break;
      case 'error':
        clearPromptTimeout(proc);
        proc.runningPrompt = false;
        this.store.addEvent(sessionId, 'error', payload);
        this.store.setStatus(sessionId, 'error');
        break;
      default:
        this.store.addEvent(sessionId, 'stdout', { event: type, payload });
    }
  }
}

function clearPromptTimeout(proc: RunningProcess): void {
  if (!proc.timeout) return;
  clearTimeout(proc.timeout);
  proc.timeout = undefined;
}

function terminateProcessTree(child: ChildProcessWithoutNullStreams): void {
  if (!child.pid) return;
  if (process.platform === 'win32') {
    spawn('taskkill', ['/pid', String(child.pid), '/t', '/f'], {
      windowsHide: true,
      stdio: 'ignore',
    });
    return;
  }

  try {
    process.kill(-child.pid, 'SIGTERM');
  } catch {
    child.kill('SIGTERM');
  }

  setTimeout(() => {
    try {
      process.kill(-child.pid!, 'SIGKILL');
    } catch {
      try {
        child.kill('SIGKILL');
      } catch {
        // Process already exited.
      }
    }
  }, 2000).unref();
}

function encodePromptForStdin(prompt: string): string {
  if (prompt.includes('\n') || prompt.trimStart().startsWith('{')) {
    return JSON.stringify({ type: 'message', message: prompt });
  }
  return prompt;
}

function toJsonValue(value: unknown): JsonValue {
  if (
    value === null ||
    typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'boolean'
  ) {
    return value;
  }
  if (Array.isArray(value)) return value.map(toJsonValue);
  if (typeof value === 'object') {
    const out: Record<string, JsonValue> = {};
    for (const [key, nested] of Object.entries(value as Record<string, unknown>)) {
      out[key] = toJsonValue(nested);
    }
    return out;
  }
  return String(value);
}
