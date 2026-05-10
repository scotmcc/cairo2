# Your First Chat

This is a guided walkthrough of a real conversation with Cairo. We'll read a file, explore a directory, and ask Cairo to write something. Along the way we'll explain what's actually happening under the hood so none of it feels like magic.

Start Cairo if it's not already running:

```bash
cairo -tui
```

or the line CLI if you prefer simpler output:

```bash
cairo
```

---

## What Cairo can do in a conversation

When you type a message, Cairo sends it to your LLM — along with context about who you are, what tools are available, and what the current session is about. The LLM decides how to respond: sometimes with a direct answer, sometimes by calling a **tool**.

Tools are Cairo's hands. The LLM can't directly read your files or run commands on its own — but it can ask Cairo to do those things via tools. When that happens you'll see something like:

```
[tool: read_file("README.md")]
[tool: run_shell("ls -la internal/")]
```

These appear in the transcript before the response. Cairo executes the tool, sends the result back to the LLM, and the LLM uses that information to write its reply. You see the whole chain.

You're always in control — if you see a tool call you didn't expect, you can say "stop, don't do that" and Cairo will explain what it was doing and why.

---

## Part 1: Read a file

Let's start with something simple. Pick any text file on your system — a README, a config file, a script. For this example we'll use a README.

```
> can you read the README.md in the current directory and give me a one-paragraph summary?
```

What you'll see happen:

1. Cairo (via the LLM) decides it needs to read the file.
2. It calls `read_file("README.md")`.
3. The file contents come back, and the LLM reads them.
4. The LLM writes a summary.

The response will be something like (paraphrased — your actual LLM output will vary):

```
Cairo is an AI coding agent built in Go. The README describes it as
a local-first assistant that runs alongside your development environment.
It uses a SQLite database for local storage and connects to any
OpenAI-compatible LLM. Three binaries are included: cairo (the agent),
cairo-registry (fleet server), and cairo-ctl (admin CLI).
```

Notice that Cairo didn't ask you to paste the file — it just went and read it. That's the difference between a chatbot and an agent.

If there's no README in your current directory, ask Cairo to read any file you have:

```
> read the file at /etc/hosts and tell me what's in it
```

---

## Part 2: Explore a directory

Now let's ask Cairo to survey a directory and tell you what it sees.

```
> look at the files in the current directory and describe what kind of project this is
```

Cairo will call `run_shell` with something like `ls -la` or `find . -maxdepth 2`, then read a few key files to get context, and give you a description.

You might see:

```
[tool: run_shell("ls -la")]
[tool: read_file("go.mod")]
[tool: read_file("scripts/build.sh")]
```

Followed by a response like:

```
This is a Go project. The go.mod file shows the module is
github.com/scotmcc/cairo2. There are scripts for building and
packaging, a cmd/ directory with three binaries, and an internal/
directory with the core packages. It looks like a self-contained
CLI tool with packaging infrastructure for deb and rpm distribution.
```

Try it in your own project directory — run `cd /path/to/your/project` before starting Cairo, or navigate there in a separate terminal and restart Cairo there.

---

## Part 3: Ask Cairo to write something

Let's ask Cairo to produce some output. We'll keep this simple — a short shell script.

```
> write a shell script that counts the number of .go files in the current directory tree and prints the total
```

Cairo will think about this, then either write the script inline or create a file. If it creates a file you'll see:

```
[tool: write_file("count_go.sh", "#!/bin/bash\nfind . -name '*.go' | wc -l\n")]
```

And it'll tell you:

```
I've written count_go.sh. It uses 'find' to locate all .go files
recursively and 'wc -l' to count them. Make it executable with:

    chmod +x count_go.sh

Then run it with ./count_go.sh.
```

If you'd rather it didn't write a file and just showed you the contents instead, ask:

```
> just show me the script, don't write a file
```

Cairo will print it directly. You're in charge of what gets written to disk.

---

## Part 4: Have a follow-up conversation

The conversation has memory within a session. Cairo knows everything that was said in this session without you having to repeat yourself.

```
> can you modify the script to also count .md files and show them separately?
```

Cairo knows what script you mean (from earlier in the conversation) and will update it:

```
[tool: write_file("count_go.sh", ...updated contents...)]

Updated. The script now counts .go and .md files separately and
prints each total with a label.
```

Try asking follow-up questions about the files Cairo already read:

```
> based on the directory you looked at earlier, what would be a good first thing to change if I wanted to add a new subcommand?
```

Cairo still has the file contents from earlier in its context and can reason about them without re-reading everything.

---

## Slash commands

While you're in a conversation, there are a few special commands you can type:

| Command | What it does |
|---|---|
| `/help` | List available slash commands |
| `/new` | Start a fresh session (the current one is saved) |
| `/sessions` | List all past sessions |
| `/memories` | Show what Cairo has stored in long-term memory |
| `/jobs` | Show background tasks |
| `/tools` | Show the available tools |
| `/reload` | Restart Cairo in-place (useful after config changes) |

Try `/sessions` now — it'll list the session you're in. After a few conversations it'll show your history.

```
> /sessions
```

You'll see something like:

```
  #1  session-001  today · 12 messages  (current)
```

To start a fresh conversation (without losing the current one):

```
> /new
```

Cairo will drain any background tasks from the current session and open a clean one. The old session is still there in `/sessions` if you want to go back to it.

---

## What Cairo remembers across sessions

Within a session, Cairo remembers everything you said. Across sessions, it remembers things that were explicitly stored as **memories** — facts about you, your project, your preferences.

Right now your memory store is probably empty (you just installed Cairo). Over time, as you work with Cairo, it will accumulate facts: your name, what your project does, coding conventions you care about, commands you use often.

To see what's stored:

```
> /memories
```

To tell Cairo something worth remembering:

```
> remember that I prefer shell scripts over Python for automation tasks
```

Cairo will call `memory_tool` to store that fact, and it'll appear in future sessions automatically.

---

## Next

[Customizing Cairo](05-customizing.md) — pick a role, adjust how Cairo behaves, and set config values to match your workflow.
