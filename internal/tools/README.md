# internal/tools

**Layer:** Foundation  
**Status:** ✅ working, moves as-is

Tool registry and ~15 built-in tools (file read/write, bash execution, search, worktree ops, etc.).

Tools are registered at startup. Enterprise deployments can register additional tools from `internal/services/automation/` — the agent loop doesn't distinguish between built-in tools and enterprise tools.

Source: `~/cairo/internal/tools/`.
