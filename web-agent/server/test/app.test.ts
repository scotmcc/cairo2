import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import type { AgentSession } from '../../shared/protocol.js';
import type { Runner } from '../src/CairoRunner.js';
import type { ServerConfig } from '../src/config.js';
import { createApp } from '../src/createApp.js';
import { SessionStore } from '../src/sessionStore.js';

class MockRunner implements Runner {
  readonly sent: Array<{ sessionId: string; prompt: string }> = [];
  readonly cancelled: string[] = [];

  async sendMessage(session: AgentSession, prompt: string): Promise<void> {
    this.sent.push({ sessionId: session.id, prompt });
  }

  async cancel(session: AgentSession): Promise<void> {
    this.cancelled.push(session.id);
  }
}

describe('web-agent API', () => {
  let tempRoot: string;
  let config: ServerConfig;

  beforeEach(() => {
    tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'cairo-web-agent-'));
    config = {
      authRequired: false,
      host: '127.0.0.1',
      port: 8787,
      workspaceRoots: [tempRoot],
      maxRuntimeSeconds: 3600,
      cairoCliPath: 'cairo',
      packageRoot: tempRoot,
      webRoot: path.join(tempRoot, 'dist', 'web'),
      cairoDbPath: path.join(tempRoot, 'cairo.db'),
      cairoDbExists: false,
    };
  });

  afterEach(() => {
    fs.rmSync(tempRoot, { recursive: true, force: true });
  });

  it('serves health without requiring a runner', async () => {
    const app = createApp({ config, runner: new MockRunner(), store: new SessionStore() });
    const response = await app.inject({ method: 'GET', url: '/api/health' });

    expect(response.statusCode).toBe(200);
    expect(response.json()).toEqual({ ok: true });
  });

  it('rejects workspaces outside the allowed roots', async () => {
    const app = createApp({ config, runner: new MockRunner(), store: new SessionStore() });
    const outside = fs.mkdtempSync(path.join(os.tmpdir(), 'outside-cairo-web-agent-'));

    try {
      const response = await app.inject({
        method: 'POST',
        url: '/api/sessions',
        payload: { workspacePath: outside },
      });

      expect(response.statusCode).toBe(400);
      expect(response.json()).toEqual({ error: 'workspace path is outside CAIRO_WORKSPACE_ROOTS' });
    } finally {
      fs.rmSync(outside, { recursive: true, force: true });
    }
  });

  it('creates a session for an allowed workspace', async () => {
    const app = createApp({ config, runner: new MockRunner(), store: new SessionStore() });
    const response = await app.inject({
      method: 'POST',
      url: '/api/sessions',
      payload: { workspacePath: tempRoot },
    });

    expect(response.statusCode).toBe(201);
    const session = response.json<AgentSession>();
    expect(session.workspacePath).toBe(fs.realpathSync.native(tempRoot));
    expect(session.status).toBe('idle');
    expect(session.messages).toEqual([]);
    expect(session.events[0]?.type).toBe('status');
  });

  it('reports server and session status', async () => {
    const app = createApp({ config, runner: new MockRunner(), store: new SessionStore() });
    await app.inject({
      method: 'POST',
      url: '/api/sessions',
      payload: { workspacePath: tempRoot },
    });

    const response = await app.inject({ method: 'GET', url: '/api/status' });

    expect(response.statusCode).toBe(200);
    expect(response.json()).toMatchObject({
      ok: true,
      counts: {
        totalSessions: 1,
        runningSessions: 0,
        idleSessions: 1,
        errorSessions: 0,
      },
    });
  });

  it('queues messages asynchronously and exposes the message list', async () => {
    const runner = new MockRunner();
    const app = createApp({ config, runner, store: new SessionStore() });
    const create = await app.inject({
      method: 'POST',
      url: '/api/sessions',
      payload: { workspacePath: tempRoot },
    });
    const session = create.json<AgentSession>();

    const send = await app.inject({
      method: 'POST',
      url: `/api/sessions/${session.id}/messages`,
      payload: { content: 'hello cairo' },
    });
    const messages = await app.inject({ method: 'GET', url: `/api/sessions/${session.id}/messages` });

    expect(send.statusCode).toBe(202);
    expect(runner.sent).toEqual([{ sessionId: session.id, prompt: 'hello cairo' }]);
    expect(messages.json()).toMatchObject({
      messages: [{ role: 'user', content: 'hello cairo' }],
    });
    expect(send.json<AgentSession>().events.some((event) => event.type === 'message')).toBe(true);
  });

  it('supports abort as an alias for cancel', async () => {
    const runner = new MockRunner();
    const app = createApp({ config, runner, store: new SessionStore() });
    const create = await app.inject({
      method: 'POST',
      url: '/api/sessions',
      payload: { workspacePath: tempRoot },
    });
    const session = create.json<AgentSession>();

    const response = await app.inject({ method: 'POST', url: `/api/sessions/${session.id}/abort` });

    expect(response.statusCode).toBe(200);
    expect(runner.cancelled).toEqual([session.id]);
  });

  it('rejects unauthorized API requests when CAIRO_WEB_TOKEN is configured', async () => {
    const app = createApp({
      config: { ...config, authRequired: true, token: 'secret' },
      runner: new MockRunner(),
      store: new SessionStore(),
    });

    const unauthorized = await app.inject({ method: 'GET', url: '/api/sessions' });
    expect(unauthorized.statusCode).toBe(401);

    const authorized = await app.inject({
      method: 'GET',
      url: '/api/sessions',
      headers: { authorization: 'Bearer secret' },
    });
    expect(authorized.statusCode).toBe(200);
  });
});
