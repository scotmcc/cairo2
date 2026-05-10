import http from 'node:http';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type MockRoute =
  | Record<string, unknown>
  | ((req: http.IncomingMessage, res: http.ServerResponse) => void);

async function startMockCairoServer(routes: Record<string, MockRoute>) {
  const server = http.createServer((req, res) => {
    const key = `${req.method} ${req.url}`;
    const handler = routes[key];
    if (!handler) {
      res.writeHead(404);
      res.end();
      return;
    }
    if (typeof handler === 'function') {
      handler(req, res);
      return;
    }
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(handler));
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const addr = server.address() as { port: number };
  return {
    url: `http://127.0.0.1:${addr.port}`,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}

describe('cairoDb HTTP adapter', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    delete process.env.CAIRO_HTTP_URL;
  });

  it('readSnapshot returns CairoDbSnapshot with dbPath injected', async () => {
    const server = await startMockCairoServer({
      'GET /api/config/snapshot': {
        config: { llm_model: 'llama3' },
        roles: [{ name: 'coder', description: '', model: 'llama3', think: '', basePromptKey: '', tools: '' }],
        considerAspects: [],
      },
    });
    process.env.CAIRO_HTTP_URL = server.url;
    const cairoDb = await import('../src/cairoDb.js');
    try {
      const result = await cairoDb.readSnapshot('/fake/cairo.db');
      expect(result.dbPath).toBe('/fake/cairo.db');
      expect(result.config.llm_model).toBe('llama3');
      expect(result.roles[0].name).toBe('coder');
      expect(result.considerAspects).toEqual([]);
    } finally {
      await server.close();
    }
  });

  it('listSessions normalizes bare array and formats lastActive as YYYY-MM-DD HH:MM', async () => {
    const server = await startMockCairoServer({
      'GET /api/sessions': [
        { id: 1, name: 'work', insight: 'Fix bug', role: 'coder', cwd: '/home', last_active: 1715000000 },
      ],
    });
    process.env.CAIRO_HTTP_URL = server.url;
    const cairoDb = await import('../src/cairoDb.js');
    try {
      const result = await cairoDb.listSessions('/fake/cairo.db');
      expect(result.dbPath).toBe('/fake/cairo.db');
      expect(result.sessions).toHaveLength(1);
      expect(result.sessions[0].id).toBe(1);
      expect(result.sessions[0].name).toBe('work');
      expect(result.sessions[0].lastActive).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/);
    } finally {
      await server.close();
    }
  });

  it('renameSession issues PATCH then re-fetches sessions list', async () => {
    let patchCalled = false;
    const server = await startMockCairoServer({
      'PATCH /api/sessions/42': (req, res) => {
        patchCalled = true;
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end('{}');
      },
      'GET /api/sessions': [
        { id: 42, name: 'renamed', insight: '', role: '', cwd: '', last_active: 0 },
      ],
    });
    process.env.CAIRO_HTTP_URL = server.url;
    const cairoDb = await import('../src/cairoDb.js');
    try {
      const result = await cairoDb.renameSession('/fake/cairo.db', 42, 'renamed');
      expect(patchCalled).toBe(true);
      expect(result.sessions).toHaveLength(1);
      expect(result.sessions[0].name).toBe('renamed');
      expect(result.dbPath).toBe('/fake/cairo.db');
    } finally {
      await server.close();
    }
  });
});
