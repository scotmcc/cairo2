# Customizing Cairo

Cairo ships with defaults that are intentionally generic. The real value comes when you shape it to match how you actually work. This guide covers the three main levers: roles, configuration keys, and the web interface.

---

## Roles

A role changes how Cairo approaches your session. Think of it like setting the context for the conversation: a "coder" role has different tendencies than a "reviewer" role or a "planner" role.

Cairo ships with several built-in roles. To see what's available:

```
> /help
```

or ask directly:

```
> what roles do you have?
```

Cairo will list them and describe what each one emphasizes.

### Starting a session in a specific role

When launching Cairo from the terminal, pass the role as a flag:

```bash
cairo -tui -role coder
```

```bash
cairo -tui -role reviewer
```

```bash
cairo -tui -role planner
```

Each role has its own system prompt framing and may use a different model (if you've configured role-specific models).

### Checking and changing the current role

To check what role you're in:

```
> what role are we in?
```

To switch roles mid-session, ask Cairo directly:

```
> switch to the reviewer role
```

Cairo will confirm the switch. Note that role-switching mid-session changes how the model responds going forward; it doesn't rewrite the conversation history.

---

## Configuration keys

Cairo's behavior is controlled by a set of configuration keys stored in `~/.cairo/cairo.db`. You can read and set them from the terminal.

### Reading a config value

```bash
cairo config get model
```

```bash
cairo config get ollama_url
```

### Setting a config value

```bash
cairo config set model qwen2.5:14b
```

```bash
cairo config set ollama_url http://localhost:11434
```

```bash
cairo config set user_name "Your Name"
```

Changes take effect on the next session start. To apply them immediately, restart Cairo or use:

```
> /reload
```

inside the TUI (this restarts Cairo in-place).

### Useful config keys to know

| Key | What it controls |
|---|---|
| `model` | The chat model used for conversations |
| `ollama_url` | The LLM endpoint URL (Ollama, LiteLLM, vLLM, etc.) |
| `llm_api_key` | API key for the LLM endpoint (if required) |
| `user_name` | Your name — Cairo uses this to address you |
| `summary_model` | Model used for background summarization |
| `embed_model` | Model used for memory search (requires an embedding model) |

For a complete reference, see [docs/reference/config-keys.md](../reference/config-keys.md).

---

## Prompt parts and aspects

Beyond roles, Cairo has a finer-grained customization layer called **prompt parts**. These are pieces of text that get injected into Cairo's system prompt every time it starts — effectively always-on instructions.

You can add a prompt part by telling Cairo what you want it to always do (or never do):

```
> always respond with concise answers — no padding, no unnecessary context
```

```
> never suggest adding comments to code unless I ask for them
```

Cairo will store these as prompt parts and apply them in every future session.

To see what's currently set:

```
> show me my prompt parts
```

To remove one:

```
> remove the prompt part about concise responses
```

This is also how you shape the personality. If the default tone is too formal, too verbose, or not direct enough, just tell Cairo:

```
> be more direct — I prefer short answers over thorough ones
```

It stores the preference and adjusts.

---

## Skills

Skills are reusable instruction blocks for multi-step workflows. Cairo ships with a few built-in ones (`/init` for setup, `/init codebase` for project exploration), and you can write your own.

To see your current skills:

```
> /help
```

or:

```
> show me my skills
```

To add a skill during a conversation:

```
> write a skill called "quick-review" that reads the git diff, summarizes
  what changed, and notes any obvious issues — then waits for my go-ahead
  before doing anything else
```

Cairo will store that instruction and you can trigger it by name in future sessions.

---

## The web interface

If you prefer a browser to a terminal for day-to-day use, Cairo includes a web agent — a local Node.js server that runs alongside the Cairo process and serves a browser UI.

The web interface gives you the same conversation capability in a standard browser tab. It's useful if you're more comfortable in a graphical environment, or if you want to access Cairo from a different device on your network.

To start the web interface:

```bash
bash scripts/cairo-web.sh
```

Then open your browser to the address printed in the output (typically `http://localhost:3000` or similar).

The web UI reads from the same `~/.cairo/cairo.db` database as the TUI — your sessions, memories, and config are shared.

A few environment variables control the web agent:

| Variable | What it controls |
|---|---|
| `CAIRO_WEB_PORT` | Port to listen on (default: 3000) |
| `CAIRO_WEB_TOKEN` | Auth token for the web interface |
| `CAIRO_CLI_PATH` | Path to the cairo binary to use |

If you're running Cairo from the source directory (not system-installed), set `CAIRO_CLI_PATH` so the web agent uses the right binary:

```bash
export CAIRO_CLI_PATH=$(pwd)/bin/cairo
bash scripts/cairo-web.sh
```

For persistent use, `scripts/install-web-agent.sh` installs the web agent as a systemd user service that starts automatically.

---

## Exporting and importing your identity

Everything Cairo knows — your name, your memories, your prompt parts, your skills, your roles — is in `~/.cairo/cairo.db`. You can snapshot it:

```bash
cairo export my-identity.cairo
```

And restore it on another machine (or after an experiment gone wrong):

```bash
cairo import my-identity.cairo
```

This is also how you share a configured identity with someone else, or keep a backup before making big changes.

To compare a bundle to your current state before importing:

```bash
cairo diff their-identity.cairo
```

---

## Next

[Where to Go Next](06-where-to-go-next.md) — depending on what you want to do with Cairo, different parts of the documentation will be most useful.
