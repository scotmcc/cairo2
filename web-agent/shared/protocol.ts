export type SessionStatus = 'idle' | 'running' | 'cancelled' | 'error' | 'complete';

export type MessageRole = 'user' | 'assistant' | 'system' | 'tool';

export type EventType = 'stdout' | 'stderr' | 'token' | 'status' | 'error' | 'done' | 'tool' | 'message';

export type JsonValue =
  | string
  | number
  | boolean
  | null
  | JsonValue[]
  | { [key: string]: JsonValue };

export interface SessionMessage {
  id: string;
  role: MessageRole;
  content: string;
  createdAt: string;
}

export interface SessionEvent {
  id: string;
  type: EventType;
  data: JsonValue;
  createdAt: string;
}

export interface AgentSession {
  id: string;
  createdAt: string;
  updatedAt: string;
  workspacePath: string;
  status: SessionStatus;
  messages: SessionMessage[];
  events: SessionEvent[];
  cairoSessionId?: number;
  cairoRole?: string;
  cairoCwd?: string;
  title?: string;
}

export interface ServerStatus {
  ok: true;
  uptimeSeconds: number;
  sessions: Array<{
    id: string;
    status: SessionStatus;
    workspacePath: string;
    updatedAt: string;
    cairoSessionId?: number;
    cairoRole?: string;
    cairoCwd?: string;
  }>;
  counts: {
    totalSessions: number;
    runningSessions: number;
    idleSessions: number;
    errorSessions: number;
  };
}

export interface WorkspaceSummary {
  path: string;
  name: string;
}

export interface PublicConfig {
  authRequired: boolean;
  host: string;
  port: number;
  workspaceRoots: string[];
  maxRuntimeSeconds: number;
  cairoDbPath: string;
  cairoDbExists: boolean;
}

export interface CreateSessionRequest {
  workspacePath?: string;
}

export interface SendMessageRequest {
  content: string;
}

export interface ApiError {
  error: string;
}

export interface CairoRoleSummary {
  name: string;
  description: string;
  model: string;
  think: string;
  basePromptKey: string;
  tools: string;
}

export interface CairoConsiderAspectSummary {
  name: string;
  traits: string;
  enabled: boolean;
  position: number;
}

export interface CairoSnapshot {
  dbPath: string;
  config: Record<string, string>;
  roles: CairoRoleSummary[];
  considerAspects: CairoConsiderAspectSummary[];
}

export interface CairoSessionSummary {
  id: number;
  name: string;
  insight: string;
  role: string;
  cwd: string;
  lastActive: string;
}

export interface CairoModelsResponse {
  models: string[];
  source: string;
  error?: string;
}
