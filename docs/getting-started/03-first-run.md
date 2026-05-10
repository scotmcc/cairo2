# First Run

You've installed Cairo. Now let's start it up, connect it to an LLM, and make sure everything is talking to each other. This should take about ten minutes.

---

## What you need before starting

Cairo is a client — it needs an LLM (large language model) to talk to. It doesn't bundle a model itself. You have two common options:

**Option 1 — Ollama running locally**
[Ollama](https://ollama.com) runs LLMs on your own machine. No internet required once the model is downloaded. Best for privacy and offline use. Requires a machine with enough RAM for the model you choose (8GB minimum for smaller models; 16–32GB for good ones).

**Option 2 — Any OpenAI-compatible API endpoint**
LiteLLM, vLLM, a remote Ollama instance, or a hosted provider that speaks the OpenAI API format all work. You'll need the URL and, if required, an API key.

Cairo's LLM connection config key is named `ollama_url` for historical reasons — but it accepts any OpenAI-compatible endpoint, not just Ollama. Don't let the name confuse you.

---

## Open a terminal

A terminal is the text-based command window where you type instructions directly to your computer. On macOS it's called **Terminal** (in Applications → Utilities). On Linux it depends on your desktop environment; look for **Terminal**, **Konsole**, or **GNOME Terminal**.

Everything in this guide happens in the terminal.

---

## Start Cairo

### The TUI (recommended for new users)

The TUI (short for Terminal User Interface) is Cairo's full-screen interface. It has panels, a conversation transcript, and keyboard shortcuts. It's the richest way to interact.

If you installed Cairo system-wide (Option B from the installation guide):

```bash
cairo -tui
```

If you built locally and are running from the source directory (Option C):

```bash
./bin/cairo -tui
```

The screen will clear and the TUI will appear. The first time, it may take a moment to create `~/.cairo/cairo.db`.

### The line CLI (simpler alternative)

If the full-screen TUI feels like too much at first, the bare command drops you into a simple line-by-line prompt instead:

```bash
cairo
```

or

```bash
./bin/cairo
```

You'll see a `>` prompt. Type a message and press Enter. Responses stream back in the terminal. No panels, no motion — just text. Both modes use the same underlying agent and the same data; the difference is purely visual.

This guide focuses on the TUI, but everything about configuration and chatting works identically in the line CLI.

---

## What you see when it starts

The first time Cairo runs, it creates `~/.cairo/cairo.db` and seeds it with defaults. The TUI will show something like:

```
┌─ Cairo ───────────────────────────────────────────┐
│  Selene · session 1                               │
│                                                   │
│  (No LLM configured yet — set ollama_url to       │
│   connect to a model)                             │
│                                                   │
│  Type /help for commands                          │
│                                                   │
│  >                                                │
└───────────────────────────────────────────────────┘
```

The name "Selene" is the default identity that ships with Cairo. You can rename or reshape it later — for now, it's just a label.

The important thing first is to connect Cairo to an LLM.

---

## Connect Cairo to an LLM

### Option 1: Ollama running on this machine

If you have Ollama installed and running locally, and you've pulled at least one model, point Cairo at it:

```bash
cairo config set ollama_url http://localhost:11434
```

Then set the model name to whatever you've pulled. To check what's available:

```bash
ollama list
```

You'll see output like:

```
NAME                        ID              SIZE   MODIFIED
qwen2.5:7b                  ff27e30e2027    4.7 GB  2 days ago
nomic-embed-text:latest     0a109f422b47    274 MB  5 days ago
```

Set the chat model (use the name from the NAME column):

```bash
cairo config set model qwen2.5:7b
```

### Option 2: A remote OpenAI-compatible endpoint

If you're connecting to LiteLLM, vLLM, a hosted provider, or a remote Ollama instance:

```bash
cairo config set ollama_url http://your-server-address:4000
```

Replace `your-server-address:4000` with the actual host and port of your endpoint.

If the endpoint requires an API key:

```bash
cairo config set llm_api_key sk-your-key-here
```

Then set the model name to whatever the endpoint serves:

```bash
cairo config set model gpt-4o
```

(The model name is whatever string the API expects — for LiteLLM proxies this is often the upstream model name.)

### Verify the connection

From inside the TUI, type:

```
/help
```

If Cairo responds with a list of commands, the LLM connection is working. If you see a connection error, double-check the URL you set and make sure the LLM service is actually running.

---

## What the TUI looks like

The TUI is divided into a few regions:

**Transcript area (top, largest region)** — this is where the conversation appears. Your messages on one side, Cairo's responses on the other. Tool calls (when Cairo reads a file, runs a command, etc.) appear here too.

**Input area (bottom)** — the `>` prompt where you type your message. Press Enter to send.

**Status bar** — a thin line showing the current session name and role.

Panels for memory, threads, and prompt preview can be toggled with keyboard shortcuts (`Ctrl-E`, `Ctrl-T`, `Ctrl-P`). You don't need to worry about these yet — they're useful once you're comfortable with the basics.

To exit the TUI:

```
/exit
```

Or press `Ctrl-C` if you need to force-quit.

---

## Send your first message

Once the LLM is configured, try saying hello:

```
> hi, what can you help me with?
```

Cairo will describe what it can do: read files, run shell commands, search codebases, remember things across sessions, help you write and review code, answer questions about your project.

If the response streams back cleanly, you're fully set up. Head to [Your First Chat](04-your-first-chat.md) to see what Cairo can actually do.

---

## If you see errors

**Connection refused / LLM unreachable**
The URL you set isn't responding. Check that your LLM service is running. For Ollama: `ollama serve` in a separate terminal. For remote endpoints: verify the host and port.

**Model not found**
The model name you set doesn't match what the LLM service knows about. For Ollama: run `ollama list` and copy the exact name. For remote endpoints: check the API's model list.

**`~/.cairo/cairo.db` permission error**
Rare. Usually means the directory was created with wrong permissions. Try `ls -la ~/.cairo/` and make sure you own it. If not: `sudo chown -R $USER ~/.cairo`.

---

## Your data directory

Everything Cairo knows about you — your conversations, memories, config, identity — lives in one file:

```
~/.cairo/cairo.db
```

It's a standard SQLite database. You can inspect it with any SQLite tool, export it with `cairo export`, or reset it by deleting it (Cairo will reseed from defaults on the next run).

If you want to use a different location — for testing or for keeping separate instances:

```bash
export CAIRO_DATA_DIR=/path/to/your/dir
cairo -tui
```

---

## Next

[Your First Chat](04-your-first-chat.md) — a guided walkthrough of a real conversation.
