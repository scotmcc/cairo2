import * as vscode from 'vscode';
import { spawn, ChildProcess } from 'child_process';
import * as fs from 'fs';
import * as path from 'path';
import * as os from 'os';
import { registerOpenFilesPanel } from './panels/open-files';
import {
  CairoConsiderAspect,
  CairoDbSnapshot,
  CairoRole,
  deleteCairoConsiderAspect,
  deleteCairoSession,
  listCairoSessions,
  readCairoDbSnapshot,
  renameCairoSession,
  setCairoConfigValue,
  setCairoConsiderAspectEnabled,
  setCairoRoleValue,
  upsertCairoConsiderAspect,
} from './cairo-db';

// ---------------------------------------------------------------------------
// Cairo VS Code extension — talks to `cairo -vscode` over JSONL on stdio.
//
// Cairo emits these event types (internal/cli/vscode.go):
//   ready, agent_start, turn_start, tokens, thinking,
//   tool_start, tool_update, tool_end, turn_end, agent_end,
//   error, system, command_end
//
// We layer on:
//   - Slash menu (/new, /reload, /clear, /config + cairo's own commands)
//   - Tool-call collapsibles with diff rendering for edits
//   - Markdown rendering with copy-able code blocks
//   - @file mentions, drag-drop attachments, "Send selection/file" commands
//   - Sessions panel: list and click-to-resume (-session N restart)
//   - Thinking display
// ---------------------------------------------------------------------------

let outputChannel: vscode.OutputChannel;
let currentProcess: ChildProcess | null = null;
let isProcessing = false;
let sessionInitialized = false;
let queuedMessages: Array<{ text: string; attachments?: string[] }> = [];
let restartAttempts = 0;
let autoRestartTimer: ReturnType<typeof setTimeout> | undefined;
const intentionallyStoppedProcesses = new WeakSet<ChildProcess>();
const activeWebviews = new Set<vscode.Webview>();
const eventLog: any[] = [];   // replayed into freshly-mounted webviews
const MAX_EVENT_LOG = 2000;
const streamQueues = new Map<string, { type: 'tokens' | 'thinking'; run: number; text: string; scheduled: boolean }>();
const STREAM_FLUSH_THRESHOLD = 64;
let sessionInfo: { id?: number; role?: string; cwd?: string } = {};
let cairoJsonBuffer = '';
let cairoStderrBuffer = '';
let runStart = 0;
let activeRunSeq = 0;          // increments per agent turn so webview can group
let runtimeConfigInfo: {
  model: string;
  modelSource: string;
  ollamaUrl: string;
  ollamaUrlSource: string;
  sessionRole: string;
  contextLen: number;
} | null = null;
const sessionListBuffer: { active: boolean; lines: string[] } = {
  active: false,
  lines: [],
};

interface ExtensionConfig {
  ollamaUrl: string;
  model: string;
  embedModel: string;
  summaryModel: string;
  keepAlive: string;
  dataDir: string;
  cairoExecutable: string;
}

interface ConfigFieldDef {
  key: string;
  label: string;
  kind?: 'text' | 'number' | 'boolean' | 'model' | 'select' | 'textarea' | 'role-model' | 'role-think';
  hint?: string;
  options?: string[];
}

interface ConfigSectionDef {
  title: string;
  tagline: string;
  fields: ConfigFieldDef[];
}

let config: ExtensionConfig = loadConfig();

const CONFIG_SECTIONS: ConfigSectionDef[] = [
  {
    title: 'Identity',
    tagline: 'Who Cairo is and who you are to it.',
    fields: [
      { key: 'ai_name', label: 'ai_name' },
      { key: 'user_name', label: 'user_name' },
    ],
  },
  {
    title: 'LLM Backend',
    tagline: 'Endpoint, primary chat model, embeddings, and thinking defaults.',
    fields: [
      { key: 'ollama_url', label: 'ollama_url' },
      { key: 'llm_api_key', label: 'llm_api_key' },
      { key: 'model', label: 'model', kind: 'model' },
      { key: 'embed_model', label: 'embed_model', kind: 'model' },
      { key: 'embed_model_code', label: 'embed_model_code', kind: 'model' },
      { key: 'think', label: 'think', kind: 'boolean' },
      { key: 'think_budget', label: 'think_budget', kind: 'number' },
    ],
  },
  {
    title: 'Voice',
    tagline: 'Kokoro TTS endpoint and voice blend.',
    fields: [
      { key: 'kokoro_url', label: 'kokoro_url' },
      { key: 'kokoro_voice', label: 'kokoro_voice' },
    ],
  },
  {
    title: 'Memory',
    tagline: 'Recall limits, summarization, and embedding-space thresholds.',
    fields: [
      { key: 'memory_limit', label: 'memory_limit', kind: 'number' },
      { key: 'summary_model', label: 'summary_model', kind: 'model' },
      { key: 'summary_threshold', label: 'summary_threshold', kind: 'number' },
      { key: 'summary_batch_size', label: 'summary_batch_size', kind: 'number' },
      { key: 'summary_context', label: 'summary_context', kind: 'number' },
      { key: 'memory_dedup_threshold', label: 'dedup_threshold', kind: 'number' },
      { key: 'learn_max_chunk_tokens', label: 'learn_max_chunk_tokens', kind: 'number' },
      { key: 'summary_token_threshold', label: 'token_pressure_thresh', kind: 'number' },
    ],
  },
  {
    title: 'Display',
    tagline: 'Rendering preferences used by Cairo terminal views.',
    fields: [
      {
        key: 'glamour_style',
        label: 'glamour_style',
        kind: 'select',
        options: ['', 'dark', 'light', 'notty', 'auto'],
      },
    ],
  },
  {
    title: 'Limits',
    tagline: 'Hard limits on turn and run size.',
    fields: [{ key: 'max_turns', label: 'max_turns', kind: 'number' }],
  },
  {
    title: 'Search',
    tagline: 'External search backend configuration.',
    fields: [{ key: 'searxng_url', label: 'searxng_url' }],
  },
  {
    title: 'Safety',
    tagline: 'Permissioning and unsafe-mode toggles.',
    fields: [{ key: 'unsafe_mode', label: 'unsafe_mode', kind: 'boolean' }],
  },
  {
    title: 'Consider',
    tagline: 'Pre-turn inner dialogue, aspect models, and prompt body.',
    fields: [
      { key: 'consider.enabled', label: 'consider.enabled', kind: 'boolean' },
      { key: 'consider.model', label: 'consider.model', kind: 'model' },
      { key: 'consider.summary_model', label: 'consider.summary_model', kind: 'model' },
      { key: 'consider.include_user_history', label: 'include_user_history', kind: 'boolean' },
      { key: 'consider.template', label: 'aspect_body', kind: 'textarea' },
    ],
  },
  {
    title: 'Roles',
    tagline: 'Per-role model and thinking overrides. Empty inherits global.',
    fields: [
      { key: 'role:orchestrator:model', label: 'orchestrator.model', kind: 'role-model' },
      { key: 'role:orchestrator:think', label: 'orchestrator.think', kind: 'role-think' },
      { key: 'role:researcher:model', label: 'researcher.model', kind: 'role-model' },
      { key: 'role:researcher:think', label: 'researcher.think', kind: 'role-think' },
      { key: 'role:planner:model', label: 'planner.model', kind: 'role-model' },
      { key: 'role:planner:think', label: 'planner.think', kind: 'role-think' },
      { key: 'role:coder:model', label: 'coder.model', kind: 'role-model' },
      { key: 'role:coder:think', label: 'coder.think', kind: 'role-think' },
      { key: 'role:reviewer:model', label: 'reviewer.model', kind: 'role-model' },
      { key: 'role:reviewer:think', label: 'reviewer.think', kind: 'role-think' },
      { key: 'role:thinking_partner:model', label: 'thinking_partner.model', kind: 'role-model' },
      { key: 'role:thinking_partner:think', label: 'thinking_partner.think', kind: 'role-think' },
      { key: 'role:dream:model', label: 'dream.model', kind: 'role-model' },
      { key: 'role:dream:think', label: 'dream.think', kind: 'role-think' },
    ],
  },
];

const EXTENSION_SETTING_MIRRORS: Record<string, keyof ExtensionConfig> = {
  ollama_url: 'ollamaUrl',
  model: 'model',
  embed_model: 'embedModel',
  summary_model: 'summaryModel',
};

const DEFAULT_OLLAMA_URL = 'http://localhost:11434';
let cachedConfigSnapshot: CairoDbSnapshot | null = null;
let cachedModels: string[] = [];
let cachedModelsError = '';
let cachedModelsFetchedAt = 0;

const EXTENSION_SLASH_COMMANDS: Array<{ name: string; desc: string }> = [
  { name: 'new', desc: 'Start a fresh Cairo session' },
  { name: 'reload', desc: 'Restart Cairo with current config' },
  { name: 'clear', desc: 'Clear the visible transcript' },
  { name: 'config', desc: 'Open Cairo settings' },
];

const CAIRO_SLASH_COMMANDS: Array<{ name: string; desc: string }> = [
  { name: 'help', desc: 'Show Cairo slash command help' },
  { name: 'init', desc: 'Guided setup (or: /init codebase)' },
  { name: 'session', desc: 'Show current session info' },
  { name: 'sessions', desc: 'List all sessions' },
  { name: 'jobs', desc: 'List background jobs' },
  { name: 'memories', desc: 'List stored memories' },
  { name: 'tools', desc: 'List custom tools' },
  { name: 'skills', desc: 'List skills' },
];

const ALL_SLASH_COMMANDS = [...EXTENSION_SLASH_COMMANDS, ...CAIRO_SLASH_COMMANDS];

// ---------------------------------------------------------------------------
// Config / paths
// ---------------------------------------------------------------------------

function loadConfig(): ExtensionConfig {
  const c = vscode.workspace.getConfiguration('cairo-vscode');
  return {
    ollamaUrl: c.get<string>('ollamaUrl', ''),
    model: c.get<string>('model', ''),
    embedModel: c.get<string>('embedModel', ''),
    summaryModel: c.get<string>('summaryModel', ''),
    keepAlive: c.get<string>('keepAlive', ''),
    dataDir: resolveHomePath(c.get<string>('dataDir', '')),
    cairoExecutable: c.get<string>('cairoExecutable', ''),
  };
}

function resolveHomePath(v: string): string {
  if (!v) return v;
  if (v === '~') return os.homedir();
  if (v.startsWith('~/') || v.startsWith(`~${path.sep}`)) {
    return path.join(os.homedir(), v.slice(2));
  }
  return v;
}

function getCairoExecutable(): string {
  if (config.cairoExecutable && fs.existsSync(config.cairoExecutable)) {
    return config.cairoExecutable;
  }
  const candidates = [
    '/usr/local/bin/cairo',
    '/usr/bin/cairo',
  ];
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    if (dir) candidates.push(path.join(dir, 'cairo'));
  }
  for (const p of candidates) {
    if (fs.existsSync(p)) return p;
  }
  throw new Error(
    'Cairo binary not found. Install with:\n' +
      '  bash scripts/install.sh\n' +
      'or set "cairo-vscode.cairoExecutable" in VS Code settings.'
  );
}

function getCairoDataDir(): string {
  return config.dataDir || process.env.CAIRO_DATA_DIR || path.join(os.homedir(), '.cairo');
}

function getCairoDbPath(): string {
  return path.join(getCairoDataDir(), 'cairo.db');
}

function postEphemeralToWebviews(message: any) {
  for (const w of activeWebviews) w.postMessage(message);
}

function parseRoleRowKey(key: string): { role: string; field: 'model' | 'think' } | null {
  const match = /^role:([^:]+):(model|think)$/.exec(key);
  if (!match) return null;
  return { role: match[1], field: match[2] as 'model' | 'think' };
}

function roleByName(snapshot: CairoDbSnapshot | null, name: string): CairoRole | undefined {
  return snapshot?.roles.find((r) => r.name === name);
}

function configValue(snapshot: CairoDbSnapshot | null, key: string): string {
  if (!snapshot) return '';
  const roleKey = parseRoleRowKey(key);
  if (roleKey) {
    const role = roleByName(snapshot, roleKey.role);
    return role ? role[roleKey.field] || '' : '';
  }
  return snapshot.config[key] || '';
}

function effectiveOllamaUrl(snapshot: CairoDbSnapshot | null): { url: string; source: string } {
  const fromSetting = config.ollamaUrl.trim();
  if (fromSetting) return { url: fromSetting, source: 'VS Code setting' };
  const fromDb = configValue(snapshot, 'ollama_url').trim();
  if (fromDb) return { url: fromDb, source: 'Cairo DB' };
  return { url: DEFAULT_OLLAMA_URL, source: 'default' };
}

function effectiveModel(snapshot: CairoDbSnapshot | null, role?: string): { model: string; source: string } {
  if (role) {
    const roleModel = roleByName(snapshot, role)?.model || '';
    if (roleModel) return { model: roleModel, source: `${role} role` };
  }
  const global = configValue(snapshot, 'model');
  if (global) return { model: global, source: 'global config' };
  return { model: '', source: 'not configured' };
}

async function loadCairoConfigSnapshot(): Promise<CairoDbSnapshot> {
  cachedConfigSnapshot = await readCairoDbSnapshot(getCairoDbPath());
  return cachedConfigSnapshot;
}

async function syncExtensionSettingMirrorsToDb() {
  const updates: Array<[string, string]> = [];
  if (config.ollamaUrl.trim()) updates.push(['ollama_url', config.ollamaUrl.trim()]);
  if (config.model.trim()) updates.push(['model', config.model.trim()]);
  if (config.embedModel.trim()) updates.push(['embed_model', config.embedModel.trim()]);
  if (config.summaryModel.trim()) updates.push(['summary_model', config.summaryModel.trim()]);
  if (updates.length === 0) return;

  const dbPath = getCairoDbPath();
  for (const [key, value] of updates) {
    cachedConfigSnapshot = await setCairoConfigValue(dbPath, key, value);
  }
}

function settingSectionForConfigKey(key: string): string | undefined {
  const mapped = EXTENSION_SETTING_MIRRORS[key];
  if (!mapped) return undefined;
  switch (mapped) {
    case 'ollamaUrl': return 'ollamaUrl';
    case 'model': return 'model';
    case 'embedModel': return 'embedModel';
    case 'summaryModel': return 'summaryModel';
    default: return undefined;
  }
}

async function mirrorConfigValueToVsCodeSetting(key: string, value: string) {
  const section = settingSectionForConfigKey(key);
  if (!section) return;
  const c = vscode.workspace.getConfiguration('cairo-vscode');
  const inspected = c.inspect<string>(section);
  let target = vscode.ConfigurationTarget.Global;
  if (inspected?.workspaceValue !== undefined || inspected?.workspaceFolderValue !== undefined) {
    target = vscode.ConfigurationTarget.Workspace;
  } else if (inspected?.globalValue !== undefined) {
    target = vscode.ConfigurationTarget.Global;
  }
  await c.update(section, value, target);
  config = loadConfig();
}

async function saveCairoConfigEntry(key: string, value: string): Promise<CairoDbSnapshot> {
  const dbPath = getCairoDbPath();
  const roleKey = parseRoleRowKey(key);
  if (roleKey) {
    cachedConfigSnapshot = await setCairoRoleValue(dbPath, roleKey.role, roleKey.field, value);
    return cachedConfigSnapshot;
  }
  cachedConfigSnapshot = await setCairoConfigValue(dbPath, key, value);
  await mirrorConfigValueToVsCodeSetting(key, value);
  return cachedConfigSnapshot;
}

function normalizeBaseUrl(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return '';
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)
    ? trimmed
    : `http://${trimmed}`;
  return withScheme.replace(/\/+$/, '');
}

async function refreshAvailableModels(snapshot: CairoDbSnapshot): Promise<void> {
  const resolved = effectiveOllamaUrl(snapshot);
  const base = normalizeBaseUrl(resolved.url);
  if (!base) {
    cachedModels = [];
    cachedModelsError = 'No LLM endpoint configured.';
    cachedModelsFetchedAt = Date.now();
    return;
  }

  const headers: Record<string, string> = {};
  const apiKey = process.env.LLM_API_KEY || configValue(snapshot, 'llm_api_key');
  if (apiKey) headers.Authorization = `Bearer ${apiKey}`;

  try {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15_000);
    const res = await fetch(`${base}/v1/models`, { headers, signal: controller.signal }).finally(() => {
      clearTimeout(timer);
    });
    const body = await res.text();
    if (!res.ok) {
      cachedModels = [];
      cachedModelsError = `HTTP ${res.status}: ${body.slice(0, 240)}`;
      cachedModelsFetchedAt = Date.now();
      return;
    }
    const parsed = JSON.parse(body) as { data?: Array<{ id?: string }> };
    cachedModels = Array.from(
      new Set((parsed.data || []).map((m) => String(m.id || '').trim()).filter(Boolean))
    ).sort((a, b) => a.localeCompare(b));
    cachedModelsError = cachedModels.length ? '' : 'The LLM endpoint returned no models.';
    cachedModelsFetchedAt = Date.now();
  } catch (err: any) {
    cachedModels = [];
    cachedModelsError = err?.message || String(err);
    cachedModelsFetchedAt = Date.now();
  }
}

async function postConfigState(target: vscode.Webview, opts: { refreshModels?: boolean } = {}) {
  try {
    const snapshot = await loadCairoConfigSnapshot();
    if (opts.refreshModels) {
      await refreshAvailableModels(snapshot);
    }
    const urlInfo = effectiveOllamaUrl(snapshot);
    const modelInfo = effectiveModel(snapshot, sessionInfo.role);
    target.postMessage({
      type: 'config-state',
      ok: true,
      dbPath: snapshot.dbPath,
      layout: CONFIG_SECTIONS,
      snapshot,
      models: cachedModels,
      modelsError: cachedModelsError,
      modelsFetchedAt: cachedModelsFetchedAt,
      effective: {
        ollamaUrl: urlInfo.url,
        ollamaUrlSource: urlInfo.source,
        model: modelInfo.model,
        modelSource: modelInfo.source,
        sessionRole: sessionInfo.role || '',
      },
      running: runtimeConfigInfo,
    });
  } catch (err: any) {
    target.postMessage({
      type: 'config-state',
      ok: false,
      dbPath: getCairoDbPath(),
      layout: CONFIG_SECTIONS,
      error: err?.message || String(err),
    });
  }
}

async function broadcastConfigState(opts: { refreshModels?: boolean } = {}) {
  await Promise.all(Array.from(activeWebviews).map((w) => postConfigState(w, opts)));
}

async function postSessionsState(target: vscode.Webview) {
  try {
    const result = await listCairoSessions(getCairoDbPath(), sessionInfo.id || 0);
    target.postMessage({ type: 'sessions', sessions: result.sessions });
  } catch (err: any) {
    target.postMessage({
      type: 'sessions',
      sessions: [],
      error: err?.message || String(err),
    });
  }
}

async function broadcastSessionsState() {
  await Promise.all(Array.from(activeWebviews).map((w) => postSessionsState(w)));
}

async function refreshRuntimeConfigInfo() {
  try {
    const snapshot = await loadCairoConfigSnapshot();
    const urlInfo = effectiveOllamaUrl(snapshot);
    const modelInfo = effectiveModel(snapshot, sessionInfo.role);
    runtimeConfigInfo = {
      model: modelInfo.model,
      modelSource: modelInfo.source,
      ollamaUrl: urlInfo.url,
      ollamaUrlSource: urlInfo.source,
      sessionRole: sessionInfo.role || '',
      contextLen: parsePositiveInt(configValue(snapshot, 'model_ctx')),
    };
    postStatus();
    await broadcastConfigState();
  } catch (err: any) {
    outputChannel.appendLine(`[runtime-config] ${err?.message || err}`);
  }
}

// ---------------------------------------------------------------------------
// Webview messaging — every message gets logged so it can be replayed
// when a webview is mounted (e.g. sidebar shown after extension activated).
// ---------------------------------------------------------------------------

function rememberWebviewMessage(message: any) {
  if (message.type !== 'status') {
    const last = eventLog[eventLog.length - 1];
    if (
      (message.type === 'tokens' || message.type === 'thinking') &&
      last?.type === message.type &&
      last.run === message.run
    ) {
      last.text = String(last.text || '') + String(message.text || '');
      return;
    }
    eventLog.push(message);
    if (eventLog.length > MAX_EVENT_LOG) {
      eventLog.splice(0, eventLog.length - MAX_EVENT_LOG);
    }
  }
}

function postToWebviews(message: any) {
  if (message.type !== 'tokens' && message.type !== 'thinking') {
    flushStreamQueues();
  }
  rememberWebviewMessage(message);
  for (const w of activeWebviews) w.postMessage(message);
}

function queueStreamToWebviews(type: 'tokens' | 'thinking', text: string, run: number) {
  if (!text) return;
  const key = `${type}:${run}`;
  let queued = streamQueues.get(key);
  if (!queued) {
    queued = { type, run, text: '', scheduled: false };
    streamQueues.set(key, queued);
  }
  queued.text += text;
  // Eagerly flush once enough has accumulated so latency stays bounded under
  // bursty token streams. Otherwise defer to the next microtask so multiple
  // tokens parsed from the same stdout chunk coalesce into a single post.
  if (queued.text.length >= STREAM_FLUSH_THRESHOLD) {
    flushStreamQueue(key);
    return;
  }
  if (!queued.scheduled) {
    queued.scheduled = true;
    queueMicrotask(() => flushStreamQueue(key));
  }
}

function flushStreamQueue(key: string) {
  const queued = streamQueues.get(key);
  if (!queued) return;
  streamQueues.delete(key);
  if (queued.text) postToWebviews({ type: queued.type, text: queued.text, run: queued.run });
}

function flushStreamQueues() {
  for (const key of [...streamQueues.keys()]) flushStreamQueue(key);
}

function discardStreamQueues() {
  streamQueues.clear();
}

function postStatus(statusOverride?: string) {
  const status =
    statusOverride ||
    (isProcessing ? 'working' : sessionInitialized ? 'idle' : 'starting');
  postToWebviews({
    type: 'status',
    processing: isProcessing,
    queued: queuedMessages.length,
    status,
    session: sessionInfo,
    runtime: runtimeConfigInfo,
    runStartedAt: isProcessing && runStart ? runStart : 0,
    activeRun: activeRunSeq,
  });
}

function parsePositiveInt(raw: string): number {
  const n = Number.parseInt(String(raw || '').trim(), 10);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function emitText(text: string, style = 'stdout') {
  if (!text) return;
  postToWebviews({ type: 'text', text, style, run: activeRunSeq });
}

function emitClear() {
  discardStreamQueues();
  eventLog.length = 0;
  postToWebviews({ type: 'clear' });
}

// ---------------------------------------------------------------------------
// Cairo JSON event handling
// ---------------------------------------------------------------------------

function handleCairoEvent(event: { type?: string; payload?: Record<string, any> }) {
  const payload = event.payload || {};
  switch (event.type) {
    case 'ready':
      sessionInfo = {
        id: typeof payload.session_id === 'number' ? payload.session_id : undefined,
        role: typeof payload.role === 'string' ? payload.role : undefined,
        cwd: typeof payload.cwd === 'string' ? payload.cwd : undefined,
      };
      sessionInitialized = true;
      emitText(
        `Cairo ready · session ${sessionInfo.id ?? '?'} · role ${sessionInfo.role ?? '?'}`,
        'system'
      );
      postStatus('idle');
      void refreshRuntimeConfigInfo();
      break;

    case 'agent_start':
      isProcessing = true;
      activeRunSeq++;
      runStart = Date.now();
      postToWebviews({ type: 'run-start', run: activeRunSeq });
      postStatus('working');
      break;

    case 'turn_start':
    case 'turn_end':
      break;

    case 'tokens': {
      const tok = typeof payload.token === 'string' ? payload.token : '';
      queueStreamToWebviews('tokens', tok, activeRunSeq);
      break;
    }

    case 'thinking': {
      const tok = typeof payload.token === 'string' ? payload.token : '';
      queueStreamToWebviews('thinking', tok, activeRunSeq);
      break;
    }

    case 'tool_start': {
      const name = typeof payload.name === 'string' ? payload.name : 'tool';
      const args = (payload.args as Record<string, unknown>) || {};
      postToWebviews({
        type: 'tool-start',
        run: activeRunSeq,
        name,
        args,
        startedAt: Date.now(),
      });
      postStatus('working');
      break;
    }

    case 'tool_update': {
      const name = typeof payload.name === 'string' ? payload.name : 'tool';
      const out = typeof payload.output === 'string' ? payload.output : '';
      postToWebviews({ type: 'tool-update', run: activeRunSeq, name, output: out });
      break;
    }

    case 'tool_end': {
      const name = typeof payload.name === 'string' ? payload.name : 'tool';
      const isErr = Boolean(payload.is_error);
      const result = typeof payload.result === 'string' ? payload.result : '';
      postToWebviews({
        type: 'tool-end',
        run: activeRunSeq,
        name,
        result,
        isError: isErr,
        endedAt: Date.now(),
      });
      break;
    }

    case 'agent_end':
      finishCurrentRun(false);
      break;

    case 'command_end':
      flushSessionListIfActive();
      finishCurrentRun(false);
      break;

    case 'system': {
      const text = typeof payload.text === 'string' ? payload.text : '';
      if (!text) break;
      // Special case: capture /sessions output so the sessions panel can
      // parse it. Sessions are formatted by listCommandOutput as:
      //   "  [3] my session — coder — 2026-05-04 12:34"
      //   "* [4] active   — thinking_partner — 2026-05-04 13:00"
      if (sessionListBuffer.active) {
        sessionListBuffer.lines.push(text);
      }
      emitText(text, 'system');
      break;
    }

    case 'error': {
      const msg = typeof payload.message === 'string' ? payload.message : 'Cairo reported an error';
      emitText(msg, 'error');
      finishCurrentRun(true);
      break;
    }
  }
}

function finishCurrentRun(failed: boolean) {
  restartAttempts = 0;
  const elapsed = runStart ? Date.now() - runStart : 0;
  postToWebviews({ type: 'run-end', run: activeRunSeq, failed, elapsedMs: elapsed });
  isProcessing = false;
  runStart = 0;
  const next = queuedMessages.shift();
  if (next) {
    void sendInput(formatMessage(next.text, next.attachments));
    return;
  }
  postStatus(failed ? 'failed' : 'idle');
}

function flushSessionListIfActive() {
  if (!sessionListBuffer.active) return;
  const sessions = parseSessionList(sessionListBuffer.lines.join('\n'));
  sessionListBuffer.active = false;
  sessionListBuffer.lines = [];
  postToWebviews({ type: 'sessions', sessions });
}

function parseSessionList(text: string): Array<{ id: number; name: string; role: string; lastActive: string; current: boolean }> {
  // Format from cli.go:listCommandOutput:
  //   "  [12] my session — coder — 2026-05-04 12:34"
  //   "* [13] (unnamed) — thinking_partner — 2026-05-04 13:00"
  const out: Array<{ id: number; name: string; role: string; lastActive: string; current: boolean }> = [];
  const re = /^([\* ])\s*\[(\d+)\]\s+(.+?)\s+—\s+(\S+)\s+—\s+(.+?)\s*$/;
  for (const line of text.split('\n')) {
    const m = line.match(re);
    if (!m) continue;
    out.push({
      current: m[1] === '*',
      id: Number(m[2]),
      name: m[3],
      role: m[4],
      lastActive: m[5],
    });
  }
  return out;
}

// ---------------------------------------------------------------------------
// Subprocess lifecycle
// ---------------------------------------------------------------------------

function appendStreamOutput(text: string) {
  cairoJsonBuffer += text;
  const lines = cairoJsonBuffer.split(/\r?\n/);
  cairoJsonBuffer = lines.pop() || '';
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    if (!trimmed.startsWith('{')) {
      emitText(trimmed, 'system');
      continue;
    }
    try {
      handleCairoEvent(JSON.parse(trimmed));
    } catch {
      emitText(trimmed, 'system');
    }
  }
}

function appendStderrOutput(text: string) {
  cairoStderrBuffer += text;
  const lines = cairoStderrBuffer.split(/\r?\n/);
  cairoStderrBuffer = lines.pop() || '';
  for (const line of lines) {
    const visible = visibleStderrLine(line.trim());
    if (visible) emitText(visible, 'error');
  }
}

function visibleStderrLine(line: string): string {
  if (!line || line.startsWith('cairo: Warning:')) return '';

  // Cairo writes routine background diagnostics with Go's default timestamped
  // logger. Keep those in the Output channel, but don't pollute the chat after
  // the VS Code JSON bridge is already ready.
  const timestampedGoLog = /^\d{4}\/\d{2}\/\d{2}\s+\d{2}:\d{2}:\d{2}\s+/.test(line);
  if (sessionInitialized && timestampedGoLog) return '';

  return line.replace(/^\d{4}\/\d{2}\/\d{2}\s+\d{2}:\d{2}:\d{2}\s+/, '');
}

async function spawnCairo(opts: { newSession?: boolean; sessionId?: number } = {}) {
  config = loadConfig();
  runtimeConfigInfo = null;
  try {
    await syncExtensionSettingMirrorsToDb();
  } catch (err: any) {
    outputChannel.appendLine(`[config-sync] ${err?.message || err}`);
  }
  const cairoPath = getCairoExecutable();
  cairoJsonBuffer = '';
  cairoStderrBuffer = '';
  const args = ['-vscode'];
  if (config.dataDir) args.push('-data-dir', config.dataDir);
  if (opts.newSession) args.push('-new');
  if (opts.sessionId) args.push('-session', String(opts.sessionId));

  const env: NodeJS.ProcessEnv = { ...process.env };
  if (config.ollamaUrl) env.OLLAMA_URL = config.ollamaUrl;
  if (config.dataDir) env.CAIRO_DATA_DIR = config.dataDir;

  outputChannel.appendLine(`[spawn] ${cairoPath} ${args.join(' ')}`);
  const child = spawn(cairoPath, args, {
    detached: process.platform !== 'win32',
    env,
  });

  child.stdout?.on('data', (data: Buffer) => {
    const text = data.toString();
    outputChannel.append(text);
    appendStreamOutput(text);
  });
  child.stderr?.on('data', (data: Buffer) => {
    const text = data.toString();
    outputChannel.append(`[stderr] ${text}`);
    appendStderrOutput(text);
  });
  child.on('error', (err) => {
    outputChannel.appendLine(`[error] ${err.message}`);
    emitText(`Cairo failed to start: ${err.message}`, 'error');
    sessionInitialized = false;
    isProcessing = false;
    postStatus('failed');
  });
  child.on('close', (code) => {
    outputChannel.appendLine(`[closed] exit=${code}`);
    const wasIntentional = intentionallyStoppedProcesses.has(child);
    if (currentProcess === child) {
      currentProcess = null;
      sessionInitialized = false;
      isProcessing = false;
      runStart = 0;
      runtimeConfigInfo = null;
      postStatus('idle');
    }
    if (!wasIntentional && code !== 0 && code !== null) {
      scheduleAutoRestart();
    }
  });
  currentProcess = child;
}

function stopCurrentProcess() {
  if (!currentProcess) return;
  intentionallyStoppedProcesses.add(currentProcess);
  terminateProcessTree(currentProcess);
  currentProcess = null;
}

function terminateProcessTree(child: ChildProcess) {
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
    try { child.kill('SIGTERM'); } catch { /* gone */ }
  }
  setTimeout(() => {
    try { process.kill(-child.pid!, 'SIGKILL'); }
    catch { try { child.kill('SIGKILL'); } catch { /* gone */ } }
  }, 2000).unref();
}

function scheduleAutoRestart() {
  clearTimeout(autoRestartTimer);
  const MAX = 5;
  if (restartAttempts >= MAX) {
    emitText('Cairo: too many restart failures — try /reload', 'system');
    restartAttempts = 0;
    return;
  }
  const delay = Math.min(2000 * Math.pow(2, restartAttempts), 30000);
  restartAttempts++;
  emitText(
    `Cairo: restarting in ${Math.round(delay / 1000)}s (attempt ${restartAttempts}/${MAX})`,
    'system'
  );
  autoRestartTimer = setTimeout(() => {
    spawnCairo().catch((err: any) => {
      emitText(`Cairo: ${err.message}`, 'error');
      scheduleAutoRestart();
    });
  }, delay);
}

async function ensureCairo() {
  if (currentProcess) return;
  await spawnCairo();
}

async function restartCairo(opts: { newSession?: boolean; sessionId?: number } = {}) {
  clearTimeout(autoRestartTimer);
  stopCurrentProcess();
  sessionInitialized = false;
  isProcessing = false;
  runStart = 0;
  queuedMessages = [];
  sessionInfo = {};
  runtimeConfigInfo = null;
  postStatus('starting');
  await spawnCairo(opts);
}

// ---------------------------------------------------------------------------
// Sending input
// ---------------------------------------------------------------------------

async function sendInput(message: string) {
  await ensureCairo();
  if (!currentProcess) return;
  if (isProcessing) {
    queuedMessages.push({ text: message });
    postStatus();
    return;
  }
  outputChannel.appendLine(`[send] ${message.split('\n')[0]}${message.includes('\n') ? ' …' : ''}`);
  currentProcess.stdin?.write(`${message}\n`);
  isProcessing = true;
  if (!runStart) runStart = Date.now();
  postStatus('working');
}

function formatMessage(text: string, attachments?: string[]): string {
  if (!attachments || attachments.length === 0) return text;
  // Inline-attach file contents above the prompt so cairo's agent has them.
  const blocks: string[] = [];
  for (const p of attachments) {
    try {
      const stat = fs.statSync(p);
      if (stat.size > 200_000) {
        blocks.push(`@${p} (skipped — file too large: ${Math.round(stat.size / 1024)} KB)`);
        continue;
      }
      const content = fs.readFileSync(p, 'utf8');
      blocks.push(`@${p}:\n\`\`\`\n${content}\n\`\`\``);
    } catch (err: any) {
      blocks.push(`@${p} (read failed: ${err?.message || err})`);
    }
  }
  return `${blocks.join('\n\n')}\n\n${text}`;
}

async function handleUserMessage(raw: string, attachments?: string[]) {
  const text = raw.trim();
  if (!text && (!attachments || attachments.length === 0)) return;

  // User-displayed message: show attachments as @path lines, separated from prompt
  const displayParts: string[] = [];
  if (attachments && attachments.length) {
    displayParts.push(attachments.map((p) => `@${p}`).join('\n'));
  }
  if (text) displayParts.push(text);
  postToWebviews({
    type: 'user-message',
    text: displayParts.join('\n\n'),
  });

  // Slash commands routed to extension or forwarded to cairo
  if (text.startsWith('/')) {
    const [cmdRaw, ...rest] = text.split(/\s+/);
    const cmd = cmdRaw.slice(1).toLowerCase();
    const args = rest.join(' ');
    switch (cmd) {
      case 'new':
        emitClear();
        await restartCairo({ newSession: true });
        return;
      case 'reload':
        await restartCairo();
        return;
      case 'clear':
        emitClear();
        return;
      case 'config':
        postEphemeralToWebviews({ type: 'config-open' });
        await broadcastConfigState({ refreshModels: true });
        return;
      case 'sessions':
        postEphemeralToWebviews({ type: 'sessions-open' });
        await broadcastSessionsState();
        return;
      default:
        await sendInput(args ? `/${cmd} ${args}` : `/${cmd}`);
        return;
    }
  }

  await sendInput(formatMessage(text, attachments));
}

// ---------------------------------------------------------------------------
// VS Code editor commands ("Send to Cairo")
// ---------------------------------------------------------------------------

async function focusCairoView() {
  await vscode.commands.executeCommand('workbench.view.extension.cairo-sidebar');
}

async function sendActiveSelection() {
  const ed = vscode.window.activeTextEditor;
  if (!ed) {
    vscode.window.showWarningMessage('Cairo: no active editor.');
    return;
  }
  const sel = ed.selection;
  if (sel.isEmpty) {
    vscode.window.showWarningMessage('Cairo: selection is empty.');
    return;
  }
  const text = ed.document.getText(sel);
  const file = vscode.workspace.asRelativePath(ed.document.uri);
  const lang = ed.document.languageId;
  const startLine = sel.start.line + 1;
  const endLine = sel.end.line + 1;
  const prompt = `Selection from \`${file}:${startLine}-${endLine}\`:\n\`\`\`${lang}\n${text}\n\`\`\`\n\n`;
  await focusCairoView();
  postToWebviews({ type: 'prefill', text: prompt });
}

async function sendActiveFile() {
  const ed = vscode.window.activeTextEditor;
  if (!ed) {
    vscode.window.showWarningMessage('Cairo: no active editor.');
    return;
  }
  const file = vscode.workspace.asRelativePath(ed.document.uri);
  await focusCairoView();
  postToWebviews({ type: 'attach', files: [ed.document.fileName], display: [file] });
}

// ---------------------------------------------------------------------------
// Workspace file search for @-mentions
// ---------------------------------------------------------------------------

async function searchWorkspaceFiles(query: string, limit = 20): Promise<string[]> {
  const trimmed = (query || '').trim();
  const pattern = trimmed ? `**/*${trimmed}*` : '**/*';
  const uris = await vscode.workspace.findFiles(
    pattern,
    '**/{node_modules,.git,dist,build,out}/**',
    limit
  );
  return uris.map((u) => vscode.workspace.asRelativePath(u));
}

function resolveAttachmentPaths(rels: string[]): string[] {
  const folders = vscode.workspace.workspaceFolders;
  const root = folders && folders.length > 0 ? folders[0].uri.fsPath : process.cwd();
  return rels.map((rel) => (path.isAbsolute(rel) ? rel : path.resolve(root, rel)));
}

// ---------------------------------------------------------------------------
// Webview HTML
// ---------------------------------------------------------------------------

function getWebviewHtml(nonce: string): string {
  const slashJson = JSON.stringify(ALL_SLASH_COMMANDS);
  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta http-equiv="Content-Security-Policy"
        content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';" />
  <title>Cairo</title>
  <style>
    :root {
      --bg: #1e1e1e;
      --panel: #252526;
      --panel-2: #2d2d30;
      --border: #3c3c3c;
      --text: #cccccc;
      --muted: #9ca3af;
      --accent: #007acc;
      --accent-2: #1f6feb;
      --success: #4ec9b0;
      --warning: #ce9178;
      --error: #f48771;
      --add: #234d2c;
      --add-fg: #b9e8b9;
      --del: #5a2424;
      --del-fg: #f5b1b1;
    }
    * { margin: 0; padding: 0; box-sizing: border-box; }
    html, body {
      height: 100%;
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
      background: var(--bg);
      color: var(--text);
      font-size: 13px;
    }
    body { display: flex; flex-direction: column; }

    .header {
      padding: 6px 10px;
      background: var(--panel);
      border-bottom: 1px solid var(--border);
      display: flex; align-items: center; gap: 6px; flex-wrap: wrap;
    }
    .header .session {
      flex: 1; font-size: 11px; color: var(--muted);
      overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    }
    .btn {
      padding: 3px 9px; background: #3c3c3c; color: var(--text);
      border: none; border-radius: 3px; cursor: pointer;
      font-size: 11px; font-family: inherit;
    }
    .btn:hover { background: #4c4c4c; }
    .btn.primary { background: var(--accent); color: #fff; }
    .btn.primary:hover { background: #0063a1; }
    .btn.active { background: var(--accent); color: #fff; }
    .btn.active:hover { background: #0063a1; }
    .btn.danger { background: #b3261e; color: #fff; }
    .btn.danger:hover { background: #8d1c17; }
    .btn:disabled { opacity: 0.4; cursor: not-allowed; }

    .output {
      flex: 1; padding: 10px;
      overflow-y: auto; overflow-x: hidden;
      line-height: 1.55;
    }
    .output > * { margin-bottom: 8px; }

    .msg-system { color: var(--warning); font-style: italic; font-size: 12px; }
    .msg-error { color: var(--error); white-space: pre-wrap; }
    .msg-user {
      padding: 8px 10px; background: var(--panel-2);
      border-left: 3px solid var(--success); border-radius: 3px;
      white-space: pre-wrap; font-family: 'Consolas', 'Courier New', monospace;
      font-size: 12.5px;
    }
    .msg-assistant {
      padding: 6px 0;
    }
    .msg-thinking {
      padding: 6px 10px; background: rgba(255,255,255,0.02);
      border-left: 2px dashed var(--muted); border-radius: 3px;
      color: var(--muted); font-size: 12px; font-style: italic;
      white-space: pre-wrap;
    }
    .msg-thinking summary { cursor: pointer; color: var(--muted); font-style: normal; }
    .msg-thinking[open] summary { margin-bottom: 4px; }

    .run-summary {
      padding: 6px 10px; background: rgba(78,201,176,0.06);
      border-top: 1px solid rgba(78,201,176,0.25);
      border-bottom: 1px solid rgba(78,201,176,0.25);
      color: var(--success); font-size: 11px;
    }
    .run-summary.failed {
      background: rgba(244,135,113,0.08);
      border-color: rgba(244,135,113,0.4);
      color: var(--error);
    }

    /* Tool cards */
    .tool {
      border: 1px solid var(--border); border-radius: 4px;
      background: #252a30; overflow: hidden;
    }
    .tool.error { border-color: rgba(244,135,113,0.5); }
    .tool-head {
      display: flex; align-items: center; gap: 8px;
      padding: 6px 10px; cursor: pointer; user-select: none;
      font-size: 12px;
    }
    .tool-head:hover { background: rgba(255,255,255,0.03); }
    .tool-icon { width: 14px; text-align: center; color: var(--muted); }
    .tool-name { font-weight: 600; color: #e2e8f0; }
    .tool-arg {
      flex: 1; color: var(--muted);
      overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
      font-family: 'Consolas', monospace; font-size: 11.5px;
    }
    .tool-status { font-size: 11px; color: var(--muted); }
    .tool-status.done { color: var(--success); }
    .tool-status.failed { color: var(--error); }
    .tool-status .spinner {
      display: inline-block; width: 10px; height: 10px;
      border: 2px solid rgba(206,145,120,0.3);
      border-top-color: var(--warning); border-radius: 50%;
      animation: spin 0.8s linear infinite; vertical-align: -2px; margin-right: 4px;
    }
    @keyframes spin { to { transform: rotate(360deg); } }
    .tool-body {
      display: none; padding: 8px 10px 10px;
      border-top: 1px solid var(--border);
      font-family: 'Consolas', 'Courier New', monospace;
      font-size: 11.5px;
    }
    .tool.open .tool-body { display: block; }
    .tool-section { margin-bottom: 8px; }
    .tool-section:last-child { margin-bottom: 0; }
    .tool-section-label {
      color: var(--muted); font-size: 10.5px; text-transform: uppercase;
      letter-spacing: 0.5px; margin-bottom: 4px;
    }
    .tool-pre {
      background: #1a1a1a; color: #d4d4d4;
      padding: 8px; border-radius: 3px;
      max-height: 380px; overflow: auto;
      white-space: pre-wrap; word-break: break-word;
    }
    .diff { background: #1a1a1a; border-radius: 3px; padding: 6px; }
    .diff-line { white-space: pre-wrap; padding: 0 4px; }
    .diff-line.add { background: var(--add); color: var(--add-fg); }
    .diff-line.del { background: var(--del); color: var(--del-fg); }

    /* Markdown */
    .md p { margin: 0 0 6px; }
    .md p:last-child { margin: 0; }
    .md h1, .md h2, .md h3, .md h4 { margin: 12px 0 6px; color: #e2e8f0; }
    .md h1 { font-size: 1.4em; border-bottom: 1px solid var(--border); padding-bottom: 4px; }
    .md h2 { font-size: 1.2em; border-bottom: 1px solid var(--border); padding-bottom: 2px; }
    .md h3 { font-size: 1.08em; }
    .md ul, .md ol { margin: 4px 0 6px 22px; }
    .md li { margin-bottom: 2px; }
    .md blockquote {
      border-left: 3px solid var(--accent); margin: 6px 0;
      padding: 2px 10px; color: var(--muted); font-style: italic;
    }
    .md hr { border: none; border-top: 1px solid var(--border); margin: 10px 0; }
    .md strong { color: #e2e8f0; }
    .md code.inline {
      background: #2a2a2a; border: 1px solid #444; border-radius: 3px;
      padding: 0 4px; font-size: 0.88em; color: var(--warning);
      font-family: 'Consolas', monospace;
    }
    .md a { color: var(--accent); text-decoration: none; }
    .md a:hover { text-decoration: underline; }
    .md .codeblock {
      margin: 8px 0; border: 1px solid var(--border); border-radius: 4px; overflow: hidden;
    }
    .md .codeblock-head {
      display: flex; align-items: center; justify-content: space-between;
      background: var(--panel); padding: 4px 8px;
      font-size: 11px; color: var(--muted);
      font-family: 'Consolas', monospace;
    }
    .md .codeblock-copy {
      background: transparent; border: 1px solid #555; color: var(--muted);
      padding: 1px 8px; border-radius: 3px; font-size: 11px; cursor: pointer;
    }
    .md .codeblock-copy:hover { background: var(--accent); color: #fff; border-color: var(--accent); }
    .md .codeblock pre {
      margin: 0; padding: 8px; background: #1a1a1a; overflow-x: auto;
      font-family: 'Consolas', 'Courier New', monospace;
      font-size: 12px; line-height: 1.5; color: #d4d4d4;
    }

    /* Input */
    .input-area {
      padding: 8px 10px; background: var(--panel);
      border-top: 1px solid var(--border);
    }
    .attachments {
      display: flex; flex-wrap: wrap; gap: 4px; margin-bottom: 6px;
    }
    .attachments:empty { display: none; }
    .attachment-chip {
      display: inline-flex; align-items: center; gap: 5px;
      max-width: 100%;
      padding: 2px 6px; background: var(--panel-2);
      border: 1px solid var(--border); border-radius: 4px;
      font-size: 11px; color: var(--text);
    }
    .attachment-name {
      overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
      max-width: min(280px, 72vw);
    }
    .attachment-chip .remove {
      cursor: pointer; color: var(--muted); font-size: 14px; line-height: 1;
      background: transparent; border: none; padding: 0 1px; font: inherit;
    }
    .attachment-chip .remove:hover { color: var(--error); }

    .input-row { display: flex; gap: 6px; align-items: stretch; }
    .input-shell { flex: 1; position: relative; }
    textarea {
      width: 100%; padding: 8px 10px;
      background: var(--bg); color: var(--text);
      border: 1px solid var(--border); border-radius: 3px;
      font-family: 'Consolas', monospace;
      font-size: 13px; resize: none;
      min-height: 56px; max-height: 240px;
    }
    #message { padding-right: 28px; }
    .resize-grip {
      position: absolute; top: 6px; right: 7px;
      width: 14px; height: 14px; cursor: ns-resize;
      opacity: 0.55; border-radius: 2px; z-index: 2;
      background:
        linear-gradient(135deg, transparent 0 48%, var(--muted) 50% 56%, transparent 58%),
        linear-gradient(135deg, transparent 0 68%, var(--muted) 70% 76%, transparent 78%);
    }
    .resize-grip:hover { opacity: 0.9; }
    textarea:focus { outline: 2px solid var(--accent); outline-offset: -1px; }
    textarea.dragover { border-color: var(--accent); background: #1a2230; }
    .input-area.dragover textarea { border-color: var(--accent); background: #1a2230; }

    /* Slash menu / file menu */
    .menu {
      position: absolute; bottom: calc(100% + 4px);
      left: 0; right: 0; max-height: 220px; overflow-y: auto;
      background: var(--panel); border: 1px solid var(--border);
      border-radius: 4px; box-shadow: 0 4px 16px rgba(0,0,0,0.5);
      padding: 4px; z-index: 10;
    }
    .menu.hidden { display: none; }
    .menu-item {
      display: grid; grid-template-columns: 110px 1fr; gap: 8px;
      padding: 4px 8px; cursor: pointer; border-radius: 3px;
      font-size: 12px;
    }
    .menu-item.active, .menu-item:hover { background: #333842; }
    .menu-item.file { grid-template-columns: 1fr; }
    .menu-name { color: var(--success); font-weight: 600; }
    .menu-desc {
      color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    }
    .menu-file { color: var(--text); font-family: 'Consolas', monospace; font-size: 11.5px; }

    /* Sessions panel */
    .sessions-panel {
      position: absolute; top: 38px; right: 8px;
      width: 360px; max-height: 60vh; overflow-y: auto;
      background: var(--panel); border: 1px solid var(--border);
      border-radius: 4px; box-shadow: 0 8px 24px rgba(0,0,0,0.5);
      z-index: 50; display: none;
    }
    .sessions-panel.open { display: block; }
    .sessions-head {
      padding: 8px 10px; background: var(--accent); color: #fff;
      font-size: 12px; font-weight: 600;
      display: flex; justify-content: space-between; align-items: center;
    }
    .sessions-head .close {
      background: transparent; border: none; color: #fff;
      font-size: 16px; cursor: pointer; line-height: 1;
    }
    .sessions-empty { padding: 16px; text-align: center; color: var(--muted); font-size: 12px; }
    .session-item {
      padding: 8px 10px; border-bottom: 1px solid var(--border);
      cursor: pointer;
    }
    .session-item:hover { background: var(--panel-2); }
    .session-item.current { background: rgba(78,201,176,0.05); }
    .session-name {
      color: var(--text); font-size: 12.5px; font-weight: 600;
      overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    }
    .session-insight {
      color: var(--muted); font-size: 11.5px; margin-top: 2px;
      overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    }
    .session-meta { color: var(--muted); font-size: 11px; margin-top: 2px; }
    .session-current-tag {
      display: inline-block; margin-left: 6px;
      font-size: 10px; padding: 1px 5px;
      background: var(--success); color: #1a1a1a; border-radius: 3px;
    }
    .session-context-menu {
      position: fixed; min-width: 128px;
      background: var(--panel); border: 1px solid var(--border);
      border-radius: 4px; box-shadow: 0 8px 24px rgba(0,0,0,0.5);
      padding: 4px; z-index: 90;
    }
    .session-context-menu.hidden { display: none; }
    .session-context-menu button {
      display: block; width: 100%; text-align: left;
      background: transparent; color: var(--text); border: none;
      border-radius: 3px; padding: 5px 8px; cursor: pointer;
      font: inherit; font-size: 12px;
    }
    .session-context-menu button:hover { background: var(--panel-2); }
    .session-context-menu button.danger { color: var(--error); }

    /* Config panel */
    .config-panel {
      position: fixed; top: 35px; left: 0; right: 0; bottom: 25px;
      background: var(--bg); border-top: 1px solid var(--border);
      border-bottom: 1px solid var(--border);
      z-index: 80; display: none; flex-direction: column; min-height: 0;
    }
    .config-panel.open { display: flex; }
    .config-toolbar {
      padding: 8px 10px; background: var(--panel);
      border-bottom: 1px solid var(--border);
      display: grid; grid-template-columns: 1fr auto; gap: 8px; align-items: center;
    }
    .config-title { font-weight: 600; color: #e2e8f0; }
    .config-subtitle {
      margin-top: 2px; color: var(--muted); font-size: 11px;
      overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    }
    .config-actions { display: flex; gap: 6px; align-items: center; }
    .config-body {
      flex: 1; min-height: 0; display: grid;
      grid-template-columns: minmax(132px, 35%) minmax(0, 1fr);
    }
    .config-rail {
      overflow: auto; border-right: 1px solid var(--border);
      background: var(--panel);
      padding: 6px;
    }
    .config-tab {
      width: 100%; text-align: left; padding: 6px 8px;
      color: var(--text); background: transparent; border: none;
      border-radius: 3px; cursor: pointer; font: inherit; font-size: 12px;
    }
    .config-tab:hover { background: rgba(255,255,255,0.04); }
    .config-tab.active { background: var(--accent); color: #fff; }
    .config-fields { overflow: auto; padding: 10px; min-width: 0; }
    .config-section-head {
      padding-bottom: 8px; margin-bottom: 8px; border-bottom: 1px solid var(--border);
    }
    .config-section-title { color: #e2e8f0; font-size: 14px; font-weight: 700; }
    .config-section-tagline { color: var(--muted); font-size: 11px; margin-top: 2px; }
    .config-row {
      display: grid; grid-template-columns: minmax(112px, 34%) minmax(0, 1fr) auto;
      gap: 8px; align-items: start; padding: 6px 0;
      border-bottom: 1px solid rgba(255,255,255,0.045);
    }
    .config-label {
      color: var(--text); font-family: 'Consolas', monospace;
      font-size: 12px; padding-top: 5px; overflow-wrap: anywhere;
    }
    .config-label.active-role { color: var(--success); font-weight: 700; }
    .config-value { min-width: 0; }
    .config-input, .config-select, .config-textarea {
      width: 100%; background: #1a1a1a; color: var(--text);
      border: 1px solid var(--border); border-radius: 3px;
      font: inherit; font-size: 12px; min-height: 26px;
    }
    .config-input, .config-select { padding: 4px 7px; }
    .config-textarea {
      padding: 7px; resize: vertical; min-height: 122px;
      font-family: 'Consolas', monospace; line-height: 1.45;
    }
    .config-input:focus, .config-select:focus, .config-textarea:focus {
      outline: 1px solid var(--accent); border-color: var(--accent);
    }
    .config-meta {
      margin-top: 3px; color: var(--muted); font-size: 10.5px;
      overflow-wrap: anywhere;
    }
    .config-save {
      min-width: 48px; height: 26px;
    }
    .config-save.clean { visibility: hidden; }
    .config-empty, .config-error {
      color: var(--muted); padding: 18px 10px; text-align: center; font-size: 12px;
    }
    .config-error { color: var(--error); white-space: pre-wrap; text-align: left; }
    .config-status {
      padding: 5px 10px; border-top: 1px solid var(--border);
      background: var(--panel); color: var(--muted); font-size: 11px;
      min-height: 25px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
    }
    .config-aspects {
      margin-top: 14px; border-top: 1px solid var(--border); padding-top: 10px;
    }
    .aspect-row {
      display: grid; grid-template-columns: auto minmax(80px, 22%) minmax(0, 1fr) auto auto;
      gap: 6px; align-items: center; padding: 5px 0;
      border-bottom: 1px solid rgba(255,255,255,0.045);
    }
    .aspect-row input[type="checkbox"] { margin-top: 1px; }
    .aspect-name {
      font-family: 'Consolas', monospace; color: var(--text);
      overflow-wrap: anywhere;
    }
    .aspect-new {
      display: grid; grid-template-columns: minmax(80px, 24%) minmax(0, 1fr) auto;
      gap: 6px; margin-top: 8px;
    }
    .config-preview {
      margin-top: 10px; background: #1a1a1a; border: 1px solid var(--border);
      border-radius: 3px; padding: 8px; max-height: 280px; overflow: auto;
      white-space: pre-wrap; font-family: 'Consolas', monospace; font-size: 11px;
      color: var(--muted);
    }
    .config-preview .body { color: var(--success); }

    /* Status bar */
    .status-bar {
      display: flex; align-items: center; gap: 10px;
      padding: 4px 10px; background: var(--bg);
      border-top: 1px solid var(--border);
      font-size: 11px; color: var(--muted);
    }
    .status-dot { width: 8px; height: 8px; border-radius: 50%; background: #6a9955; }
    .status-dot.starting, .status-dot.working {
      background: var(--warning); animation: pulse 1.2s infinite;
    }
    .status-dot.failed { background: var(--error); }
    .status-dot.idle { background: var(--success); }
    @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.4; } }
    .status-meter { color: var(--muted); }
    .status-meter.warn { color: var(--warning); }
    .status-meter.error { color: var(--error); }
    .status-meter.hidden { display: none; }
    .status-spacer { margin-left: auto; }
    .queue-badge {
      padding: 1px 6px;
      background: var(--accent); color: #fff;
      border-radius: 3px; font-size: 10px;
    }
    .queue-badge.hidden { display: none; }
  </style>
</head>
<body>
  <div class="header">
    <div class="session" id="session-info">Cairo · starting…</div>
    <button class="btn" id="btn-sessions">Sessions</button>
    <button class="btn" id="btn-new" title="Start a new session">New</button>
    <button class="btn" id="btn-reload" title="Restart Cairo">Reload</button>
    <button class="btn" id="btn-clear" title="Clear transcript">Clear</button>
    <button class="btn" id="btn-config" title="Open settings">Config</button>
  </div>

  <div class="output" id="output"></div>

  <div class="sessions-panel" id="sessions-panel">
    <div class="sessions-head">
      <span>Sessions</span>
      <button class="close" id="sessions-close">×</button>
    </div>
    <div id="sessions-list"></div>
  </div>
  <div class="session-context-menu hidden" id="session-context-menu"></div>

  <div class="config-panel" id="config-panel">
    <div class="config-toolbar">
      <div>
        <div class="config-title">Cairo Config</div>
        <div class="config-subtitle" id="config-current">Loading…</div>
      </div>
      <div class="config-actions">
        <button class="btn" id="config-close">Close</button>
      </div>
    </div>
    <div class="config-body">
      <div class="config-rail" id="config-rail"></div>
      <div class="config-fields" id="config-fields">
        <div class="config-empty">Loading…</div>
      </div>
    </div>
    <div class="config-status" id="config-status"></div>
  </div>

  <div class="input-area">
    <div class="attachments" id="attachments"></div>
    <div class="input-row">
      <div class="input-shell">
        <div class="menu hidden" id="menu"></div>
        <div class="resize-grip" id="message-resize"></div>
        <textarea id="message" rows="2"
                  placeholder="Type a message, /command, or @file — Enter sends, Shift+Enter adds a line. Drop files to attach."></textarea>
      </div>
      <button class="btn primary" id="btn-send">Send</button>
      <button class="btn danger" id="btn-stop">Stop</button>
    </div>
  </div>

  <div class="status-bar">
    <div class="status-dot" id="status-dot"></div>
    <span id="status-text">starting</span>
    <span class="status-meter hidden" id="status-elapsed"></span>
    <span class="status-meter hidden" id="token-meter"></span>
    <span class="status-spacer"></span>
    <span class="queue-badge hidden" id="queue-badge">0 queued</span>
  </div>

  <script nonce="${nonce}">
    (function () {
      const vscode = acquireVsCodeApi();
      const slashCommands = ${slashJson};

      const outputEl = document.getElementById('output');
      const messageEl = document.getElementById('message');
      const sendBtn = document.getElementById('btn-send');
      const stopBtn = document.getElementById('btn-stop');
      const newBtn = document.getElementById('btn-new');
      const reloadBtn = document.getElementById('btn-reload');
      const clearBtn = document.getElementById('btn-clear');
      const configBtn = document.getElementById('btn-config');
      const sessionsBtn = document.getElementById('btn-sessions');
      const menuEl = document.getElementById('menu');
      const statusDot = document.getElementById('status-dot');
      const statusText = document.getElementById('status-text');
      const statusElapsedEl = document.getElementById('status-elapsed');
      const tokenMeterEl = document.getElementById('token-meter');
      const queueBadge = document.getElementById('queue-badge');
      const sessionInfoEl = document.getElementById('session-info');
      const attachmentsEl = document.getElementById('attachments');
      const inputAreaEl = document.querySelector('.input-area');
      const sessionsPanel = document.getElementById('sessions-panel');
      const sessionsListEl = document.getElementById('sessions-list');
      const sessionsClose = document.getElementById('sessions-close');
      const sessionContextMenuEl = document.getElementById('session-context-menu');
      const configPanel = document.getElementById('config-panel');
      const configRailEl = document.getElementById('config-rail');
      const configFieldsEl = document.getElementById('config-fields');
      const configCurrentEl = document.getElementById('config-current');
      const configStatusEl = document.getElementById('config-status');
      const configCloseBtn = document.getElementById('config-close');
      const messageResizeGrip = document.getElementById('message-resize');

      let processing = false;
      let scrollPinned = false;
      const attachments = [];          // [{ display, path }]
      let menuMode = 'none';           // 'slash' | 'file' | 'none'
      let menuItems = [];
      let menuSelected = 0;
      let pendingFileQuery = '';
      let assistantBlock = null;       // current run's assistant text container
      let assistantSettled = null;     // child holding finalized rendered HTML
      let assistantTail = null;        // child holding the in-progress markdown
      let assistantSettledLen = 0;     // bytes of assistantBuffer already in settled
      let assistantBuffer = '';
      let assistantFrame = 0;
      let thinkingFrame = 0;
      let streamChars = 0;
      const toolCards = new Map();     // run|name|seq → element
      const runContainers = new Map(); // run → containing element
      let currentRun = 0;
      let configState = null;
      let runtimeInfo = null;
      let contextLen = 0;
      let statusState = 'starting';
      let statusStartedAt = 0;
      let activeConfigSection = 'LLM Backend';

      // ---------------------------------------------------------------- utils
      function escapeHtml(s) {
        return String(s == null ? '' : s)
          .replace(/&/g, '&amp;').replace(/</g, '&lt;')
          .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
      }
      function pinIfNeeded() {
        if (!scrollPinned) outputEl.scrollTop = outputEl.scrollHeight;
      }

      // ---------------------------------------------------------- markdown
      function inlineMd(s) {
        // Process inline code first so its contents don't get further parsed.
        const parts = s.split(/(\`[^\`]+\`)/);
        for (let i = 0; i < parts.length; i++) {
          if (i % 2 === 1) {
            parts[i] = '<code class="inline">' + escapeHtml(parts[i].slice(1, -1)) + '</code>';
          } else {
            let t = escapeHtml(parts[i]);
            t = t.replace(/\\*\\*([^*\\n]+)\\*\\*/g, '<strong>$1</strong>');
            t = t.replace(/\\*([^*\\n]+)\\*/g, '<em>$1</em>');
            t = t.replace(/~~([^~\\n]+)~~/g, '<del>$1</del>');
            t = t.replace(/\\[([^\\]]+)\\]\\((https?:[^)]+)\\)/g,
              '<a href="$2">$1</a>');
            parts[i] = t;
          }
        }
        return parts.join('');
      }
      function renderMarkdown(text) {
        const lines = text.split('\\n');
        let html = '';
        let i = 0;
        while (i < lines.length) {
          const line = lines[i];
          const fence = line.match(/^\`\`\`(.*)$/);
          if (fence) {
            const lang = (fence[1] || '').trim() || 'text';
            const codeLines = [];
            i++;
            while (i < lines.length && !/^\`\`\`/.test(lines[i])) {
              codeLines.push(lines[i]); i++;
            }
            html += '<div class="codeblock">'
                  + '<div class="codeblock-head"><span>' + escapeHtml(lang) + '</span>'
                  + '<button class="codeblock-copy" type="button">Copy</button></div>'
                  + '<pre><code>' + escapeHtml(codeLines.join('\\n')) + '</code></pre>'
                  + '</div>';
            i++; continue;
          }
          const h = line.match(/^(#{1,6})\\s+(.+)/);
          if (h) {
            const lvl = Math.min(h[1].length, 4);
            html += '<h' + lvl + '>' + inlineMd(h[2]) + '</h' + lvl + '>';
            i++; continue;
          }
          if (/^>\\s/.test(line)) {
            html += '<blockquote>' + inlineMd(line.replace(/^>\\s?/, '')) + '</blockquote>';
            i++; continue;
          }
          if (/^([-*_])\\1{2,}\\s*$/.test(line.trim())) {
            html += '<hr>'; i++; continue;
          }
          if (/^[-*+]\\s/.test(line)) {
            html += '<ul>';
            while (i < lines.length && /^[-*+]\\s/.test(lines[i])) {
              html += '<li>' + inlineMd(lines[i].replace(/^[-*+]\\s+/, '')) + '</li>';
              i++;
            }
            html += '</ul>'; continue;
          }
          if (/^\\d+\\.\\s/.test(line)) {
            html += '<ol>';
            while (i < lines.length && /^\\d+\\.\\s/.test(lines[i])) {
              html += '<li>' + inlineMd(lines[i].replace(/^\\d+\\.\\s+/, '')) + '</li>';
              i++;
            }
            html += '</ol>'; continue;
          }
          if (line.trim() === '') {
            html += '<div style="height:4px"></div>'; i++; continue;
          }
          html += '<p>' + inlineMd(line) + '</p>';
          i++;
        }
        return html;
      }

      function attachCopyHandlers(root) {
        root.querySelectorAll('.codeblock-copy').forEach(function (btn) {
          if (btn.dataset.bound) return;
          btn.dataset.bound = '1';
          btn.addEventListener('click', function () {
            const code = btn.closest('.codeblock').querySelector('code');
            navigator.clipboard.writeText(code.textContent).then(function () {
              btn.textContent = 'Copied';
              setTimeout(function () { btn.textContent = 'Copy'; }, 1500);
            });
          });
        });
      }

      // ---------------------------------------------------------- containers
      function getRunContainer(runId) {
        if (!runId) return outputEl;
        let el = runContainers.get(runId);
        if (!el) {
          el = document.createElement('div');
          el.className = 'run';
          el.dataset.run = String(runId);
          outputEl.appendChild(el);
          runContainers.set(runId, el);
        }
        return el;
      }

      function startNewRun(runId) {
        currentRun = runId;
        resetAssistantSegment();
        resetThinkingSegment();
        streamChars = 0;
        updateStatusReadouts();
      }

      function resetAssistantSegment() {
        flushAssistantRender();
        assistantBlock = null;
        assistantSettled = null;
        assistantTail = null;
        assistantSettledLen = 0;
        assistantBuffer = '';
      }

      function resetThinkingSegment() {
        flushThinkingRender();
        thinkingBlock = null;
        thinkingBuffer = '';
      }

      // -------------------------------------------------- text/system/error
      function addText(text, style, runId) {
        const container = getRunContainer(runId);
        resetAssistantSegment();
        resetThinkingSegment();
        const div = document.createElement('div');
        if (style === 'system') div.className = 'msg-system';
        else if (style === 'error') div.className = 'msg-error';
        else div.className = 'msg-system';
        div.textContent = text;
        container.appendChild(div);
        pinIfNeeded();
      }

      function addUserMessage(text) {
        resetAssistantSegment();
        resetThinkingSegment();
        const div = document.createElement('div');
        div.className = 'msg-user';
        div.textContent = text;
        outputEl.appendChild(div);
        pinIfNeeded();
      }

      // ---------------------------------------------------------- assistant
      // Streaming strategy: split the assistant block into a "settled" child
      // that holds already-finalized rendered HTML and a "tail" child that
      // holds whatever paragraph is still being typed. We only re-render the
      // tail on each frame; the settled portion is appended to once when a
      // paragraph break (\\n\\n outside any open code fence) advances. This
      // turns total markdown work from O(n²) into O(n) for long responses.
      function addAssistantTokens(text, runId) {
        const container = getRunContainer(runId);
        if (!assistantBlock || assistantBlock.dataset.run !== String(runId)) {
          flushAssistantRender();
          assistantBlock = document.createElement('div');
          assistantBlock.className = 'msg-assistant md';
          assistantBlock.dataset.run = String(runId);
          assistantSettled = document.createElement('div');
          assistantSettled.className = 'md-settled';
          assistantTail = document.createElement('div');
          assistantTail.className = 'md-tail';
          assistantBlock.appendChild(assistantSettled);
          assistantBlock.appendChild(assistantTail);
          assistantBuffer = '';
          assistantSettledLen = 0;
          container.appendChild(assistantBlock);
        }
        streamChars += String(text || '').length;
        assistantBuffer += text;
        scheduleAssistantRender();
      }

      function scheduleAssistantRender() {
        if (assistantFrame) return;
        assistantFrame = requestAnimationFrame(function () {
          assistantFrame = 0;
          renderAssistantNow();
        });
      }

      function flushAssistantRender() {
        if (assistantFrame) {
          cancelAnimationFrame(assistantFrame);
          assistantFrame = 0;
        }
        renderAssistantNow();
      }

      // Walk forward from fromIndex tracking code-fence state. Return the
      // largest index ≤ buf.length such that buf[fromIndex..ret) ends at a
      // \\n\\n boundary that is OUTSIDE any open code fence. Anything before
      // that index is safe to commit as final HTML.
      function findSettleBoundary(buf, fromIndex) {
        let inFence = false;
        let i = fromIndex;
        let lastBoundary = fromIndex;
        while (i < buf.length) {
          const nl = buf.indexOf('\\n', i);
          if (nl === -1) break;
          const line = buf.slice(i, nl);
          if (/^\\s*\`\`\`/.test(line)) {
            inFence = !inFence;
          }
          if (!inFence && buf.charCodeAt(nl + 1) === 10 /* \\n */) {
            lastBoundary = nl + 2;
          }
          i = nl + 1;
        }
        return lastBoundary;
      }

      function renderAssistantNow() {
        if (!assistantBlock) return;
        const boundary = findSettleBoundary(assistantBuffer, assistantSettledLen);
        if (boundary > assistantSettledLen) {
          const chunk = assistantBuffer.slice(assistantSettledLen, boundary);
          const tmp = document.createElement('div');
          tmp.innerHTML = renderMarkdown(chunk);
          while (tmp.firstChild) {
            assistantSettled.appendChild(tmp.firstChild);
          }
          attachCopyHandlers(assistantSettled);
          assistantSettledLen = boundary;
        }
        const tail = assistantBuffer.slice(assistantSettledLen);
        if (tail) {
          assistantTail.innerHTML = renderMarkdown(tail);
          attachCopyHandlers(assistantTail);
        } else if (assistantTail.innerHTML) {
          assistantTail.innerHTML = '';
        }
        updateStatusReadouts();
        pinIfNeeded();
      }

      // ---------------------------------------------------------- thinking
      let thinkingBlock = null;
      let thinkingBuffer = '';
      function addThinking(text, runId) {
        const container = getRunContainer(runId);
        resetAssistantSegment();
        if (!thinkingBlock || thinkingBlock.dataset.run !== String(runId)) {
          thinkingBlock = document.createElement('details');
          thinkingBlock.className = 'msg-thinking';
          thinkingBlock.dataset.run = String(runId);
          thinkingBuffer = '';
          const summary = document.createElement('summary');
          summary.textContent = 'Reasoning…';
          thinkingBlock.appendChild(summary);
          const body = document.createElement('div');
          body.className = 'thinking-body';
          thinkingBlock.appendChild(body);
          container.appendChild(thinkingBlock);
        }
        thinkingBuffer += text;
        scheduleThinkingRender();
      }

      function scheduleThinkingRender() {
        if (thinkingFrame) return;
        thinkingFrame = requestAnimationFrame(function () {
          thinkingFrame = 0;
          renderThinkingNow();
        });
      }

      function flushThinkingRender() {
        if (thinkingFrame) {
          cancelAnimationFrame(thinkingFrame);
          thinkingFrame = 0;
        }
        renderThinkingNow();
      }

      function renderThinkingNow() {
        if (!thinkingBlock) return;
        thinkingBlock.querySelector('.thinking-body').textContent = thinkingBuffer;
        pinIfNeeded();
      }

      // ---------------------------------------------------------------- diff
      function renderDiffBody(args) {
        const oldT = String(args.old_text || '');
        const newT = String(args.new_text || '');
        if (!oldT && !newT) return null;
        const wrap = document.createElement('div');
        wrap.className = 'diff';
        oldT.split('\\n').forEach(function (line) {
          const div = document.createElement('div');
          div.className = 'diff-line del';
          div.textContent = '- ' + line;
          wrap.appendChild(div);
        });
        newT.split('\\n').forEach(function (line) {
          const div = document.createElement('div');
          div.className = 'diff-line add';
          div.textContent = '+ ' + line;
          wrap.appendChild(div);
        });
        return wrap;
      }

      // ---------------------------------------------------------- tool cards
      const TOOL_ICONS = {
        bash: '$', read: '📄', write: '✎', edit: '✎',
        search: '🔍', fetch: '🌐', memory_tool: '🧠', memory: '🧠',
        skill: '★', task: '⚙', agent: '🤖', learn: '📚',
        worktree: '🌳', merge_job: '⚙', config: '⚙',
        prompt_part: '📝', choose: '?'
      };
      function toolIcon(name) {
        return TOOL_ICONS[name] || '⚙';
      }
      function toolArgPreview(name, args) {
        if (!args) return '';
        const pick = function () {
          for (let i = 0; i < arguments.length; i++) {
            const v = args[arguments[i]];
            if (v != null && String(v).trim()) return String(v).replace(/\\s+/g, ' ').trim();
          }
          return '';
        };
        switch (name) {
          case 'bash': return pick('command');
          case 'read': case 'write': case 'edit': return pick('path');
          case 'fetch': return pick('url');
          case 'search': return pick('query');
          case 'learn': return pick('path');
          default: return pick('path','url','query','command','name','title','action','id');
        }
      }

      function toolCardKey(runId, name) {
        // The bus emits tool_start/end strictly in pairs in cairo, so the
        // most-recent unfinished card with this name in the same run is
        // the one to update. We track that by sequence in toolCards Map
        // keyed by run + name + a per-run counter we increment on each
        // tool_start.
        return runId + '|' + name;
      }

      function makeToolCard(name, args, runId) {
        const card = document.createElement('div');
        card.className = 'tool';
        const head = document.createElement('div');
        head.className = 'tool-head';
        head.innerHTML =
          '<span class="tool-icon">' + escapeHtml(toolIcon(name)) + '</span>'
          + '<span class="tool-name">' + escapeHtml(name) + '</span>'
          + '<span class="tool-arg"></span>'
          + '<span class="tool-status"><span class="spinner"></span>running</span>';
        head.querySelector('.tool-arg').textContent = toolArgPreview(name, args);
        head.addEventListener('click', function () {
          card.classList.toggle('open');
        });
        card.appendChild(head);

        const body = document.createElement('div');
        body.className = 'tool-body';
        // Args section
        const argsSec = document.createElement('div');
        argsSec.className = 'tool-section';
        argsSec.innerHTML = '<div class="tool-section-label">Arguments</div>';
        if (name === 'edit' && (args.old_text || args.new_text)) {
          const path = args.path ? '<div style="color:var(--muted);margin-bottom:4px">' + escapeHtml(String(args.path)) + '</div>' : '';
          if (path) {
            const meta = document.createElement('div');
            meta.innerHTML = path;
            argsSec.appendChild(meta);
          }
          const diff = renderDiffBody(args);
          if (diff) argsSec.appendChild(diff);
        } else if (name === 'bash' && args.command) {
          const pre = document.createElement('div');
          pre.className = 'tool-pre';
          pre.textContent = String(args.command);
          argsSec.appendChild(pre);
        } else if (name === 'write' && args.content != null) {
          const path = args.path ? '<div style="color:var(--muted);margin-bottom:4px">' + escapeHtml(String(args.path)) + '</div>' : '';
          const meta = document.createElement('div');
          meta.innerHTML = path;
          argsSec.appendChild(meta);
          const pre = document.createElement('div');
          pre.className = 'tool-pre';
          pre.textContent = String(args.content);
          argsSec.appendChild(pre);
        } else {
          const pre = document.createElement('div');
          pre.className = 'tool-pre';
          try { pre.textContent = JSON.stringify(args, null, 2); }
          catch { pre.textContent = String(args); }
          argsSec.appendChild(pre);
        }
        body.appendChild(argsSec);

        // Output section (filled on tool_update / tool_end)
        const outSec = document.createElement('div');
        outSec.className = 'tool-section tool-output-section';
        outSec.style.display = 'none';
        outSec.innerHTML = '<div class="tool-section-label">Output</div>';
        const outPre = document.createElement('div');
        outPre.className = 'tool-pre tool-output';
        outSec.appendChild(outPre);
        body.appendChild(outSec);

        card.appendChild(body);
        return card;
      }

      function toolStart(runId, name, args) {
        const container = getRunContainer(runId);
        resetAssistantSegment();
        resetThinkingSegment();
        const card = makeToolCard(name, args || {}, runId);
        container.appendChild(card);
        toolCards.set(toolCardKey(runId, name), card);
        pinIfNeeded();
      }
      function toolUpdate(runId, name, output) {
        const card = toolCards.get(toolCardKey(runId, name));
        if (!card) return;
        const sec = card.querySelector('.tool-output-section');
        const pre = card.querySelector('.tool-output');
        sec.style.display = 'block';
        pre.textContent = output;
        pinIfNeeded();
      }
      function toolEnd(runId, name, result, isError, endedAt) {
        const card = toolCards.get(toolCardKey(runId, name));
        if (!card) return;
        toolCards.delete(toolCardKey(runId, name));
        if (isError) card.classList.add('error');
        const status = card.querySelector('.tool-status');
        status.classList.add(isError ? 'failed' : 'done');
        status.innerHTML = isError ? 'failed' : 'done';
        if (result) {
          const sec = card.querySelector('.tool-output-section');
          const pre = card.querySelector('.tool-output');
          sec.style.display = 'block';
          pre.textContent = result;
        }
        pinIfNeeded();
      }

      function runEnd(runId, failed, elapsedMs) {
        const container = getRunContainer(runId);
        if (!container) return;
        const summary = document.createElement('div');
        summary.className = 'run-summary' + (failed ? ' failed' : '');
        const sec = Math.max(1, Math.round((elapsedMs || 0) / 1000));
        summary.textContent = (failed ? 'Run failed · ' : 'Done · ') + sec + 's';
        container.appendChild(summary);
        pinIfNeeded();
      }

      // ---------------------------------------------------------- attachments
      function renderAttachments() {
        attachmentsEl.textContent = '';
        attachments.forEach(function (a, idx) {
          const chip = document.createElement('span');
          chip.className = 'attachment-chip';
          const label = document.createElement('span');
          label.className = 'attachment-name';
          label.textContent = '@' + a.display;
          chip.title = a.path;
          chip.appendChild(label);
          const x = document.createElement('button');
          x.type = 'button';
          x.className = 'remove';
          x.textContent = '×';
          x.title = 'Remove attachment';
          x.setAttribute('aria-label', 'Remove ' + a.display);
          x.addEventListener('click', function () {
            attachments.splice(idx, 1);
            renderAttachments();
          });
          chip.appendChild(x);
          attachmentsEl.appendChild(chip);
        });
      }
      function addAttachment(displayPath, fullPath) {
        const pathValue = String(fullPath || '').trim();
        if (!pathValue) return;
        if (attachments.some(function (a) { return a.path === pathValue; })) return;
        const display = String(displayPath || pathValue).replace(/^@+/, '');
        attachments.push({ display: display, path: pathValue });
        renderAttachments();
      }

      // ---------------------------------------------------------------- send
      function send() {
        const text = messageEl.value;
        if (!text.trim() && attachments.length === 0) return;
        vscode.postMessage({
          type: 'send',
          message: text,
          attachments: attachments.map(function (a) { return a.path; }),
        });
        messageEl.value = '';
        attachments.length = 0;
        renderAttachments();
        hideMenu();
      }

      // ---------------------------------------------------------- input menu
      function hideMenu() {
        menuMode = 'none';
        menuItems = [];
        menuEl.classList.add('hidden');
      }
      function renderMenu() {
        menuEl.textContent = '';
        if (menuItems.length === 0) { hideMenu(); return; }
        menuItems.forEach(function (item, i) {
          const row = document.createElement('div');
          row.className = 'menu-item' + (menuMode === 'file' ? ' file' : '') + (i === menuSelected ? ' active' : '');
          if (menuMode === 'file') {
            const f = document.createElement('span');
            f.className = 'menu-file';
            f.textContent = item;
            row.appendChild(f);
          } else {
            const n = document.createElement('span');
            n.className = 'menu-name';
            n.textContent = '/' + item.name;
            const d = document.createElement('span');
            d.className = 'menu-desc';
            d.textContent = item.desc;
            row.appendChild(n);
            row.appendChild(d);
          }
          row.addEventListener('mousedown', function (ev) {
            ev.preventDefault();
            applyMenu(item);
          });
          menuEl.appendChild(row);
        });
        menuEl.classList.remove('hidden');
      }
      function showSlashMenu(query) {
        menuMode = 'slash';
        const q = (query || '').toLowerCase();
        menuItems = q
          ? slashCommands.filter(function (c) {
              return c.name.includes(q) || c.desc.toLowerCase().includes(q);
            })
          : slashCommands.slice();
        menuSelected = 0;
        renderMenu();
      }
      function showFileMenu(items) {
        menuMode = 'file';
        menuItems = items.slice(0, 20);
        menuSelected = 0;
        renderMenu();
      }
      function applyMenu(item) {
        if (!item) return;
        if (menuMode === 'slash') {
          messageEl.value = '/' + item.name + ' ';
          messageEl.focus();
          const len = messageEl.value.length;
          messageEl.setSelectionRange(len, len);
        } else if (menuMode === 'file') {
          // Replace the @<query> at the cursor with @<chosen> and turn it
          // into an attachment chip.
          const v = messageEl.value;
          const cursor = messageEl.selectionStart;
          const before = v.slice(0, cursor);
          const after = v.slice(cursor);
          const m = before.match(/@(\\S*)$/);
          if (m) {
            messageEl.value = before.slice(0, before.length - m[0].length) + after;
          }
          vscode.postMessage({ type: 'attach-by-rel', rels: [item] });
        }
        hideMenu();
      }

      function maybeUpdateMenu() {
        const v = messageEl.value;
        const cursor = messageEl.selectionStart;
        const before = v.slice(0, cursor);
        // Slash menu when input begins with "/" and no whitespace yet.
        if (v.startsWith('/') && !/\\s/.test(v)) {
          showSlashMenu(v.slice(1).toLowerCase());
          return;
        }
        // @file mention: any non-whitespace chunk after @
        const m = before.match(/(?:^|\\s)@([^\\s@]*)$/);
        if (m) {
          const query = m[1];
          pendingFileQuery = query;
          vscode.postMessage({ type: 'search-files', query: query });
          return;
        }
        hideMenu();
      }

      // ---------------------------------------------------- buttons & events
      sendBtn.addEventListener('click', function (e) { e.preventDefault(); send(); });
      stopBtn.addEventListener('click', function (e) {
        e.preventDefault();
        vscode.postMessage({ type: 'stop' });
      });
      newBtn.addEventListener('click', function () { vscode.postMessage({ type: 'command', name: 'new' }); });
      reloadBtn.addEventListener('click', function () { vscode.postMessage({ type: 'command', name: 'reload' }); });
      clearBtn.addEventListener('click', function () { vscode.postMessage({ type: 'command', name: 'clear' }); });
      configBtn.addEventListener('click', function () { toggleConfigPanel(); });
      configCloseBtn.addEventListener('click', closeConfigPanel);
      sessionsBtn.addEventListener('click', function () {
        sessionsPanel.classList.toggle('open');
        if (sessionsPanel.classList.contains('open')) {
          vscode.postMessage({ type: 'list-sessions' });
        }
      });
      sessionsClose.addEventListener('click', function () {
        sessionsPanel.classList.remove('open');
      });

      messageResizeGrip.addEventListener('mousedown', function (e) {
        e.preventDefault();
        const startY = e.clientY;
        const startH = messageEl.offsetHeight;
        const minH = 56;
        const maxH = 240;
        function onMove(ev) {
          const next = Math.max(minH, Math.min(maxH, startH - (ev.clientY - startY)));
          messageEl.style.height = next + 'px';
        }
        function onUp() {
          window.removeEventListener('mousemove', onMove);
          window.removeEventListener('mouseup', onUp);
        }
        window.addEventListener('mousemove', onMove);
        window.addEventListener('mouseup', onUp);
      });

      messageEl.addEventListener('input', maybeUpdateMenu);
      messageEl.addEventListener('keydown', function (e) {
        const menuVisible = !menuEl.classList.contains('hidden');
        if (menuVisible && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
          e.preventDefault();
          if (e.key === 'ArrowDown') {
            menuSelected = (menuSelected + 1) % menuItems.length;
          } else {
            menuSelected = (menuSelected - 1 + menuItems.length) % menuItems.length;
          }
          renderMenu();
          return;
        }
        if (menuVisible && (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey))) {
          e.preventDefault();
          applyMenu(menuItems[menuSelected]);
          return;
        }
        if (menuVisible && e.key === 'Escape') {
          e.preventDefault();
          hideMenu();
          return;
        }
        if (e.key === 'Enter' && !e.shiftKey) {
          e.preventDefault();
          send();
        }
      });

      // Drag-and-drop file attachments into the input area.
      function pathFromFileUri(uri) {
        try {
          const parsed = new URL(uri);
          if (parsed.protocol !== 'file:') return '';
          let p = decodeURIComponent(parsed.pathname || '');
          if (/^\\/[A-Za-z]:/.test(p)) p = p.slice(1);
          return p;
        } catch {
          return '';
        }
      }
      function isAbsolutePath(value) {
        return /^\\//.test(value) || /^[A-Za-z]:[\\\\/]/.test(value);
      }
      function extractPathsFromText(text) {
        const out = [];
        String(text || '').split(/\\r?\\n/).forEach(function (raw) {
          const item = raw.trim();
          if (!item || item[0] === '#') return;
          if (item.startsWith('file://')) {
            const p = pathFromFileUri(item);
            if (p) out.push(p);
          } else if (isAbsolutePath(item)) {
            out.push(item);
          }
        });
        return out;
      }
      function postDroppedPaths(paths) {
        const unique = [];
        paths.forEach(function (p) {
          if (p && unique.indexOf(p) === -1) unique.push(p);
        });
        if (unique.length) vscode.postMessage({ type: 'attach-by-abs', paths: unique });
      }
      function collectDropPaths(dt) {
        const paths = [];
        if (!dt) return Promise.resolve(paths);

        ['text/uri-list', 'text/plain', 'application/vnd.code.uri-list'].forEach(function (type) {
          paths.push.apply(paths, extractPathsFromText(dt.getData(type)));
        });

        Array.from(dt.files || []).forEach(function (file) {
          const p = file && file.path ? String(file.path) : '';
          if (isAbsolutePath(p)) paths.push(p);
        });

        const itemReads = Array.from(dt.items || [])
          .filter(function (it) { return it.kind === 'string'; })
          .map(function (it) {
            return new Promise(function (resolve) {
              it.getAsString(function (s) {
                resolve(extractPathsFromText(s));
              });
            });
          });

        return Promise.all(itemReads).then(function (all) {
          all.forEach(function (list) { paths.push.apply(paths, list); });
          return paths;
        });
      }
      function setDropActive(active) {
        inputAreaEl.classList.toggle('dragover', active);
        messageEl.classList.toggle('dragover', active);
      }
      inputAreaEl.addEventListener('dragover', function (e) {
        e.preventDefault();
        setDropActive(true);
      });
      inputAreaEl.addEventListener('dragenter', function (e) {
        e.preventDefault();
        setDropActive(true);
      });
      inputAreaEl.addEventListener('dragleave', function (e) {
        if (!e.relatedTarget || !inputAreaEl.contains(e.relatedTarget)) setDropActive(false);
      });
      inputAreaEl.addEventListener('drop', function (e) {
        e.preventDefault();
        setDropActive(false);
        collectDropPaths(e.dataTransfer).then(postDroppedPaths);
      });

      outputEl.addEventListener('scroll', function () {
        const dist = outputEl.scrollHeight - outputEl.scrollTop - outputEl.clientHeight;
        scrollPinned = dist > 60;
      });

      // ----------------------------------------------------- session panel
      function hideSessionContextMenu() {
        sessionContextMenuEl.classList.add('hidden');
        sessionContextMenuEl.textContent = '';
      }
      function sessionAction(action, session) {
        hideSessionContextMenu();
        if (action === 'open') {
          if (session.current) {
            sessionsPanel.classList.remove('open');
            return;
          }
          vscode.postMessage({ type: 'open-session', id: session.id });
          sessionsPanel.classList.remove('open');
          return;
        }
        if (action === 'rename') {
          vscode.postMessage({
            type: 'rename-session',
            id: session.id,
            name: session.name || '',
            fallback: session.insight || '',
          });
          return;
        }
        if (action === 'delete') {
          vscode.postMessage({
            type: 'delete-session',
            id: session.id,
            name: session.name || '',
            fallback: session.insight || '',
            current: !!session.current,
          });
        }
      }
      function showSessionContextMenu(session, x, y) {
        sessionContextMenuEl.textContent = '';
        [
          { action: 'open', label: 'Open' },
          { action: 'rename', label: 'Rename' },
          { action: 'delete', label: 'Delete', danger: true },
        ].forEach(function (item) {
          const btn = document.createElement('button');
          btn.type = 'button';
          btn.textContent = item.label;
          if (item.danger) btn.className = 'danger';
          btn.addEventListener('click', function () {
            sessionAction(item.action, session);
          });
          sessionContextMenuEl.appendChild(btn);
        });
        sessionContextMenuEl.classList.remove('hidden');
        const width = 132;
        const height = 96;
        const left = Math.max(4, Math.min(x, window.innerWidth - width - 4));
        const top = Math.max(4, Math.min(y, window.innerHeight - height - 4));
        sessionContextMenuEl.style.left = left + 'px';
        sessionContextMenuEl.style.top = top + 'px';
      }
      document.addEventListener('click', function (e) {
        if (!sessionContextMenuEl.contains(e.target)) hideSessionContextMenu();
      });
      window.addEventListener('blur', hideSessionContextMenu);
      sessionsPanel.addEventListener('scroll', hideSessionContextMenu);

      function renderSessions(list) {
        sessionsListEl.textContent = '';
        hideSessionContextMenu();
        if (!list || list.length === 0) {
          const empty = document.createElement('div');
          empty.className = 'sessions-empty';
          empty.textContent = 'No sessions yet';
          sessionsListEl.appendChild(empty);
          return;
        }
        list.forEach(function (s) {
          const item = document.createElement('div');
          item.className = 'session-item' + (s.current ? ' current' : '');
          item.title = 'Open session. Right-click for actions.';
          const name = document.createElement('div');
          name.className = 'session-name';
          const title = s.name || s.insight || 'Session ' + s.id;
          name.textContent = '[' + s.id + '] ' + title;
          if (s.current) {
            const tag = document.createElement('span');
            tag.className = 'session-current-tag';
            tag.textContent = 'current';
            name.appendChild(tag);
          }
          const insight = document.createElement('div');
          insight.className = 'session-insight';
          insight.textContent = s.name && s.insight ? s.insight : '';
          const meta = document.createElement('div');
          meta.className = 'session-meta';
          meta.textContent = s.role + ' · ' + s.lastActive;
          item.appendChild(name);
          if (insight.textContent) item.appendChild(insight);
          item.appendChild(meta);
          item.addEventListener('click', function () {
            if (s.current) {
              sessionsPanel.classList.remove('open');
              return;
            }
            vscode.postMessage({ type: 'open-session', id: s.id });
            sessionsPanel.classList.remove('open');
          });
          item.addEventListener('contextmenu', function (e) {
            e.preventDefault();
            showSessionContextMenu(s, e.clientX, e.clientY);
          });
          sessionsListEl.appendChild(item);
        });
      }

      // ---------------------------------------------------------- config
      function setConfigStatus(text, isError) {
        configStatusEl.textContent = text || '';
        configStatusEl.style.color = isError ? 'var(--error)' : 'var(--muted)';
      }
      function openConfigPanel() {
        configPanel.classList.add('open');
        configBtn.classList.add('active');
        setConfigStatus('Loading Cairo config…');
        vscode.postMessage({ type: 'get-config' });
      }
      function closeConfigPanel() {
        configPanel.classList.remove('open');
        configBtn.classList.remove('active');
      }
      function toggleConfigPanel() {
        if (configPanel.classList.contains('open')) closeConfigPanel();
        else openConfigPanel();
      }
      function parseRoleConfigKey(key) {
        const m = String(key || '').match(/^role:([^:]+):(model|think)$/);
        return m ? { role: m[1], field: m[2] } : null;
      }
      function snapshotRole(name) {
        const roles = configState && configState.snapshot ? configState.snapshot.roles || [] : [];
        return roles.find(function (r) { return r.name === name; }) || null;
      }
      function snapshotValue(key) {
        if (!configState || !configState.snapshot) return '';
        const roleKey = parseRoleConfigKey(key);
        if (roleKey) {
          const r = snapshotRole(roleKey.role);
          return r ? String(r[roleKey.field] || '') : '';
        }
        return String((configState.snapshot.config || {})[key] || '');
      }
      function saveConfigKey(key, value) {
        setConfigStatus('Saving ' + key + '…');
        vscode.postMessage({ type: 'config-save', key: key, value: value });
      }
      function modelOptions(current, inheritLabel) {
        const models = (configState && configState.models) || [];
        const out = [{ value: '', label: inheritLabel || '(empty)' }];
        if (current && models.indexOf(current) === -1) {
          out.push({ value: current, label: current + ' (current)' });
        }
        models.forEach(function (m) { out.push({ value: m, label: m }); });
        return out;
      }
      function makeOption(value, label, selected) {
        const opt = document.createElement('option');
        opt.value = value;
        opt.textContent = label == null ? value : label;
        opt.selected = selected;
        return opt;
      }
      function renderConfigInput(field, current) {
        if (field.kind === 'model' || field.kind === 'role-model') {
          if (configState && configState.models && configState.models.length) {
            const select = document.createElement('select');
            select.className = 'config-select';
            const inheritLabel = field.kind === 'role-model' ? '(inherit global)' : '(empty)';
            modelOptions(current, inheritLabel).forEach(function (o) {
              select.appendChild(makeOption(o.value, o.label, o.value === current));
            });
            select.addEventListener('change', function () {
              saveConfigKey(field.key, select.value);
            });
            return { input: select, autosaves: true };
          }
        }

        if (field.kind === 'boolean' || field.kind === 'role-think') {
          const select = document.createElement('select');
          select.className = 'config-select';
          const emptyLabel = field.kind === 'role-think' ? '(inherit global)' : '(default)';
          [
            { value: '', label: emptyLabel },
            { value: 'true', label: 'true' },
            { value: 'false', label: 'false' },
          ].forEach(function (o) {
            select.appendChild(makeOption(o.value, o.label, o.value === current));
          });
          select.addEventListener('change', function () {
            saveConfigKey(field.key, select.value);
          });
          return { input: select, autosaves: true };
        }

        if (field.kind === 'select') {
          const select = document.createElement('select');
          select.className = 'config-select';
          const opts = field.options || [''];
          if (current && opts.indexOf(current) === -1) {
            select.appendChild(makeOption(current, current + ' (current)', true));
          }
          opts.forEach(function (o) {
            select.appendChild(makeOption(o, o || '(default)', o === current));
          });
          select.addEventListener('change', function () {
            saveConfigKey(field.key, select.value);
          });
          return { input: select, autosaves: true };
        }

        if (field.kind === 'textarea') {
          const ta = document.createElement('textarea');
          ta.className = 'config-textarea';
          ta.value = current;
          return { input: ta, autosaves: false };
        }

        const input = document.createElement('input');
        input.className = 'config-input';
        input.value = current;
        if (field.kind === 'number') input.inputMode = 'decimal';
        return { input: input, autosaves: false };
      }
      function renderConfigField(field) {
        const current = snapshotValue(field.key);
        const row = document.createElement('div');
        row.className = 'config-row';

        const label = document.createElement('div');
        label.className = 'config-label';
        label.textContent = field.label;
        const roleKey = parseRoleConfigKey(field.key);
        if (roleKey && configState && configState.effective && configState.effective.sessionRole === roleKey.role) {
          label.classList.add('active-role');
        }
        row.appendChild(label);

        const valueWrap = document.createElement('div');
        valueWrap.className = 'config-value';
        const built = renderConfigInput(field, current);
        valueWrap.appendChild(built.input);
        const meta = document.createElement('div');
        meta.className = 'config-meta';
        if ((field.kind === 'model' || field.kind === 'role-model') && configState && configState.modelsError) {
          meta.textContent = 'Model list unavailable: ' + configState.modelsError;
        } else if (roleKey && field.kind === 'role-model' && !current) {
          meta.textContent = 'inherits ' + snapshotValue('model');
        } else if (field.hint) {
          meta.textContent = field.hint;
        }
        valueWrap.appendChild(meta);
        row.appendChild(valueWrap);

        const save = document.createElement('button');
        save.className = 'btn config-save clean';
        save.textContent = 'Save';
        if (built.autosaves) {
          save.disabled = true;
        } else {
          const markDirty = function () {
            const dirty = built.input.value !== current;
            save.classList.toggle('clean', !dirty);
            save.disabled = !dirty;
          };
          built.input.addEventListener('input', markDirty);
          built.input.addEventListener('keydown', function (e) {
            if (e.key === 'Enter' && field.kind !== 'textarea') {
              e.preventDefault();
              if (!save.disabled) save.click();
            }
          });
          save.disabled = true;
          save.addEventListener('click', function () {
            saveConfigKey(field.key, built.input.value);
          });
        }
        row.appendChild(save);
        return row;
      }
      function buildConsiderPreview(aspect, tmpl) {
        const name = aspect ? aspect.name : '{name}';
        const traits = aspect ? aspect.traits : '{traits}';
        const body = String(tmpl || '')
          .replace(/\\{name\\}/g, name)
          .replace(/\\{traits\\}/g, traits);
        const suffix = '\\n\\nSteps to perform:\\n\\n'
          + '1. Alignment: How strongly does THIS input activate you, ' + name + '? Default low.\\n'
          + '2. Thought: A single internal reaction from ' + name + "'s angle.\\n"
          + '3. Question (optional): One question ' + name + ' would want held in mind.\\n\\n'
          + '## Response Format\\n\\n'
          + 'Return your response as a single JSON object with alignment, Thought, and optional Question.';
        return { prefix: '## Instructions\\n\\n', body: body, suffix: suffix };
      }
      function renderConfigAspects(sectionEl) {
        const wrap = document.createElement('div');
        wrap.className = 'config-aspects';
        const head = document.createElement('div');
        head.className = 'config-section-title';
        head.textContent = 'Aspects';
        wrap.appendChild(head);

        const aspects = (configState.snapshot && configState.snapshot.considerAspects) || [];
        if (!aspects.length) {
          const empty = document.createElement('div');
          empty.className = 'config-empty';
          empty.textContent = 'No aspects configured';
          wrap.appendChild(empty);
        }
        aspects.forEach(function (a) {
          const row = document.createElement('div');
          row.className = 'aspect-row';
          const enabled = document.createElement('input');
          enabled.type = 'checkbox';
          enabled.checked = !!a.enabled;
          enabled.addEventListener('change', function () {
            setConfigStatus('Saving ' + a.name + '…');
            vscode.postMessage({ type: 'config-aspect-enabled', name: a.name, enabled: enabled.checked });
          });
          row.appendChild(enabled);

          const name = document.createElement('div');
          name.className = 'aspect-name';
          name.textContent = a.name;
          row.appendChild(name);

          const traits = document.createElement('input');
          traits.className = 'config-input';
          traits.value = a.traits || '';
          row.appendChild(traits);

          const save = document.createElement('button');
          save.className = 'btn';
          save.textContent = 'Save';
          save.addEventListener('click', function () {
            setConfigStatus('Saving ' + a.name + '…');
            vscode.postMessage({
              type: 'config-upsert-aspect',
              name: a.name,
              traits: traits.value,
              enabled: enabled.checked,
            });
          });
          row.appendChild(save);

          const del = document.createElement('button');
          del.className = 'btn danger';
          del.textContent = 'Delete';
          del.addEventListener('click', function () {
            setConfigStatus('Deleting ' + a.name + '…');
            vscode.postMessage({ type: 'config-delete-aspect', name: a.name });
          });
          row.appendChild(del);
          wrap.appendChild(row);
        });

        const add = document.createElement('div');
        add.className = 'aspect-new';
        const addName = document.createElement('input');
        addName.className = 'config-input';
        addName.placeholder = 'name';
        const addTraits = document.createElement('input');
        addTraits.className = 'config-input';
        addTraits.placeholder = 'traits';
        const addBtn = document.createElement('button');
        addBtn.className = 'btn';
        addBtn.textContent = 'Add';
        addBtn.addEventListener('click', function () {
          const name = addName.value.trim();
          if (!name) {
            setConfigStatus('Aspect name is required.', true);
            return;
          }
          setConfigStatus('Adding ' + name + '…');
          vscode.postMessage({
            type: 'config-upsert-aspect',
            name: name,
            traits: addTraits.value,
            enabled: true,
          });
        });
        add.appendChild(addName);
        add.appendChild(addTraits);
        add.appendChild(addBtn);
        wrap.appendChild(add);

        const preview = document.createElement('div');
        preview.className = 'config-preview';
        const parts = buildConsiderPreview(aspects[0], snapshotValue('consider.template'));
        const prefix = document.createElement('span');
        prefix.textContent = parts.prefix;
        const body = document.createElement('span');
        body.className = 'body';
        body.textContent = parts.body;
        const suffix = document.createElement('span');
        suffix.textContent = parts.suffix;
        preview.appendChild(prefix);
        preview.appendChild(body);
        preview.appendChild(suffix);
        wrap.appendChild(preview);

        sectionEl.appendChild(wrap);
      }
      function renderConfig() {
        if (!configState || !configState.ok) {
          configRailEl.textContent = '';
          configFieldsEl.innerHTML = '<div class="config-error"></div>';
          configFieldsEl.querySelector('.config-error').textContent =
            configState && configState.error ? configState.error : 'Unable to load Cairo config.';
          configCurrentEl.textContent = 'DB: ' + ((configState && configState.dbPath) || '');
          return;
        }

        const layout = configState.layout || [];
        if (!layout.some(function (s) { return s.title === activeConfigSection; }) && layout.length) {
          activeConfigSection = layout[0].title;
        }

        const effective = configState.effective || {};
        const running = configState.running || runtimeInfo || {};
        const modelText = running.model || effective.model || '(not configured)';
        const configuredModel = effective.model || '(not configured)';
        const urlText = running.ollamaUrl || effective.ollamaUrl || '(not configured)';
        const sourceText = running.ollamaUrlSource || effective.ollamaUrlSource || 'unknown';
        const modelSuffix = configuredModel !== modelText ? ' · configured: ' + configuredModel : '';
        configCurrentEl.textContent =
          'Running model: ' + modelText + modelSuffix + ' · role: '
          + (running.sessionRole || effective.sessionRole || 'default')
          + ' · URL: ' + urlText + ' (' + sourceText + ')';

        configRailEl.textContent = '';
        layout.forEach(function (section) {
          const btn = document.createElement('button');
          btn.className = 'config-tab' + (section.title === activeConfigSection ? ' active' : '');
          btn.textContent = section.title;
          btn.addEventListener('click', function () {
            activeConfigSection = section.title;
            renderConfig();
          });
          configRailEl.appendChild(btn);
        });

        const section = layout.find(function (s) { return s.title === activeConfigSection; }) || layout[0];
        configFieldsEl.textContent = '';
        if (!section) {
          configFieldsEl.innerHTML = '<div class="config-empty">No config sections</div>';
          return;
        }
        const head = document.createElement('div');
        head.className = 'config-section-head';
        const title = document.createElement('div');
        title.className = 'config-section-title';
        title.textContent = section.title;
        const tagline = document.createElement('div');
        tagline.className = 'config-section-tagline';
        tagline.textContent = section.tagline || '';
        head.appendChild(title);
        head.appendChild(tagline);
        configFieldsEl.appendChild(head);

        (section.fields || []).forEach(function (field) {
          configFieldsEl.appendChild(renderConfigField(field));
        });
        if (section.title === 'Consider') {
          renderConfigAspects(configFieldsEl);
        }

        const modelCount = configState.models ? configState.models.length : 0;
        const modelMsg = modelCount ? modelCount + ' models available' : 'No model list loaded';
        setConfigStatus(
          'DB: ' + configState.dbPath + ' · ' + modelMsg
          + (configState.modelsError ? ' · ' + configState.modelsError : ''),
          !!configState.modelsError && !modelCount
        );
      }

      // ---------------------------------------------------------- status
      function shortDuration(ms) {
        const total = Math.max(0, Math.floor((ms || 0) / 1000));
        const h = Math.floor(total / 3600);
        const m = Math.floor((total % 3600) / 60);
        const s = total % 60;
        if (h > 0) return h + 'h ' + m + 'm';
        if (m > 0) return m + 'm ' + String(s).padStart(2, '0') + 's';
        return s + 's';
      }
      function formatTokenCount(n) {
        n = Math.max(0, Math.floor(n || 0));
        if (n >= 1000000) return (n / 1000000).toFixed(1).replace(/\\.0$/, '') + 'm';
        if (n >= 1000) return (n / 1000).toFixed(1).replace(/\\.0$/, '') + 'k';
        return String(n);
      }
      function contextWindowLabel(n) {
        if (!n) return '';
        return Math.max(1, Math.round(n / 1024)) + 'k';
      }
      function updateStatusReadouts() {
        const active = statusState === 'working' || statusState === 'starting';
        if (active) {
          const started = statusStartedAt || Date.now();
          statusElapsedEl.classList.remove('hidden');
          statusElapsedEl.textContent = '· ' + shortDuration(Date.now() - started);
        } else {
          statusElapsedEl.classList.add('hidden');
          statusElapsedEl.textContent = '';
        }

        tokenMeterEl.classList.remove('warn', 'error');
        const currentToks = Math.floor(streamChars / 4);
        if (active && currentToks > 0) {
          let label = 'tok: ' + formatTokenCount(currentToks);
          if (contextLen > 0) {
            label += ' / ' + contextWindowLabel(contextLen) + ' ctx';
            const ratio = currentToks / contextLen;
            if (ratio >= 0.80) tokenMeterEl.classList.add('error');
            else if (ratio >= 0.50) tokenMeterEl.classList.add('warn');
          }
          tokenMeterEl.textContent = '· ' + label;
          tokenMeterEl.classList.remove('hidden');
        } else if (contextLen > 0) {
          tokenMeterEl.textContent = '· ' + contextWindowLabel(contextLen) + ' ctx';
          tokenMeterEl.classList.remove('hidden');
        } else {
          tokenMeterEl.classList.add('hidden');
          tokenMeterEl.textContent = '';
        }
      }
      setInterval(updateStatusReadouts, 1000);
      function setStatus(state, queued, startedAt) {
        statusState = state || 'idle';
        statusDot.className = 'status-dot ' + statusState;
        statusText.textContent = statusState;
        if (statusState === 'working' || statusState === 'starting') {
          statusStartedAt = Number(startedAt || 0) || statusStartedAt || Date.now();
        } else {
          statusStartedAt = 0;
        }
        if (queued && queued > 0) {
          queueBadge.classList.remove('hidden');
          queueBadge.textContent = queued + ' queued';
        } else {
          queueBadge.classList.add('hidden');
        }
        processing = statusState === 'working' || statusState === 'starting';
        sendBtn.disabled = false;
        stopBtn.disabled = !processing;
        updateStatusReadouts();
      }
      function setSession(s) {
        if (!s || s.id == null) {
          sessionInfoEl.textContent = 'Cairo · starting…';
          return;
        }
        const cwd = s.cwd ? ' · ' + s.cwd : '';
        const model = runtimeInfo && runtimeInfo.model
          ? ' · ' + runtimeInfo.model
          : '';
        sessionInfoEl.textContent = 'Cairo · session ' + s.id + ' · ' + (s.role || '') + model + cwd;
      }

      // ---------------------------------------------------------- dispatch
      window.addEventListener('message', function (event) {
        const msg = event.data || {};
        switch (msg.type) {
          case 'text': addText(msg.text, msg.style, msg.run); break;
          case 'tokens': addAssistantTokens(msg.text, msg.run); break;
          case 'thinking': addThinking(msg.text, msg.run); break;
          case 'tool-start': toolStart(msg.run, msg.name, msg.args); break;
          case 'tool-update': toolUpdate(msg.run, msg.name, msg.output); break;
          case 'tool-end': toolEnd(msg.run, msg.name, msg.result, msg.isError, msg.endedAt); break;
          case 'run-start': startNewRun(msg.run); break;
          case 'run-end': runEnd(msg.run, msg.failed, msg.elapsedMs); break;
          case 'user-message': addUserMessage(msg.text); break;
          case 'clear':
            outputEl.textContent = '';
            runContainers.clear();
            toolCards.clear();
            assistantBlock = null;
            assistantSettled = null;
            assistantTail = null;
            assistantSettledLen = 0;
            assistantBuffer = '';
            thinkingBlock = null;
            thinkingBuffer = '';
            streamChars = 0;
            updateStatusReadouts();
            break;
          case 'status':
            runtimeInfo = msg.runtime || null;
            contextLen = runtimeInfo && runtimeInfo.contextLen ? Number(runtimeInfo.contextLen) : 0;
            setStatus(msg.status, msg.queued, msg.runStartedAt);
            setSession(msg.session);
            break;
          case 'sessions': renderSessions(msg.sessions); break;
          case 'file-results':
            if (msg.query === pendingFileQuery && menuMode === 'file' || menuMode === 'none' || menuMode === 'file') {
              showFileMenu(msg.files);
            }
            break;
          case 'attach':
            if (msg.files && msg.display) {
              for (let i = 0; i < msg.files.length; i++) {
                addAttachment(msg.display[i], msg.files[i]);
              }
              messageEl.focus();
            }
            break;
          case 'prefill':
            messageEl.value = msg.text + messageEl.value;
            messageEl.focus();
            break;
          case 'sessions-open':
            sessionsPanel.classList.add('open');
            break;
          case 'config-open':
            configPanel.classList.add('open');
            configBtn.classList.add('active');
            break;
          case 'config-state':
            configState = msg;
            runtimeInfo = msg.running || runtimeInfo;
            contextLen = runtimeInfo && runtimeInfo.contextLen ? Number(runtimeInfo.contextLen) : contextLen;
            updateStatusReadouts();
            renderConfig();
            break;
        }
      });

      messageEl.focus();
      vscode.postMessage({ type: 'ready' });
    })();
  </script>
</body>
</html>`;
}

// ---------------------------------------------------------------------------
// Webview view provider
// ---------------------------------------------------------------------------

class CairoChatViewProvider implements vscode.WebviewViewProvider {
  resolveWebviewView(view: vscode.WebviewView) {
    view.webview.options = { enableScripts: true };
    activeWebviews.add(view.webview);
    view.onDidDispose(() => activeWebviews.delete(view.webview));

    view.webview.html = getWebviewHtml(getNonce());

    view.webview.onDidReceiveMessage(async (msg) => {
      try {
        switch (msg?.type) {
          case 'ready':
            for (const e of eventLog) view.webview.postMessage(e);
            postStatus();
            await ensureCairo();
            return;

          case 'get-config':
            await postConfigState(view.webview, { refreshModels: true });
            return;

          case 'config-refresh-models':
            await postConfigState(view.webview, { refreshModels: true });
            return;

          case 'config-save':
            await saveCairoConfigEntry(String(msg.key || ''), String(msg.value ?? ''));
            await postConfigState(view.webview, {
              refreshModels: String(msg.key || '') === 'ollama_url' || String(msg.key || '') === 'llm_api_key',
            });
            return;

          case 'config-upsert-aspect':
            cachedConfigSnapshot = await upsertCairoConsiderAspect(
              getCairoDbPath(),
              String(msg.name || ''),
              String(msg.traits || ''),
              Boolean(msg.enabled)
            );
            await postConfigState(view.webview);
            return;

          case 'config-delete-aspect':
            cachedConfigSnapshot = await deleteCairoConsiderAspect(
              getCairoDbPath(),
              String(msg.name || '')
            );
            await postConfigState(view.webview);
            return;

          case 'config-aspect-enabled':
            cachedConfigSnapshot = await setCairoConsiderAspectEnabled(
              getCairoDbPath(),
              String(msg.name || ''),
              Boolean(msg.enabled)
            );
            await postConfigState(view.webview);
            return;

          case 'rename-session': {
            const id = Number(msg.id || 0);
            if (!id) return;
            const value = await vscode.window.showInputBox({
              title: 'Rename Cairo Session',
              value: String(msg.name || ''),
              placeHolder: String(msg.fallback || 'Session name'),
              prompt: 'Leave blank to use the generated insight.',
            });
            if (value === undefined) return;
            await renameCairoSession(getCairoDbPath(), id, value, sessionInfo.id || 0);
            await postSessionsState(view.webview);
            return;
          }

          case 'delete-session': {
            const id = Number(msg.id || 0);
            if (!id) return;
            if (id === (sessionInfo.id || 0)) {
              vscode.window.showWarningMessage('Cairo: the active session cannot be deleted while it is open.');
              await postSessionsState(view.webview);
              return;
            }
            const label = String(msg.name || msg.fallback || `Session ${id}`).trim();
            const confirmed = await vscode.window.showWarningMessage(
              `Delete Cairo session ${id}${label ? ` (${label})` : ''}?`,
              { modal: true },
              'Delete'
            );
            if (confirmed !== 'Delete') return;
            await deleteCairoSession(getCairoDbPath(), id, sessionInfo.id || 0);
            await postSessionsState(view.webview);
            return;
          }

          case 'send':
            await handleUserMessage(String(msg.message || ''), Array.isArray(msg.attachments) ? msg.attachments : []);
            return;

          case 'stop':
            await restartCairo();
            emitText('Cairo: interrupted.', 'system');
            return;

          case 'command':
            switch (msg.name) {
              case 'new':
                emitClear();
                await restartCairo({ newSession: true });
                return;
              case 'reload':
                await restartCairo();
                return;
              case 'clear':
                emitClear();
                return;
              case 'config':
                view.webview.postMessage({ type: 'config-open' });
                await postConfigState(view.webview, { refreshModels: true });
                return;
            }
            return;

          case 'list-sessions':
            await postSessionsState(view.webview);
            return;

          case 'open-session':
          case 'resume-session':
            if (typeof msg.id === 'number') {
              emitClear();
              await restartCairo({ sessionId: msg.id });
            }
            return;

          case 'search-files': {
            const files = await searchWorkspaceFiles(String(msg.query || ''));
            view.webview.postMessage({ type: 'file-results', query: String(msg.query || ''), files });
            return;
          }

          case 'attach-by-rel': {
            const rels: string[] = Array.isArray(msg.rels) ? msg.rels : [];
            const abs = resolveAttachmentPaths(rels);
            view.webview.postMessage({ type: 'attach', files: abs, display: rels });
            return;
          }

          case 'attach-by-abs': {
            const paths: string[] = Array.isArray(msg.paths) ? msg.paths : [];
            const display = paths.map((p) => vscode.workspace.asRelativePath(p));
            view.webview.postMessage({ type: 'attach', files: paths, display });
            return;
          }
        }
      } catch (err: any) {
        emitText(`Extension error: ${err?.message || err}`, 'error');
      }
    });

    postStatus();
  }
}

function getNonce(): string {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  let out = '';
  for (let i = 0; i < 32; i++) out += chars[Math.floor(Math.random() * chars.length)];
  return out;
}

// ---------------------------------------------------------------------------
// Activation
// ---------------------------------------------------------------------------

export function activate(context: vscode.ExtensionContext) {
  outputChannel = vscode.window.createOutputChannel('Cairo Agent');
  context.subscriptions.push(outputChannel);

  config = loadConfig();

  const provider = new CairoChatViewProvider();
  context.subscriptions.push(
    vscode.window.registerWebviewViewProvider('cairoChat', provider, {
      webviewOptions: { retainContextWhenHidden: true },
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('cairo.openChat', () =>
      vscode.commands.executeCommand('workbench.view.extension.cairo-sidebar')
    ),
    vscode.commands.registerCommand('cairo.newSession', async () => {
      emitClear();
      await restartCairo({ newSession: true });
    }),
    vscode.commands.registerCommand('cairo.showConfig', async () => {
      await focusCairoView();
      postEphemeralToWebviews({ type: 'config-open' });
      await broadcastConfigState({ refreshModels: true });
    }),
    vscode.commands.registerCommand('cairo.sendSelection', sendActiveSelection),
    vscode.commands.registerCommand('cairo.sendFile', sendActiveFile),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration('cairo-vscode')) {
        config = loadConfig();
        outputChannel.appendLine('[config] reloaded');
      }
    })
  );

  registerOpenFilesPanel(context);
}

export function deactivate() {
  stopCurrentProcess();
  outputChannel?.dispose();
}
