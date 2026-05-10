# What Is Cairo?

Cairo is a local AI coding agent that lives on your machine, not in someone else's cloud. You point it at an LLM — running locally with Ollama, or at a remote API endpoint you control — and it becomes a thinking partner that reads your files, runs your tools, remembers your preferences across sessions, and helps you get work done. Everything stays on your machine. Nothing is sent to any Cairo server. You own the data.

---

## Who this is for

Cairo is for people who want to work alongside an AI assistant without giving up control of their environment or their data. You might be:

- A developer who wants an AI pair programmer that actually knows your project structure.
- A technical writer or analyst who wants to query documents, summarize files, and draft content without copy-pasting into a chat window.
- A team running a private LLM and looking for a client that connects to it cleanly.
- Someone curious about local AI who wants something that goes deeper than a demo.

You don't need to be a software developer to use Cairo. The guided setup walks you through everything. You will need to be comfortable opening a terminal (the text-based command window on your computer — on a Mac it's called Terminal, on Linux it depends on your desktop) and typing a few commands.

Cairo rewards patience. The first ten minutes are setup. After that it starts to feel like a colleague who's been reading your files and taking notes.

---

## What Cairo is NOT

### Not ChatGPT or a hosted chatbot

ChatGPT, Claude.ai, Gemini, and similar tools run on servers controlled by their companies. Every message you type goes to their infrastructure, gets processed, and a response comes back. That's fine for many uses, but it means your code, your documents, and your questions all pass through someone else's systems.

Cairo is the opposite. It runs on your machine. You choose what LLM to connect it to — which could also be local. Nothing about your conversations is sent to Cairo's servers because there are no Cairo servers.

### Not Cursor or a code editor extension

Cursor, GitHub Copilot, Codeium, and similar tools live inside your editor. They're good at autocompleting code as you type.

Cairo is more like a conversation you have alongside your editor. You describe what you want — "read the files in this directory and tell me what the API surface looks like" — and Cairo does the legwork: reading files, running shell commands, searching code, writing output. It's less about autocomplete and more about delegation.

There is also a VS Code extension for Cairo that adds some editor integration, but the core experience is conversational, not inline.

### Not a fully managed AI platform

GitHub Copilot Workspace, Devin, and similar "AI engineer" products manage everything for you — you describe a task, they execute it, you review the result. Impressive demos.

Cairo gives you more control and more visibility into what's happening. When Cairo calls a tool — reads a file, runs a command, writes output — you see it happen. You can interrupt at any point. The LLM never acts without the exchange passing through your terminal.

### Not a one-size-fits-all assistant

Cairo ships with a default identity named Selene: a character sketch, several roles, and a handful of built-in skills. But Selene is a starting point, not a fixed product. You can change the name, rewrite the personality, define new roles and skills, and shape the assistant into something that fits your workflow. The whole personality lives in a local SQLite database you can inspect, edit, export, and import.

---

## The short version

> Cairo is a private, local-first AI agent. You bring the LLM; Cairo handles the conversation, memory, tools, and file access. Your data stays with you.

---

## Next: install it

Head to [Installation](02-installation.md) to get Cairo running on your machine. If you already have it installed, skip ahead to [First Run](03-first-run.md).
