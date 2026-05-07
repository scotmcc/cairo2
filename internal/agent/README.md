# internal/agent

**Layer:** Foundation — The Brain  
**Status:** ✅ working, moves as-is

The agent loop, prompt assembly, and response summarizer. This is the core intelligence of the system.

Knows nothing about the TUI, the HTTP server, or the enterprise stack. Takes an LLM client, a tool set, and a DB and runs the loop. That's it. This package being surface-agnostic is what makes cairo reusable — the same loop runs in TUI mode, HTTP serve mode, VS Code mode, and as the enterprise agent.

## Responsibilities

- Conversation loop (user turn → tool dispatch → LLM response → repeat)
- System prompt assembly from roles, context providers, and memory
- Response summarization for long sessions
- Tool call dispatch and result collection

## Source

Migrates from `~/cairo/internal/agent/`.
