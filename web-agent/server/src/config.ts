import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { cairoDbExists, defaultCairoDbPath } from './cairoDb.js';
import type { PublicConfig } from '../../shared/protocol.js';

export interface ServerConfig extends PublicConfig {
  token?: string;
  cairoCliPath: string;
  packageRoot: string;
  webRoot: string;
}

export function readConfig(env: NodeJS.ProcessEnv = process.env, cwd = process.cwd()): ServerConfig {
  const packageRoot = resolvePackageRoot(cwd);
  const repoRoot = path.dirname(packageRoot);
  const defaultWorkspaceRoot = path.basename(packageRoot) === 'web-agent' ? repoRoot : cwd;

  const host = env.CAIRO_WEB_HOST || '127.0.0.1';
  const port = readPositiveInt(env.CAIRO_WEB_PORT, 8787, 'CAIRO_WEB_PORT');
  const maxRuntimeSeconds = readPositiveInt(
    env.CAIRO_WEB_MAX_RUNTIME_SECONDS,
    3600,
    'CAIRO_WEB_MAX_RUNTIME_SECONDS',
  );
  const workspaceRoots = (env.CAIRO_WORKSPACE_ROOTS
    ? env.CAIRO_WORKSPACE_ROOTS.split(path.delimiter)
    : [defaultWorkspaceRoot]
  )
    .map((p) => resolveHome(p.trim()))
    .filter(Boolean)
    .map((p) => path.resolve(p));

  const cairoDbPath = env.CAIRO_DB_PATH ? resolveHome(env.CAIRO_DB_PATH) : defaultCairoDbPath(env);

  return {
    authRequired: Boolean(env.CAIRO_WEB_TOKEN),
    token: env.CAIRO_WEB_TOKEN,
    host,
    port,
    workspaceRoots,
    maxRuntimeSeconds,
    cairoCliPath: env.CAIRO_CLI_PATH || 'cairo',
    packageRoot,
    webRoot: path.join(packageRoot, 'dist', 'web'),
    cairoDbPath,
    cairoDbExists: cairoDbExists(cairoDbPath),
  };
}

export function publicConfig(config: ServerConfig): PublicConfig {
  return {
    authRequired: config.authRequired,
    host: config.host,
    port: config.port,
    workspaceRoots: config.workspaceRoots,
    maxRuntimeSeconds: config.maxRuntimeSeconds,
    cairoDbPath: config.cairoDbPath,
    cairoDbExists: cairoDbExists(config.cairoDbPath),
  };
}

function readPositiveInt(value: string | undefined, fallback: number, name: string): number {
  if (!value) return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error(`${name} must be a positive integer`);
  }
  return parsed;
}

function resolveHome(input: string): string {
  if (input === '~') return os.homedir();
  if (input.startsWith('~/') || input.startsWith(`~${path.sep}`)) {
    return path.join(os.homedir(), input.slice(2));
  }
  return input;
}

function resolvePackageRoot(cwd: string): string {
  const direct = path.join(cwd, 'package.json');
  if (path.basename(cwd) === 'web-agent' && fs.existsSync(direct)) return cwd;

  const child = path.join(cwd, 'web-agent', 'package.json');
  if (fs.existsSync(child)) return path.join(cwd, 'web-agent');

  return cwd;
}
