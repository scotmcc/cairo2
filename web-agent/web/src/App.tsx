import {
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Copy,
  Database,
  FileText,
  Globe,
  History,
  Loader2,
  MessageSquare,
  PencilLine,
  Plus,
  Search,
  Send,
  Settings,
  SquareTerminal,
  Square,
  Wrench,
} from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type {
  AgentSession,
  CairoSnapshot,
  PublicConfig,
  SessionEvent,
  SessionMessage,
  WorkspaceSummary,
} from '../../shared/protocol.js';
import { ConfigPanel } from './ConfigPanel.js';
import { SessionsPanel } from './SessionsPanel.js';
import { renderMarkdown } from './markdown.js';
import {
  cancelSession,
  createSession,
  getConfig,
  getCairoSnapshot,
  getSessions,
  getStoredToken,
  getWorkspaces,
  sendMessage,
  storeToken,
} from './api.js';

type UtilityTab = 'log' | 'config' | 'sessions';

const QUICK_COMMANDS = ['/init', '/help', '/session', '/jobs', '/memories', '/tools', '/skills'];

export function App() {
  const [config, setConfig] = useState<PublicConfig | null>(null);
  const [token, setToken] = useState(getStoredToken());
  const [tokenDraft, setTokenDraft] = useState(getStoredToken());
  const [authNeeded, setAuthNeeded] = useState(false);
  const [workspaces, setWorkspaces] = useState<WorkspaceSummary[]>([]);
  const [workspacePath, setWorkspacePath] = useState('');
  const [sessions, setSessions] = useState<AgentSession[]>([]);
  const [activeId, setActiveId] = useState<string>('');
  const [prompt, setPrompt] = useState('');
  const [error, setError] = useState('');
  const [utilityOpen, setUtilityOpen] = useState(true);
  const [utilityTab, setUtilityTab] = useState<UtilityTab>('log');
  const [utilityWidth, setUtilityWidth] = useState(420);
  const [cairoSnapshot, setCairoSnapshot] = useState<CairoSnapshot | null>(null);

  const activeSession = sessions.find((session) => session.id === activeId);
  const logText = useMemo(() => formatLog(activeSession?.events || []), [activeSession?.events]);
  const modelInfo = useMemo(
    () => effectiveModel(cairoSnapshot, activeSession?.cairoRole),
    [cairoSnapshot, activeSession?.cairoRole],
  );

  useEffect(() => {
    void bootstrap(token);
  }, []);

  useEffect(() => {
    if (!activeId) return;
    const source = new EventSource(`/api/sessions/${activeId}/events`, { withCredentials: true });
    const handleEvent = (message: MessageEvent) => {
      try {
        applyEvent(JSON.parse(message.data) as SessionEvent);
      } catch {
        // Ignore malformed stream fragments; the raw log still keeps server-side data.
      }
    };
    const eventTypes: SessionEvent['type'][] = [
      'stdout',
      'stderr',
      'token',
      'status',
      'error',
      'done',
      'tool',
      'message',
    ];
    eventTypes.forEach((type) => source.addEventListener(type, handleEvent));
    source.onerror = () => {
      // EventSource reconnects automatically. Avoid surfacing transient reconnects in chat.
    };
    return () => source.close();
  }, [activeId]);

  async function bootstrap(nextToken: string) {
    setError('');
    try {
      const nextConfig = await getConfig(nextToken);
      setConfig(nextConfig);
      setAuthNeeded(false);
      if (nextToken) storeToken(nextToken);

      if (nextConfig.cairoDbExists) {
        getCairoSnapshot().then(setCairoSnapshot).catch(() => setCairoSnapshot(null));
      } else {
        setCairoSnapshot(null);
      }

      const [{ workspaces: workspaceList }, { sessions: sessionList }] = await Promise.all([
        getWorkspaces(),
        getSessions(),
      ]);
      setWorkspaces(workspaceList);
      setWorkspacePath(workspaceList[0]?.path || nextConfig.workspaceRoots[0] || '');
      setSessions(sessionList);
      setActiveId((current) => current || sessionList[0]?.id || '');
    } catch (err) {
      if (err instanceof Error && err.message.includes('unauthorized')) {
        setAuthNeeded(true);
        setConfig(null);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  }

  function applyEvent(event: SessionEvent) {
    setSessions((current) =>
      current.map((session) => {
        if (session.id !== activeId) return session;
        if (session.events.some((existing) => existing.id === event.id)) return session;
        const status = statusFromEvent(session.status, event);
        const metadata = metadataFromEvent(event);
        return {
          ...session,
          ...metadata,
          status,
          updatedAt: event.createdAt,
          events: [...session.events, event],
          messages: messagesFromEvent(session.messages, event),
        };
      }),
    );
  }

  async function handleTokenSubmit(event: React.FormEvent) {
    event.preventDefault();
    setToken(tokenDraft);
    await bootstrap(tokenDraft);
  }

  async function handleNewSession() {
    setError('');
    try {
      const session = await createSession({ workspacePath });
      setSessions((current) => [session, ...current]);
      setActiveId(session.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleSend() {
    await submitMessage(prompt, true);
  }

  async function submitMessage(raw: string, clearComposer: boolean) {
    if (!activeSession || !raw.trim()) return;
    const content = raw.trim();
    if (clearComposer) setPrompt('');
    setError('');
    setSessions((current) =>
      current.map((session) =>
        session.id === activeSession.id
          ? {
              ...session,
              status: 'running',
              messages: [
                ...session.messages,
                {
                  id: `local-${Date.now()}`,
                  role: 'user',
                  content,
                  createdAt: new Date().toISOString(),
                },
              ],
            }
          : session,
      ),
    );
    try {
      const updated = await sendMessage(activeSession.id, { content });
      replaceSession(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleQuickCommand(command: string) {
    if (command === '/config') {
      setUtilityTab('config');
      setUtilityOpen(true);
      return;
    }
    if (command === '/sessions') {
      setUtilityTab('sessions');
      setUtilityOpen(true);
      return;
    }
    await submitMessage(command, false);
  }

  async function handleCancel() {
    if (!activeSession) return;
    try {
      replaceSession(await cancelSession(activeSession.id));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleCopyLog() {
    await navigator.clipboard.writeText(logText);
  }

  function handleUtilityResize(event: React.PointerEvent<HTMLDivElement>) {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = utilityWidth;
    const maxWidth = Math.min(780, Math.max(360, window.innerWidth - 520));

    const handleMove = (move: PointerEvent) => {
      const nextWidth = startWidth + startX - move.clientX;
      setUtilityWidth(Math.min(maxWidth, Math.max(320, nextWidth)));
    };
    const handleUp = () => {
      window.removeEventListener('pointermove', handleMove);
      window.removeEventListener('pointerup', handleUp);
    };

    window.addEventListener('pointermove', handleMove);
    window.addEventListener('pointerup', handleUp);
  }

  function replaceSession(updated: AgentSession) {
    setSessions((current) => current.map((session) => (session.id === updated.id ? updated : session)));
  }

  if (authNeeded) {
    return (
      <main className="authShell">
        <form className="authPanel" onSubmit={handleTokenSubmit}>
          <h1>Cairo Web</h1>
          <label>
            Bearer token
            <input
              type="password"
              value={tokenDraft}
              onChange={(event) => setTokenDraft(event.target.value)}
              autoFocus
            />
          </label>
          <button type="submit">
            <Send size={16} />
            Connect
          </button>
        </form>
      </main>
    );
  }

  return (
    <div
      className={`appShell ${utilityOpen ? '' : 'utilityCollapsed'}`}
      style={{ '--utility-width': `${utilityWidth}px` } as React.CSSProperties}
    >
      <aside className="sidebar">
        <div className="brandRow">
          <div>
            <h1>Cairo</h1>
            <span className={`statusDot ${activeSession?.status || 'idle'}`} />
            <span className="statusText">{activeSession?.status || 'idle'}</span>
          </div>
          <button className="iconButton" onClick={handleNewSession} title="New Session">
            <Plus size={18} />
          </button>
        </div>

        <section className="workspaceCard">
          <label className="fieldLabel">
            Workspace
            <select
              value={workspacePath}
              onChange={(event) => setWorkspacePath(event.target.value)}
              disabled={activeSession?.status === 'running'}
            >
              {workspaces.length === 0 && (
                <option value={workspacePath || config?.workspaceRoots[0] || ''}>
                  {shortPath(workspacePath || config?.workspaceRoots[0] || '') || 'workspace'}
                </option>
              )}
              {workspaces.map((workspace) => (
                <option key={workspace.path} value={workspace.path}>
                  {workspace.name}
                </option>
              ))}
            </select>
          </label>
          <code>{workspacePath || config?.workspaceRoots[0] || ''}</code>
        </section>

        <section className="commandDock">
          <header>Commands</header>
          <div>
            <button onClick={() => handleQuickCommand('/config')}>
              <Settings size={14} />
              Config
            </button>
            <button onClick={() => handleQuickCommand('/sessions')}>
              <History size={14} />
              Sessions
            </button>
            {QUICK_COMMANDS.map((command) => (
              <button
                key={command}
                onClick={() => handleQuickCommand(command)}
                disabled={!activeSession || activeSession.status === 'running'}
              >
                {command}
              </button>
            ))}
          </div>
        </section>

        <div className="sessionList">
          {sessions.map((session) => (
            <button
              key={session.id}
              className={`sessionItem ${session.id === activeId ? 'active' : ''}`}
              onClick={() => {
                setActiveId(session.id);
                setWorkspacePath(session.workspacePath);
              }}
            >
              <span>{shortPath(session.workspacePath)}</span>
              <small>{new Date(session.updatedAt).toLocaleString()}</small>
            </button>
          ))}
        </div>
      </aside>

      <main className="chatPanel">
        <header className="topBar">
          <div>
            <strong>{activeSession ? shortPath(activeSession.workspacePath) : 'No session'}</strong>
            <span>{config ? `${config.host}:${config.port}` : ''}</span>
            <div className="runtimeMeta">
              <span title={modelInfo.source}>
                <SquareTerminal size={14} />
                {modelInfo.model || 'model not configured'}
              </span>
              <span>
                <MessageSquare size={14} />
                {activeSession?.cairoRole || 'role pending'}
              </span>
              {activeSession?.cairoSessionId && <span>session {activeSession.cairoSessionId}</span>}
            </div>
          </div>
        </header>

        {error && <div className="errorBanner">{error}</div>}

        <Transcript session={activeSession} />

        <footer className="composer">
          <textarea
            value={prompt}
            placeholder="Message Cairo..."
            onChange={(event) => setPrompt(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                event.preventDefault();
                void handleSend();
              }
            }}
          />
          <div className="composerActions">
            <button
              className="sendButton"
              onClick={handleSend}
              disabled={!activeSession || activeSession.status === 'running' || !prompt.trim()}
            >
              {activeSession?.status === 'running' ? <Loader2 className="spin" size={16} /> : <Send size={16} />}
              Send
            </button>
            <button
              className="cancelButton"
              onClick={handleCancel}
              disabled={!activeSession || activeSession.status !== 'running'}
            >
              <Square size={16} />
              Cancel
            </button>
          </div>
        </footer>
      </main>

      <aside className={`utilityPanel ${utilityOpen ? '' : 'collapsed'}`}>
        {utilityOpen ? (
          <>
            <div
              className="utilityResizeHandle"
              role="separator"
              aria-orientation="vertical"
              title="Resize Right Panel"
              onPointerDown={handleUtilityResize}
            />
            <header className="utilityHeader">
              <div className="utilityTabs" role="tablist" aria-label="Cairo side panels">
                <button className={utilityTab === 'log' ? 'active' : ''} onClick={() => setUtilityTab('log')}>
                  <SquareTerminal size={14} />
                  Log
                </button>
                <button className={utilityTab === 'config' ? 'active' : ''} onClick={() => setUtilityTab('config')}>
                  <Settings size={14} />
                  Config
                </button>
                <button
                  className={utilityTab === 'sessions' ? 'active' : ''}
                  onClick={() => setUtilityTab('sessions')}
                >
                  <History size={14} />
                  Sessions
                </button>
              </div>
              <button className="iconButton" onClick={() => setUtilityOpen(false)} title="Collapse Right Panel">
                <ChevronRight size={16} />
              </button>
            </header>
            {utilityTab === 'log' && (
              <section className="logPanelContent">
                <header>
                  <strong>Agent Log</strong>
                  <button className="iconButton" onClick={handleCopyLog} title="Copy Log">
                    <Copy size={16} />
                  </button>
                </header>
                <pre>{logText}</pre>
              </section>
            )}
            {utilityTab === 'config' && <ConfigPanel onSnapshot={setCairoSnapshot} />}
            {utilityTab === 'sessions' && <SessionsPanel />}
          </>
        ) : (
          <button className="panelRailButton" onClick={() => setUtilityOpen(true)} title="Expand Right Panel">
            <ChevronLeft size={18} />
          </button>
        )}
      </aside>
    </div>
  );
}

function Transcript({ session }: { session?: AgentSession }) {
  const ref = useRef<HTMLDivElement>(null);
  const pinnedToBottom = useRef(true);
  const sessionId = useRef<string | undefined>();
  const items = useMemo(() => (session ? transcriptItems(session) : []), [session]);

  useLayoutEffect(() => {
    const node = ref.current;
    if (!node) return;

    if (sessionId.current !== session?.id) {
      sessionId.current = session?.id;
      pinnedToBottom.current = true;
      node.scrollTop = node.scrollHeight;
      return;
    }

    if (pinnedToBottom.current) {
      node.scrollTo({ top: node.scrollHeight });
    }
  }, [session?.id, session?.updatedAt]);

  function handleScroll() {
    const node = ref.current;
    if (!node) return;
    const distanceFromBottom = node.scrollHeight - node.scrollTop - node.clientHeight;
    pinnedToBottom.current = distanceFromBottom < 56;
  }

  if (!session) {
    return (
      <section className="transcriptShell">
        <div className="transcript empty">No session selected</div>
      </section>
    );
  }

  return (
    <section className="transcriptShell">
      <div className="transcript" ref={ref} onScroll={handleScroll}>
        {items.map((item) =>
          item.kind === 'message' ? (
            <MessageBubble key={`message-${item.message.id}`} message={item.message} />
          ) : (
            <ToolRunCard key={`tool-${item.run.id}`} run={item.run} />
          ),
        )}
      </div>
    </section>
  );
}

function MessageBubble({ message }: { message: SessionMessage }) {
  return (
    <article className={`message ${message.role}`}>
      <header>{message.role}</header>
      <div className="markdownBody" dangerouslySetInnerHTML={{ __html: renderMarkdown(message.content) }} />
    </article>
  );
}

function ToolRunCard({ run }: { run: ToolRun }) {
  const preview = toolArgPreview(run.name, run.args);
  const argsText = toolArgsText(run.name, run.args);
  const output = run.isError ? '' : run.result || run.output;
  const hasBody = Boolean(argsText || output);

  return (
    <article className={`toolCard ${run.status}`}>
      <header className="toolCardHead">
        <span className="toolIcon">
          <ToolIcon name={run.name} />
        </span>
        <strong>{run.name}</strong>
        {preview && <code title={preview}>{preview}</code>}
        <span className={`toolStatus ${run.status}`}>
          {run.status === 'running' ? (
            <>
              <Loader2 className="spin" size={12} />
              running
            </>
          ) : (
            <>
              <CheckCircle2 size={12} />
              done
            </>
          )}
        </span>
      </header>
      {hasBody && (
        <div className="toolCardBody">
          {argsText && (
            <section>
              <span>Arguments</span>
              <pre>{argsText}</pre>
            </section>
          )}
          {output && (
            <section>
              <span>Output</span>
              <pre>{output}</pre>
            </section>
          )}
        </div>
      )}
    </article>
  );
}

type TranscriptItem =
  | { kind: 'message'; at: string; message: SessionMessage }
  | { kind: 'toolRun'; at: string; run: ToolRun };

interface ToolRun {
  id: string;
  at: string;
  name: string;
  args: Record<string, unknown> | null;
  output: string;
  result: string;
  isError: boolean;
  status: 'running' | 'done';
}

function transcriptItems(session: AgentSession): TranscriptItem[] {
  const messageItems: TranscriptItem[] = session.messages.map((message) => ({
    kind: 'message',
    at: message.createdAt,
    message,
  }));
  const eventItems: TranscriptItem[] = toolRunsFromEvents(session.events).map((run) => ({
    kind: 'toolRun',
    at: run.at,
    run,
  }));

  return [...messageItems, ...eventItems].sort((a, b) => a.at.localeCompare(b.at));
}

function toolRunsFromEvents(events: SessionEvent[]): ToolRun[] {
  const runs: ToolRun[] = [];
  const open = new Map<string, ToolRun[]>();

  for (const event of events) {
    if (event.type !== 'tool') continue;
    const data = objectValue(event.data);
    const eventName = String(data?.event || '');
    const payload = objectValue(data?.payload);
    const name = typeof payload?.name === 'string' ? payload.name : 'tool';

    if (eventName === 'tool_start') {
      const run: ToolRun = {
        id: event.id,
        at: event.createdAt,
        name,
        args: objectValue(payload?.args),
        output: '',
        result: '',
        isError: false,
        status: 'running',
      };
      runs.push(run);
      const stack = open.get(name) || [];
      stack.push(run);
      open.set(name, stack);
      continue;
    }

    if (eventName === 'tool_update') {
      const run = latestOpenRun(open, name);
      if (!run) continue;
      run.output = typeof payload?.output === 'string' ? appendStreamChunk(run.output, payload.output) : run.output;
      continue;
    }

    if (eventName === 'tool_end') {
      const run = popOpenRun(open, name) || {
        id: event.id,
        at: event.createdAt,
        name,
        args: objectValue(payload?.args),
        output: '',
        result: '',
        isError: false,
        status: 'running' as const,
      };
      if (!runs.includes(run)) runs.push(run);
      run.status = 'done';
      run.isError = Boolean(payload?.is_error);
      if (typeof payload?.result === 'string') run.result = payload.result;
      else if (payload?.result !== undefined) run.result = safeJson(payload.result);
    }
  }

  return runs;
}

function latestOpenRun(open: Map<string, ToolRun[]>, name: string): ToolRun | undefined {
  const stack = open.get(name);
  return stack?.[stack.length - 1];
}

function popOpenRun(open: Map<string, ToolRun[]>, name: string): ToolRun | undefined {
  const stack = open.get(name);
  const run = stack?.pop();
  if (stack && stack.length === 0) open.delete(name);
  return run;
}

function ToolIcon({ name }: { name: string }) {
  if (name === 'bash' || name === 'shell') return <SquareTerminal size={14} />;
  if (name === 'read') return <FileText size={14} />;
  if (name === 'write' || name === 'edit') return <PencilLine size={14} />;
  if (name === 'search') return <Search size={14} />;
  if (name === 'fetch') return <Globe size={14} />;
  if (name.includes('memory') || name === 'learn') return <Database size={14} />;
  return <Wrench size={14} />;
}

function toolArgPreview(name: string, args: Record<string, unknown> | null): string {
  if (!args) return '';
  if (name === 'bash' || name === 'shell') return firstStringArg(args, ['command', 'cmd']);
  if (name === 'read' || name === 'write' || name === 'edit') return firstStringArg(args, ['path', 'file']);
  if (name === 'fetch') return firstStringArg(args, ['url']);
  if (name === 'search') return firstStringArg(args, ['query', 'pattern']);
  return firstStringArg(args, ['path', 'file', 'url', 'query', 'command', 'cmd', 'name', 'title', 'action', 'id']);
}

function toolArgsText(name: string, args: Record<string, unknown> | null): string {
  if (!args || Object.keys(args).length === 0) return '';
  if ((name === 'bash' || name === 'shell') && typeof args.command === 'string') return args.command;
  if ((name === 'bash' || name === 'shell') && typeof args.cmd === 'string') return args.cmd;
  return safeJson(args);
}

function firstStringArg(args: Record<string, unknown>, keys: string[]): string {
  for (const key of keys) {
    const value = args[key];
    if (value === undefined || value === null) continue;
    const text = String(value).replace(/\s+/g, ' ').trim();
    if (text) return text;
  }
  return '';
}

function safeJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function appendStreamChunk(current: string, next: string): string {
  if (!next) return current;
  if (!current) return next;
  if (next.startsWith(current)) return next;
  if (current.endsWith(next)) return current;
  return `${current}${next}`;
}

function appendAssistantToken(messages: AgentSession['messages'], token: string): AgentSession['messages'] {
  const last = messages[messages.length - 1];
  if (last?.role === 'assistant' && last.id.startsWith('stream-')) {
    return [...messages.slice(0, -1), { ...last, content: last.content + token }];
  }
  return [
    ...messages,
    {
      id: `stream-${Date.now()}`,
      role: 'assistant',
      content: token,
      createdAt: new Date().toISOString(),
    },
  ];
}

function messagesFromEvent(messages: AgentSession['messages'], event: SessionEvent): AgentSession['messages'] {
  if (event.type === 'token') return appendAssistantToken(messages, String(event.data));
  if (event.type !== 'message') return messages;
  const message = parseMessageEvent(event.data);
  if (!message || messages.some((existing) => existing.id === message.id)) return messages;

  const localIndex = messages.findIndex(
    (existing) =>
      existing.id.startsWith('local-') &&
      existing.role === message.role &&
      existing.content === message.content,
  );
  if (localIndex >= 0) {
    return messages.map((existing, index) => (index === localIndex ? message : existing));
  }

  const last = messages[messages.length - 1];
  if (message.role === 'assistant' && last?.role === 'assistant' && last.id.startsWith('stream-')) {
    return [...messages.slice(0, -1), message];
  }

  return [...messages, message];
}

function parseMessageEvent(data: SessionEvent['data']): SessionMessage | null {
  if (typeof data !== 'object' || data === null || Array.isArray(data)) return null;
  const value = data as Record<string, unknown>;
  if (
    typeof value.id !== 'string' ||
    typeof value.role !== 'string' ||
    typeof value.content !== 'string' ||
    typeof value.createdAt !== 'string'
  ) {
    return null;
  }
  if (!['user', 'assistant', 'system', 'tool'].includes(value.role)) return null;
  return {
    id: value.id,
    role: value.role as SessionMessage['role'],
    content: value.content,
    createdAt: value.createdAt,
  };
}

function statusFromEvent(current: AgentSession['status'], event: SessionEvent): AgentSession['status'] {
  if (event.type === 'error') return 'error';
  if (event.type === 'done') return 'idle';
  if (event.type !== 'status' || typeof event.data !== 'object' || event.data === null || Array.isArray(event.data)) {
    return current;
  }
  const status = (event.data as { status?: string }).status;
  if (status === 'running' || status === 'idle' || status === 'cancelled' || status === 'complete') return status;
  return current;
}

function metadataFromEvent(event: SessionEvent): Partial<AgentSession> {
  if (event.type !== 'status') return {};
  const data = objectValue(event.data);
  const payload = objectValue(data?.payload);
  const next: Partial<AgentSession> = {};
  const sessionId = Number(payload?.session_id);
  if (Number.isFinite(sessionId) && sessionId > 0) next.cairoSessionId = sessionId;
  if (typeof payload?.role === 'string') next.cairoRole = payload.role;
  if (typeof payload?.cwd === 'string') next.cairoCwd = payload.cwd;
  return next;
}

function formatLog(events: SessionEvent[]): string {
  return events
    .map((event) => {
      const data = typeof event.data === 'string' ? event.data : JSON.stringify(event.data);
      return `[${new Date(event.createdAt).toLocaleTimeString()}] ${event.type}: ${data}`;
    })
    .join('\n');
}

function shortPath(input: string): string {
  const parts = input.split('/').filter(Boolean);
  return parts.slice(-2).join('/') || input;
}

function effectiveModel(
  snapshot: CairoSnapshot | null,
  role: string | undefined,
): { model: string; source: string } {
  if (snapshot && role) {
    const roleModel = snapshot.roles.find((entry) => entry.name === role)?.model?.trim();
    if (roleModel) return { model: roleModel, source: `${role} role` };
  }
  const globalModel = snapshot?.config.model?.trim();
  if (globalModel) return { model: globalModel, source: 'global config' };
  return { model: '', source: 'Cairo config' };
}

function objectValue(value: unknown): Record<string, unknown> | null {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}
