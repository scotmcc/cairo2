import type {
  AgentSession,
  CairoModelsResponse,
  CairoSessionSummary,
  CairoSnapshot,
  CreateSessionRequest,
  PublicConfig,
  SendMessageRequest,
  ServerStatus,
  SessionMessage,
  WorkspaceSummary,
} from '../../shared/protocol.js';

const TOKEN_KEY = 'cairo-web-token';

export function getStoredToken(): string {
  return localStorage.getItem(TOKEN_KEY) || '';
}

export function storeToken(token: string): void {
  if (token) localStorage.setItem(TOKEN_KEY, token);
  else localStorage.removeItem(TOKEN_KEY);
}

async function api<T>(path: string, init: RequestInit = {}, token = getStoredToken()): Promise<T> {
  const headers = new Headers(init.headers);
  if (!headers.has('Content-Type') && init.body) headers.set('Content-Type', 'application/json');
  if (token) headers.set('Authorization', `Bearer ${token}`);

  const response = await fetch(path, {
    ...init,
    headers,
    credentials: 'same-origin',
  });

  if (response.status === 204) return undefined as T;
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // Keep status text.
    }
    throw new Error(message);
  }

  return (await response.json()) as T;
}

export function getConfig(token?: string) {
  return api<PublicConfig>('/api/config', {}, token);
}

export function getWorkspaces() {
  return api<{ workspaces: WorkspaceSummary[] }>('/api/workspaces');
}

export function getSessions() {
  return api<{ sessions: AgentSession[] }>('/api/sessions');
}

export function getStatus() {
  return api<ServerStatus>('/api/status');
}

export function createSession(payload: CreateSessionRequest) {
  return api<AgentSession>('/api/sessions', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export function getSession(id: string) {
  return api<AgentSession>(`/api/sessions/${id}`);
}

export function getSessionMessages(id: string) {
  return api<{ messages: SessionMessage[] }>(`/api/sessions/${id}/messages`);
}

export function deleteWebSession(id: string) {
  return api<void>(`/api/sessions/${id}`, { method: 'DELETE' });
}

export function sendMessage(id: string, payload: SendMessageRequest) {
  return api<AgentSession>(`/api/sessions/${id}/messages`, {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export function cancelSession(id: string) {
  return api<AgentSession>(`/api/sessions/${id}/cancel`, { method: 'POST' });
}

export function abortSession(id: string) {
  return api<AgentSession>(`/api/sessions/${id}/abort`, { method: 'POST' });
}

export function getCairoSnapshot() {
  return api<CairoSnapshot>('/api/cairo/snapshot');
}

export function setCairoConfig(key: string, value: string) {
  return api<CairoSnapshot>('/api/cairo/config', {
    method: 'POST',
    body: JSON.stringify({ key, value }),
  });
}

export function setCairoRole(name: string, field: 'model' | 'think', value: string) {
  return api<CairoSnapshot>('/api/cairo/role', {
    method: 'POST',
    body: JSON.stringify({ name, field, value }),
  });
}

export function upsertCairoAspect(name: string, traits: string, enabled: boolean) {
  return api<CairoSnapshot>('/api/cairo/aspect', {
    method: 'POST',
    body: JSON.stringify({ name, traits, enabled }),
  });
}

export function deleteCairoAspect(name: string) {
  return api<CairoSnapshot>(`/api/cairo/aspect/${encodeURIComponent(name)}`, { method: 'DELETE' });
}

export function setCairoAspectEnabled(name: string, enabled: boolean) {
  return api<CairoSnapshot>(`/api/cairo/aspect/${encodeURIComponent(name)}/enabled`, {
    method: 'POST',
    body: JSON.stringify({ enabled }),
  });
}

export function getCairoSessions() {
  return api<{ dbPath: string; sessions: CairoSessionSummary[] }>('/api/cairo/sessions');
}

export function renameCairoDbSession(id: number, name: string) {
  return api<{ dbPath: string; sessions: CairoSessionSummary[] }>(`/api/cairo/sessions/${id}/rename`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  });
}

export function deleteCairoDbSession(id: number) {
  return api<{ dbPath: string; sessions: CairoSessionSummary[] }>(`/api/cairo/sessions/${id}`, {
    method: 'DELETE',
  });
}

export function getCairoModels() {
  return api<CairoModelsResponse>('/api/cairo/models');
}
