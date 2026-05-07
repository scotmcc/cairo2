# internal/llm

**Layer:** Foundation  
**Status:** ✅ working, moves as-is

LLM client abstraction. Speaks to Ollama and OpenAI-compatible endpoints. The agent loop only sees the interface defined here — swapping models or providers is a config change, not a code change.

In enterprise mode, this could be wired to an air-gapped model server or a private OpenAI-compatible API. The interface stays the same.

Source: `~/cairo/internal/llm/`.
