import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const CAIRO_HTTP_URL = process.env.CAIRO_HTTP_URL ?? 'http://localhost:11434';
const CAIRO_HTTP_TOKEN = process.env.CAIRO_HTTP_TOKEN;

function formatTime(unixSeconds: number): string {
  const d = new Date(unixSeconds * 1000);
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  const HH = String(d.getHours()).padStart(2, '0');
  const MM = String(d.getMinutes()).padStart(2, '0');
  return `${yyyy}-${mm}-${dd} ${HH}:${MM}`;
}

async function fetchCairo<T = void>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (CAIRO_HTTP_TOKEN) headers['Authorization'] = `Bearer ${CAIRO_HTTP_TOKEN}`;
  const res = await fetch(`${CAIRO_HTTP_URL}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new Error(`Cairo HTTP ${method} ${path} failed: ${res.status} ${text}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

interface RawSession {
  id: number;
  name: string;
  insight: string;
  role: string;
  cwd: string;
  last_active: number;
}

function normalizeSnapshot(
  raw: {
    config?: Record<string, string>;
    roles?: CairoRole[];
    considerAspects?: CairoConsiderAspect[];
  },
  dbPath: string,
): CairoDbSnapshot {
  return {
    dbPath,
    config: raw.config ?? {},
    roles: raw.roles ?? [],
    considerAspects: raw.considerAspects ?? [],
  };
}

function normalizeSessionsResult(raw: RawSession[], dbPath: string): CairoSessionsResult {
  return {
    dbPath,
    sessions: raw.map((s) => ({
      id: s.id,
      name: s.name ?? '',
      insight: s.insight ?? '',
      role: s.role ?? '',
      cwd: s.cwd ?? '',
      lastActive: formatTime(s.last_active),
    })),
  };
}

export interface CairoRole {
  name: string;
  description: string;
  model: string;
  think: string;
  basePromptKey: string;
  tools: string;
}

export interface CairoConsiderAspect {
  name: string;
  traits: string;
  enabled: boolean;
  position: number;
}

export interface CairoDbSnapshot {
  dbPath: string;
  config: Record<string, string>;
  roles: CairoRole[];
  considerAspects: CairoConsiderAspect[];
}

export interface CairoSessionSummary {
  id: number;
  name: string;
  insight: string;
  role: string;
  cwd: string;
  lastActive: string;
}

export interface CairoSessionsResult {
  dbPath: string;
  sessions: CairoSessionSummary[];
}

export function defaultCairoDataDir(env: NodeJS.ProcessEnv = process.env): string {
  if (env.CAIRO_DATA_DIR) return resolveHome(env.CAIRO_DATA_DIR);
  return path.join(os.homedir(), '.cairo');
}

export function defaultCairoDbPath(env: NodeJS.ProcessEnv = process.env): string {
  return path.join(defaultCairoDataDir(env), 'cairo.db');
}

export function cairoDbExists(dbPath: string): boolean {
  try {
    return fs.statSync(dbPath).isFile();
  } catch {
    return false;
  }
}

export async function readSnapshot(dbPath: string): Promise<CairoDbSnapshot> {
  const raw = await fetchCairo<{
    config?: Record<string, string>;
    roles?: CairoRole[];
    considerAspects?: CairoConsiderAspect[];
  }>('GET', '/api/config/snapshot');
  return normalizeSnapshot(raw, dbPath);
}

export async function listSessions(dbPath: string): Promise<CairoSessionsResult> {
  const raw = await fetchCairo<RawSession[]>('GET', '/api/sessions');
  return normalizeSessionsResult(raw, dbPath);
}

export async function renameSession(dbPath: string, id: number, name: string): Promise<CairoSessionsResult> {
  await fetchCairo('PATCH', `/api/sessions/${id}`, { name });
  const raw = await fetchCairo<RawSession[]>('GET', '/api/sessions');
  return normalizeSessionsResult(raw, dbPath);
}

export async function deleteSession(dbPath: string, id: number): Promise<CairoSessionsResult> {
  await fetchCairo('DELETE', `/api/sessions/${id}`);
  const raw = await fetchCairo<RawSession[]>('GET', '/api/sessions');
  return normalizeSessionsResult(raw, dbPath);
}

export async function setConfig(dbPath: string, key: string, value: string): Promise<CairoDbSnapshot> {
  await fetchCairo('PUT', `/api/config/${key}`, value);
  const raw = await fetchCairo<{
    config?: Record<string, string>;
    roles?: CairoRole[];
    considerAspects?: CairoConsiderAspect[];
  }>('GET', '/api/config/snapshot');
  return normalizeSnapshot(raw, dbPath);
}

export async function setRole(
  dbPath: string,
  name: string,
  field: 'model' | 'think',
  value: string,
): Promise<CairoDbSnapshot> {
  await fetchCairo('PATCH', `/api/roles/${name}`, { field, value });
  const raw = await fetchCairo<{
    config?: Record<string, string>;
    roles?: CairoRole[];
    considerAspects?: CairoConsiderAspect[];
  }>('GET', '/api/config/snapshot');
  return normalizeSnapshot(raw, dbPath);
}

export async function upsertAspect(
  dbPath: string,
  name: string,
  traits: string,
  enabled: boolean,
): Promise<CairoDbSnapshot> {
  await fetchCairo('PUT', `/api/consider/aspects/${name}`, { traits, enabled });
  const raw = await fetchCairo<{
    config?: Record<string, string>;
    roles?: CairoRole[];
    considerAspects?: CairoConsiderAspect[];
  }>('GET', '/api/config/snapshot');
  return normalizeSnapshot(raw, dbPath);
}

export async function deleteAspect(dbPath: string, name: string): Promise<CairoDbSnapshot> {
  await fetchCairo('DELETE', `/api/consider/aspects/${name}`);
  const raw = await fetchCairo<{
    config?: Record<string, string>;
    roles?: CairoRole[];
    considerAspects?: CairoConsiderAspect[];
  }>('GET', '/api/config/snapshot');
  return normalizeSnapshot(raw, dbPath);
}

export async function setAspectEnabled(dbPath: string, name: string, enabled: boolean): Promise<CairoDbSnapshot> {
  await fetchCairo('PATCH', `/api/consider/aspects/${name}`, { enabled });
  const raw = await fetchCairo<{
    config?: Record<string, string>;
    roles?: CairoRole[];
    considerAspects?: CairoConsiderAspect[];
  }>('GET', '/api/config/snapshot');
  return normalizeSnapshot(raw, dbPath);
}

function resolveHome(input: string): string {
  if (input === '~') return os.homedir();
  if (input.startsWith('~/') || input.startsWith(`~${path.sep}`)) {
    return path.join(os.homedir(), input.slice(2));
  }
  return input;
}
