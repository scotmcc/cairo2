# Cairo Web Agent

Browser UI and Node/TypeScript wrapper for driving the local Cairo CLI from another machine on your LAN or Tailscale network.

This is powerful: anyone who can reach this service can send prompts to a local coding agent that can modify files and run commands through Cairo's normal tool system. Bind to localhost by default, use `CAIRO_WEB_TOKEN` when exposing it, and prefer a Tailscale-only or nginx-protected listener.

## Run

```bash
cd web-agent
npm install
CAIRO_CLI_PATH=/path/to/cairo CAIRO_WORKSPACE_ROOTS=/home/sgreen npm run dev
```

Open:

```text
http://localhost:8787
```

LAN/Tailscale example after building:

```bash
cd web-agent
npm run build
CAIRO_WEB_HOST=0.0.0.0 CAIRO_WEB_PORT=8787 CAIRO_WEB_TOKEN=changeme npm run start
```

## Scripts

```bash
npm run dev        # build once, then run the server
npm run build      # build React UI and TypeScript backend
npm run start      # run the built backend
npm run typecheck  # TypeScript project check
npm test           # backend tests
```

## Configuration

| Env var | Default | Purpose |
|---|---:|---|
| `CAIRO_WEB_HOST` | `127.0.0.1` | Bind address. Set `0.0.0.0` only when intentionally exposing the service. |
| `CAIRO_WEB_PORT` | `8787` | HTTP port. |
| `CAIRO_WEB_TOKEN` | unset | Optional bearer token for API calls and UI bootstrap. |
| `CAIRO_CLI_PATH` | `cairo` | Cairo executable to spawn. |
| `CAIRO_WORKSPACE_ROOTS` | repo root/current cwd | Colon-separated roots that browser-selected workspaces must live under. |
| `CAIRO_WEB_MAX_RUNTIME_SECONDS` | `3600` | Per-message runtime timeout before the Cairo process is killed. |

## API

```text
GET  /api/health
GET  /api/status
GET  /api/config
GET  /api/workspaces
GET  /api/sessions
POST /api/sessions
GET  /api/sessions/:id
GET  /api/sessions/:id/messages
POST /api/sessions/:id/messages
GET  /api/sessions/:id/events
POST /api/sessions/:id/cancel
POST /api/sessions/:id/abort
```

When `CAIRO_WEB_TOKEN` is set, send:

```http
Authorization: Bearer <token>
```

The browser stores the token locally and sends it to `/api/config`; the server also sets an HttpOnly same-origin cookie so native `EventSource` can subscribe to the SSE stream.

## Cairo invocation

Each web session owns a controlled Cairo child process. The backend spawns:

```bash
cairo -vscode -new
```

or resumes with:

```bash
cairo -vscode -session <id>
```

The process runs with the selected workspace as `cwd`. Single-line browser prompts are written to Cairo's stdin the same way the VS Code extension does. Multiline prompts, or prompts that begin with `{`, are written as JSON input lines so they survive the line-oriented stdin bridge. Stdout JSONL events are translated into web session events, and stderr is streamed into the live log panel. Cancellation kills the whole process group where the platform supports it.

The v1 runner assumes Cairo's `-vscode` mode is available. This repository accepts both raw stdin lines and JSON input lines in that mode; older installed binaries may only support raw single-line prompts.

## Safety boundaries

- No shell endpoint is exposed.
- The browser cannot choose the executable or command arguments.
- Workspace paths are rejected unless they are inside `CAIRO_WORKSPACE_ROOTS`.
- Bearer tokens are not logged by the web service.
- HTTPS, OAuth, nginx, Docker, and persistent session storage are intentionally out of scope for v1.

## Later

- Diff viewer.
- Git branch selector.
- Approve/deny command prompts.
- Test runner button.
- Open in VS Code link.
- Persistent sessions.
- WebSocket/node-pty terminal tab.
- Tailscale/nginx HTTPS hardening.
