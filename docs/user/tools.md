# Tools

Cairo comes with a set of built-in tools the AI can call during a conversation to take actions on your behalf. You don't invoke tools directly — cairo decides when to call them based on the task. This document tells you what each tool does and any side effects you should know about.

---

## How tools work

When cairo calls a tool, you'll see the call and its result in the transcript. Tool calls are persisted in the database so they survive restarts and appear in session history.

Which tools are available depends on the session's **role**. The `thinking_partner` role has access to all built-in tools. Other roles have narrower allowlists. See [Roles and Aspects](roles-and-aspects.md).

The **discipline mode** (`-discipline` flag) provides a second layer of restriction:
- `readonly` — only tools that observe state (no writes, no shell)
- `scoped` — file writes allowed, but only within the session's cwd; no shell, no identity-layer tools
- `full` (default) — all tools

---

## Built-in tools

### Filesystem

**`read`**
Read file contents. Accepts an optional byte offset and limit. Requires `readonly` discipline or higher. Cairo uses this to inspect files before editing, read config files, and examine code.

**`write`**
Overwrite or create a file. Creates parent directories if they don't exist. Requires `scoped` discipline or higher. **This edits real files on your disk.** Cairo should read a file before writing it; if it doesn't, that's a sign to double-check the result.

**`edit`**
Replace a specific string in a file. The old string must be unique in the file; if it isn't, the call fails rather than making the wrong replacement. Requires `scoped` discipline or higher. **This edits real files on your disk.**

**`bash`**
Run a shell command. Default timeout 30 seconds, max 120 seconds. Requires `full` discipline. The shell has access to your full environment. Cairo uses this for running tests, building projects, querying databases with `sqlite3`, and any task that needs a real shell.

---

### Memory

**`memory_tool`**
All memory operations in one tool: add memories, search across memories/facts/summaries, delete memories, pin/unpin. Actions:
- `add` — write a new memory
- `search` — semantic (default), exact, or hybrid search across all three stores
- `delete` — remove a memory by id
- `pin` / `unpin` — protect or unprotect a memory from auto-removal

The `search` action is available in `readonly` discipline; `add`/`delete`/`pin`/`unpin` require `scoped` or higher.

---

### Skills

**`skill`**
Manage reusable instruction blocks. Actions: `list`, `read`, `create`, `update`, `delete`, `search`. Skills are named documents cairo can load to follow a specific workflow (e.g., the `init` skill drives the `/init` setup flow).

---

### Jobs and orchestration

**`job`**
Create, list, update, and delete orchestration units — named bodies of work with a status. Cairo uses jobs to track multi-step tasks it manages internally.

**`task`**
Create, list, update, and delete steps within a job. Tasks support a `depends_on` field for sequencing, and a `ready` action to mark a task available for a worker. Workers are spawned via `agent`.

**`agent`**
Spawn, wait for, and log background subprocess workers. Each worker runs as a separate `cairo` process with its own session, operating in `background` mode. Requires `full` discipline.

**`worktree`**
Create and manage per-job git worktrees — isolated checkouts where background workers can make changes without touching your working tree. Requires `full` discipline.

**`merge_job`**
Approve or reject a job's accumulated changes. Supports rebase and squash-merge strategies before pushing. Requires `full` discipline.

---

### Identity

**`soul`**
Get or set the character sketch — a short description (max 300 characters) of cairo's identity for this installation. Requires `full` discipline because it's an identity-layer change.

**`config`**
Get, set, or list config key-value pairs. Requires `full` discipline. Cairo uses this to read settings like `ollama_url` and write things like `init_complete`. See [Config keys reference](../reference/config-keys.md) for the full list.

**`prompt_part`**
Manage system prompt fragments — composable pieces that get assembled into the system prompt each turn. Role framings, base instructions, and user-customizable sections are all stored as prompt parts.

---

### Web

**`search`**
Semantic web search via SearXNG. Requires `searxng_url` to be configured; disabled if that key is empty. Requires `scoped` discipline or higher.

**`fetch`**
Fetch a URL and return its content. Cairo can summarize or extract structured data from the result. Requires `scoped` discipline or higher.

---

### Voice

**`say`**
Speak text aloud via Kokoro TTS. Requires `kokoro_url` to be configured; disabled if empty. Sends the text to the TTS server and plays audio asynchronously. Requires `scoped` discipline or higher.

---

### Project indexing

**`learn`**
Per-project file indexer. Actions:
- `add` — index a directory (walk, summarize, embed)
- `search` — semantic search over indexed file summaries
- `list` — list indexed projects
- `describe` — show a project's auto-generated description
- `forget` — remove a project from the index
- `status` — show indexing progress

`list`, `search`, `describe`, and `status` work in `readonly` discipline. `add` and `forget` require `scoped` or higher.

Note: `code_search` was removed in v0.3.0. `learn` is the only codebase search path.

---

### Interaction

**`choose`**
Present a blocking multi-choice prompt to the user and wait for a selection. **TUI only** — in headless or line-CLI mode this always returns an error. Cairo uses this when it genuinely needs a decision from you before proceeding.

**`consider_input`**
Inner-dialogue step used by the Consider (aspects) system. Runs aspect evaluations and writes results to `consider_activations`. Requires `scoped` discipline or higher. You'll typically never see this called unless you have aspects configured.

---

### Utility

**`tool_list_builtin`**
Returns the list of all registered built-in tool names. Cairo can call this to check what's available in its current role.

---

## Custom tools

You can add your own tools — shell scripts or executables that cairo can call the same way it calls built-ins. Custom tools are always available regardless of role allowlists.

To create a custom tool, ask cairo:
```
> create a custom tool called "deploy" that runs scripts/deploy.sh
  with the current branch as an argument
```

Cairo will write the tool definition to the `custom_tools` table. List your custom tools with `/tools` in the line CLI.

---

## Side-effect summary

| Tool | Writes files? | Runs shell? | Network? | DB changes? |
|---|---|---|---|---|
| `read` | No | No | No | No |
| `write` | **Yes** | No | No | No |
| `edit` | **Yes** | No | No | No |
| `bash` | **Yes** | **Yes** | **Yes** | No |
| `memory_tool` | No | No | No | **Yes** |
| `skill` | No | No | No | **Yes** |
| `job` / `task` | No | No | No | **Yes** |
| `agent` | No | **Yes** | No | **Yes** |
| `worktree` | **Yes** | No | No | **Yes** |
| `config` | No | No | No | **Yes** |
| `soul` | No | No | No | **Yes** |
| `search` | No | No | **Yes** | No |
| `fetch` | No | No | **Yes** | No |
| `say` | No | No | **Yes** | No |
| `learn` | No | No | No | **Yes** |
