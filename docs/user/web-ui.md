# Web UI

Cairo includes a browser-based interface for chatting, managing sessions, and editing config — useful when you're working remotely or prefer a web panel alongside the terminal.

---

## What the web UI provides

- A chat interface connected to a live cairo process
- Session list and the ability to create, rename, and delete sessions
- A config panel for browsing and changing settings
- A snapshot view of the cairo database (sessions, messages, memories)
- WebSocket-based streaming so responses appear as they're generated

The web UI does not replace the TUI or line CLI — it runs alongside them. The same database and identity are shared.

---

## Starting the web server

The quickest way:

```bash
bash scripts/cairo-web.sh
```

This script locates the web-agent package, verifies the built runtime is present, and starts the Node.js server. By default it listens on `127.0.0.1:8787`.

Open your browser to: `http://127.0.0.1:8787`

---

## Systemd service (persistent)

If you installed cairo via a package or ran `scripts/install-web-agent.sh`, a systemd user unit is available:

```bash
# Start once
systemctl --user start cairo-web

# Start automatically on login
systemctl --user enable cairo-web

# Check status
systemctl --user status cairo-web
```

---

## Configuration

All options are environment variables passed to the server process.

| Variable | Default | Effect |
|---|---|---|
| `CAIRO_WEB_HOST` | `127.0.0.1` | Bind address. Set to `0.0.0.0` to listen on all interfaces (see Security below). |
| `CAIRO_WEB_PORT` | `8787` | TCP port. |
| `CAIRO_WEB_TOKEN` | *(empty)* | Bearer token required on all requests. If empty, no authentication is enforced. |
| `CAIRO_CLI_PATH` | `cairo` | Path to the cairo binary the web server spawns. |
| `CAIRO_DB_PATH` | `~/.cairo/cairo.db` | Path to cairo.db. |
| `CAIRO_WORKSPACE_ROOTS` | *(cwd)* | Colon-separated list of workspace root directories the UI can use for new sessions. |
| `CAIRO_WEB_MAX_RUNTIME_SECONDS` | `3600` | Max seconds a single message turn can run before it's aborted. |

### Setting variables for the launch script

```bash
CAIRO_WEB_PORT=9000 CAIRO_WEB_TOKEN=mytoken bash scripts/cairo-web.sh
```

### Setting variables for the systemd service

Edit the unit's environment:
```bash
systemctl --user edit cairo-web
```
Add an `[Service]` section with `Environment=` lines:
```ini
[Service]
Environment=CAIRO_WEB_PORT=9000
Environment=CAIRO_WEB_TOKEN=mytoken
```

---

## Authentication

If `CAIRO_WEB_TOKEN` is set, every API request (except `GET /api/health`) must include the token either as:

- An HTTP header: `Authorization: Bearer <token>`
- A cookie: `cairo_web_token=<token>`

The browser UI handles this automatically once you log in. Without a token set, the UI is open to anyone who can reach the port — keep the default `127.0.0.1` bind address unless you've set a token.

---

## Pointing at the right cairo binary (dev note)

The server defaults to finding `cairo` on your `PATH`. If you have the legacy `~/cairo` binary at `/usr/local/bin/cairo`, the web agent will use the wrong one.

Fix for development:

```bash
CAIRO_CLI_PATH=$(pwd)/bin/cairo bash scripts/cairo-web.sh
```

Or set it permanently in the systemd override.

---

## Security

- **Do not expose port 8787 to the internet without authentication.** The web UI can spawn cairo processes and read your database.
- Set `CAIRO_WEB_TOKEN` to a strong random string before binding to anything other than localhost.
- If you're accessing the web UI over a network you don't fully trust, put it behind a reverse proxy with TLS.

---

## Troubleshooting

**"Cannot find web-agent"** — The script looks for the web-agent in `CAIRO_WEB_AGENT_DIR`, the repo root, and `/usr/share/cairo/web-agent`. Run `scripts/build-web-agent.sh` to build it, or install via the package which places it at `/usr/share/cairo/web-agent`.

**"node_modules not found"** — The runtime hasn't been built. Run:
```bash
cd web-agent && npm install && npm run build
```

**Chat sends a message but nothing happens** — Check that `CAIRO_CLI_PATH` points to a working cairo binary and that the `ollama_url` config key is set correctly. Test the binary directly: `cairo "hello"`.

**Session list is empty** — The web UI reads from `CAIRO_DB_PATH`. Confirm it points to the same `cairo.db` your CLI sessions use (default `~/.cairo/cairo.db`).
