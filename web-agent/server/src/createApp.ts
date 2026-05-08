import fastifyStatic from '@fastify/static';
import Fastify, { type FastifyInstance, type FastifyReply, type FastifyRequest } from 'fastify';
import fs from 'node:fs';
import path from 'node:path';
import type { AgentSession, CreateSessionRequest, SendMessageRequest, ServerStatus } from '../../shared/protocol.js';
import * as cairoDb from './cairoDb.js';
import { CairoRunner, type Runner } from './CairoRunner.js';
import { publicConfig, type ServerConfig } from './config.js';
import { fetchOllamaModels } from './models.js';
import { SessionStore } from './sessionStore.js';
import { listWorkspaces, validateWorkspacePath, WorkspaceValidationError } from './workspaces.js';

interface CreateAppOptions {
  config: ServerConfig;
  store?: SessionStore;
  runner?: Runner;
}

export function createApp(options: CreateAppOptions) {
  const store = options.store || new SessionStore();
  const runner = options.runner || new CairoRunner(store, options.config);
  const app = Fastify({ logger: false });
  const startedAt = Date.now();

  app.decorate('store', store);

  app.addHook('preHandler', async (request, reply) => {
    if (request.url === '/api/health') return;
    if (!request.url.startsWith('/api/')) return;
    if (!isAuthorized(request, options.config)) {
      await reply.code(401).send({ error: 'unauthorized' });
    }
  });

  app.get('/api/health', async () => ({ ok: true }));

  app.get('/api/status', async () => buildStatus(store, startedAt));

  app.get('/api/config', async (_request, reply) => {
    setAuthCookie(reply, options.config);
    return publicConfig(options.config);
  });

  app.get('/api/workspaces', async () => ({ workspaces: listWorkspaces(options.config.workspaceRoots) }));

  app.get('/api/sessions', async () => ({ sessions: store.list() }));

  app.post<{ Body: CreateSessionRequest }>('/api/sessions', async (request, reply) => {
    try {
      const workspacePath = validateWorkspacePath(request.body?.workspacePath, options.config.workspaceRoots);
      const session = store.create(workspacePath);
      return reply.code(201).send(session);
    } catch (err) {
      return sendError(reply, err);
    }
  });

  app.get<{ Params: { id: string } }>('/api/sessions/:id', async (request, reply) => {
    const session = store.get(request.params.id);
    if (!session) return reply.code(404).send({ error: 'session not found' });
    return session;
  });

  app.get<{ Params: { id: string } }>('/api/sessions/:id/messages', async (request, reply) => {
    const session = store.get(request.params.id);
    if (!session) return reply.code(404).send({ error: 'session not found' });
    return { messages: session.messages };
  });

  app.delete<{ Params: { id: string } }>('/api/sessions/:id', async (request, reply) => {
    const session = store.get(request.params.id);
    if (!session) return reply.code(404).send({ error: 'session not found' });
    try {
      await runner.cancel(session);
    } catch {
      // best-effort cleanup; the store remove below is the source of truth
    }
    store.remove(session.id);
    return reply.code(204).send();
  });

  app.post<{ Params: { id: string }; Body: SendMessageRequest }>(
    '/api/sessions/:id/messages',
    async (request, reply) => {
      const session = store.get(request.params.id);
      if (!session) return reply.code(404).send({ error: 'session not found' });

      const content = request.body?.content?.trim();
      if (!content) return reply.code(400).send({ error: 'content is required' });

      store.addMessage(session.id, 'user', content);
      try {
        await runner.sendMessage(session, content);
      } catch (err) {
        return sendError(reply, err);
      }
      return reply.code(202).send(store.get(session.id));
    },
  );

  app.get<{ Params: { id: string } }>('/api/sessions/:id/events', async (request, reply) => {
    const session = store.get(request.params.id);
    if (!session) return reply.code(404).send({ error: 'session not found' });

    reply.hijack();
    reply.raw.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache, no-transform',
      Connection: 'keep-alive',
      'X-Accel-Buffering': 'no',
    });

    for (const event of session.events) {
      writeSse(reply, event);
    }

    const unsubscribe = store.subscribe(session.id, (event) => writeSse(reply, event));
    const keepAlive = setInterval(() => {
      if (!reply.raw.destroyed && !reply.raw.writableEnded) reply.raw.write(': keepalive\n\n');
    }, 15000);
    keepAlive.unref();
    request.raw.on('close', () => {
      clearInterval(keepAlive);
      unsubscribe();
    });
  });

  app.post<{ Params: { id: string } }>('/api/sessions/:id/cancel', async (request, reply) => {
    const session = store.get(request.params.id);
    if (!session) return reply.code(404).send({ error: 'session not found' });
    await runner.cancel(session);
    return store.get(session.id);
  });

  app.post<{ Params: { id: string } }>('/api/sessions/:id/abort', async (request, reply) => {
    const session = store.get(request.params.id);
    if (!session) return reply.code(404).send({ error: 'session not found' });
    await runner.cancel(session);
    return store.get(session.id);
  });

  app.get('/api/cairo/snapshot', async (_request, reply) => {
    if (!cairoDb.cairoDbExists(options.config.cairoDbPath)) {
      return reply.code(404).send({ error: `Cairo database not found at ${options.config.cairoDbPath}` });
    }
    try {
      return await cairoDb.readSnapshot(options.config.cairoDbPath);
    } catch (err) {
      return sendError(reply, err);
    }
  });

  app.post<{ Body: { key?: string; value?: string } }>('/api/cairo/config', async (request, reply) => {
    const key = request.body?.key?.trim();
    if (!key) return reply.code(400).send({ error: 'key is required' });
    try {
      return await cairoDb.setConfig(options.config.cairoDbPath, key, String(request.body?.value ?? ''));
    } catch (err) {
      return sendError(reply, err);
    }
  });

  app.post<{ Body: { name?: string; field?: 'model' | 'think'; value?: string } }>(
    '/api/cairo/role',
    async (request, reply) => {
      const name = request.body?.name?.trim();
      const field = request.body?.field;
      if (!name) return reply.code(400).send({ error: 'name is required' });
      if (field !== 'model' && field !== 'think') {
        return reply.code(400).send({ error: 'field must be model or think' });
      }
      try {
        return await cairoDb.setRole(options.config.cairoDbPath, name, field, String(request.body?.value ?? ''));
      } catch (err) {
        return sendError(reply, err);
      }
    },
  );

  app.post<{ Body: { name?: string; traits?: string; enabled?: boolean } }>(
    '/api/cairo/aspect',
    async (request, reply) => {
      const name = request.body?.name?.trim();
      if (!name) return reply.code(400).send({ error: 'name is required' });
      try {
        return await cairoDb.upsertAspect(
          options.config.cairoDbPath,
          name,
          String(request.body?.traits ?? ''),
          Boolean(request.body?.enabled),
        );
      } catch (err) {
        return sendError(reply, err);
      }
    },
  );

  app.delete<{ Params: { name: string } }>('/api/cairo/aspect/:name', async (request, reply) => {
    try {
      return await cairoDb.deleteAspect(options.config.cairoDbPath, request.params.name);
    } catch (err) {
      return sendError(reply, err);
    }
  });

  app.post<{ Params: { name: string }; Body: { enabled?: boolean } }>(
    '/api/cairo/aspect/:name/enabled',
    async (request, reply) => {
      try {
        return await cairoDb.setAspectEnabled(
          options.config.cairoDbPath,
          request.params.name,
          Boolean(request.body?.enabled),
        );
      } catch (err) {
        return sendError(reply, err);
      }
    },
  );

  app.get('/api/cairo/sessions', async (_request, reply) => {
    if (!cairoDb.cairoDbExists(options.config.cairoDbPath)) {
      return reply.code(404).send({ error: `Cairo database not found at ${options.config.cairoDbPath}` });
    }
    try {
      return await cairoDb.listSessions(options.config.cairoDbPath);
    } catch (err) {
      return sendError(reply, err);
    }
  });

  app.post<{ Params: { id: string }; Body: { name?: string } }>(
    '/api/cairo/sessions/:id/rename',
    async (request, reply) => {
      const id = Number(request.params.id);
      if (!Number.isFinite(id) || id <= 0) return reply.code(400).send({ error: 'invalid session id' });
      try {
        return await cairoDb.renameSession(options.config.cairoDbPath, id, String(request.body?.name ?? ''));
      } catch (err) {
        return sendError(reply, err);
      }
    },
  );

  app.delete<{ Params: { id: string } }>('/api/cairo/sessions/:id', async (request, reply) => {
    const id = Number(request.params.id);
    if (!Number.isFinite(id) || id <= 0) return reply.code(400).send({ error: 'invalid session id' });
    try {
      return await cairoDb.deleteSession(options.config.cairoDbPath, id);
    } catch (err) {
      return sendError(reply, err);
    }
  });

  app.get('/api/cairo/models', async (_request, reply) => {
    let baseUrl = '';
    if (cairoDb.cairoDbExists(options.config.cairoDbPath)) {
      try {
        const snapshot = await cairoDb.readSnapshot(options.config.cairoDbPath);
        baseUrl = snapshot.config?.ollama_url || '';
      } catch {
        // fall through to env-based default
      }
    }
    if (!baseUrl) baseUrl = process.env.OLLAMA_HOST || 'http://localhost:11434';
    const result = await fetchOllamaModels(baseUrl);
    return reply.send(result);
  });

  registerStatic(app, options.config.webRoot);

  return app;
}

declare module 'fastify' {
  interface FastifyInstance {
    store: SessionStore;
  }
}

function isAuthorized(request: FastifyRequest, config: ServerConfig): boolean {
  if (!config.token) return true;
  const auth = request.headers.authorization || '';
  if (auth === `Bearer ${config.token}`) return true;
  return parseCookie(request.headers.cookie || '').cairo_web_token === config.token;
}

function setAuthCookie(reply: FastifyReply, config: ServerConfig): void {
  if (!config.token) return;
  reply.header('Set-Cookie', `cairo_web_token=${encodeURIComponent(config.token)}; HttpOnly; SameSite=Lax; Path=/`);
}

function parseCookie(header: string): Record<string, string> {
  const cookies: Record<string, string> = {};
  for (const part of header.split(';')) {
    const [rawKey, ...rawValue] = part.trim().split('=');
    if (!rawKey) continue;
    cookies[rawKey] = decodeURIComponent(rawValue.join('='));
  }
  return cookies;
}

function sendError(reply: FastifyReply, err: unknown) {
  if (err instanceof WorkspaceValidationError) {
    return reply.code(400).send({ error: err.message });
  }
  const message = err instanceof Error ? err.message : 'request failed';
  const status = message.includes('already running') ? 409 : 500;
  return reply.code(status).send({ error: message });
}

function writeSse(reply: FastifyReply, event: AgentSession['events'][number]): void {
  if (reply.raw.destroyed || reply.raw.writableEnded) return;
  try {
    reply.raw.write(`id: ${event.id}\n`);
    reply.raw.write(`event: ${event.type}\n`);
    reply.raw.write(`data: ${JSON.stringify(event)}\n\n`);
  } catch {
    // The close handler removes the listener; this avoids surfacing socket races.
  }
}

function buildStatus(store: SessionStore, startedAt: number): ServerStatus {
  const sessions = store.list();
  return {
    ok: true,
    uptimeSeconds: Math.floor((Date.now() - startedAt) / 1000),
    sessions: sessions.map((session) => ({
      id: session.id,
      status: session.status,
      workspacePath: session.workspacePath,
      updatedAt: session.updatedAt,
      cairoSessionId: session.cairoSessionId,
      cairoRole: session.cairoRole,
      cairoCwd: session.cairoCwd,
    })),
    counts: {
      totalSessions: sessions.length,
      runningSessions: sessions.filter((session) => session.status === 'running').length,
      idleSessions: sessions.filter((session) => session.status === 'idle').length,
      errorSessions: sessions.filter((session) => session.status === 'error').length,
    },
  };
}

function registerStatic(app: FastifyInstance, webRoot: string): void {
  if (fs.existsSync(path.join(webRoot, 'index.html'))) {
    app.register(fastifyStatic, {
      root: webRoot,
      prefix: '/',
      decorateReply: true,
    });
    app.setNotFoundHandler((request, reply) => {
      if (request.url.startsWith('/api/')) return reply.code(404).send({ error: 'not found' });
      return reply.sendFile('index.html');
    });
    return;
  }

  app.get('/', async (_request, reply) =>
    reply.type('text/html').send('<!doctype html><title>Cairo Web</title><p>Run npm run build first.</p>'),
  );
}
