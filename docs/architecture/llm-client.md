# LLM client

`internal/llm` is the boundary between Cairo and the LLM backend. Cairo uses the OpenAI-compatible HTTP API — it works with Ollama (v0.1.24+), LiteLLM, vLLM, or any server exposing standard OpenAI endpoints. One HTTP client, no SDK dependency.

The surface is narrow on purpose: `New`, `Ping`, `StreamOnce`, `Complete`, `Embed`, `FetchModelInfo`, `ListModels`.

---

## The client

`llm.Client` wraps two `http.Client` instances — one without a global timeout for streaming calls (deadline is on the request context), one with a 15s timeout for short calls:

```go
type Client struct {
    url       string
    apiKey    string
    http      *http.Client // no global timeout — streaming uses request context
    shortHTTP *http.Client // 15s timeout for health checks and non-streaming calls
}

func New(url, apiKey string) *Client
```

`apiKey` is sent as `Authorization: Bearer <key>` on every request when non-empty. The default URL is `http://localhost:11434` (Ollama's default); override via the `ollama_url` config key or `OLLAMA_URL` env var.

**`Ping()`** hits `GET /health` (standard LiteLLM/vLLM health endpoint) using the short HTTP client. Returns an error if unreachable or non-200.

---

## `StreamOnce`

The main function. One HTTP POST to `/v1/chat/completions` with `stream: true`, SSE response.

```go
func (c *Client) StreamOnce(
    ctx context.Context,
    model string,
    messages []Message,
    tools []ToolDef,
    opts ChatOptions,
    cb ChatCallbacks,
) (text string, toolCalls []ToolCall, budgetExceeded bool, err error)
```

**One call, one response.** Not a loop. If the model emits tool calls, that's what `toolCalls` will contain; the caller (`runLoop` in `internal/agent/loop.go`) decides what to do with them.

**Streaming.** The server emits SSE `data:` lines, each containing a JSON delta. `StreamOnce` parses these with a 4MB line buffer (large enough for tool calls whose arguments include big file contents), accumulates content bytes, and fires `cb.Content(token)` / `cb.Thinking(token)` for each chunk so UIs can render live.

**Context cancellation.** The context is propagated into `http.NewRequestWithContext`. Cancelling aborts the stream; whatever text accumulated is returned along with `ctx.Err()`. This is what lets Ctrl-C in the TUI produce "partial text + (interrupted)" rather than "turn lost."

**Thinking budget.** `ChatOptions.ThinkBudget` caps reasoning tokens. If exceeded, `StreamOnce` returns `budgetExceeded=true` without an error; the caller retries without thinking enabled.

---

## `Complete`

Non-streaming one-shot completion via `POST /v1/chat/completions` with `stream: false`. Used by the summarizer and other callers that don't need progressive output. Returns the full response text.

---

## `FetchModelInfo`

`internal/llm/modelinfo.go`. Called at startup to populate `model_ctx` for dynamic memory budgeting.

```go
func FetchModelInfo(ctx context.Context, baseURL, apiKey, model string) (ModelInfo, error)
```

Calls LiteLLM's `GET /model_group/info` endpoint. Returns `ErrModelNotFound` when the model group is absent from the response data array. A 30-second timeout is applied on top of the caller's context.

---

## `ListModels`

`GET /v1/models` — returns the list of model IDs available on the server. Used by the `cairo serve` OpenAI compatibility layer.

---

## Message shape

```go
type Message struct {
    Role        string     `json:"role"`                   // system | user | assistant | tool
    Content     string     `json:"content,omitempty"`
    ToolCalls   []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID  string     `json:"tool_call_id,omitempty"` // required on role=tool (OpenAI spec)
}

type ToolCall struct {
    Function struct {
        Name      string `json:"name"`
        Arguments any    `json:"arguments"` // map[string]any or raw JSON string
    } `json:"function"`
}

type ToolDef struct {
    Type     string `json:"type"`              // always "function"
    Function struct {
        Name        string `json:"name"`
        Description string `json:"description"`
        Parameters  any    `json:"parameters"` // JSON Schema
    } `json:"function"`
}
```

This shape is the OpenAI spec. It works with any OpenAI-compatible backend.

---

## Message normalization

Before the request is sent, outbound messages go through a normalization pass (`normalizeMessages`) that:

1. Strips tool-result messages where `IsError` is true (OpenAI has no native error message type).
2. Defaults any `ToolCall.Type` to `"function"` when missing (required by spec).
3. Enforces system-role-at-position-0 (vLLM/OpenAI enforces this; Ollama is more lenient).

---

## Tool call argument normalization

Models are inconsistent about whether they emit `arguments` as a JSON object or a JSON string containing an object. `ToolCall.Args()` handles both:

```go
func (tc *ToolCall) Args() map[string]any
```

The normalize helper inspects the runtime type: if it's already a `map[string]any` it returns it directly; if it's a `string` it unmarshals. Cheap insurance against a model that learned the wrong convention.

---

## Synthetic call IDs

The OpenAI spec requires stable tool-call IDs for correlating requests with results. When a model response lacks them, Cairo synthesizes:

```go
func (tc *ToolCall) CallID(seq int) string {
    return fmt.Sprintf("call_%s_%d", tc.Function.Name, seq)
}
```

IDs are stable within a turn but re-synthesized on resume (name-based, not id-based). Documented as a known rough edge in [Agent loop](agent-loop.md#known-rough-edges).

---

## Embeddings

`internal/llm/embed.go` handles `POST /v1/embeddings`:

```go
func (c *Client) Embed(ctx context.Context, model, text string) ([]float32, error)
```

One request, one vector. Used by the memory tool on add, the memory/summary search tools on query, and the summarizer on write.

---

## Error handling philosophy

The client's job is to:

1. Round-trip the HTTP call cleanly.
2. Distinguish connection errors, HTTP errors, and context cancellation.
3. Return partial progress on cancel rather than discarding it.
4. Return typed sentinels (`ErrModelNotFound`, `ErrUnauthorized`) where callers need to branch.

Non-2xx responses are parsed as OpenAI error envelopes (`{"error": {"message": ..., "type": ..., "code": ...}}`); 401/403 are wrapped with `ErrUnauthorized` so callers can detect auth failures.

---

## What's required from the server

Cairo targets the OpenAI HTTP API. Required endpoints:

- `POST /v1/chat/completions` — streaming (SSE) and non-streaming modes, with tool use
- `POST /v1/embeddings` — single-input float32 vector
- `GET /health` — health check (200 = OK)
- `GET /model_group/info` — LiteLLM endpoint for context-length metadata (optional; `FetchModelInfo` returns `ErrModelNotFound` gracefully)
- `GET /v1/models` — OpenAI-compatible model list (used by `cairo serve`)

Ollama (v0.1.24+), LiteLLM, and vLLM all satisfy these requirements.

---

## Known rough edges

- **Synthetic call IDs.** Documented above. Real IDs from the backend improve resume fidelity.
- **No retry on transient network errors.** A blip hitting the backend fails the turn. In practice a local server is reliable enough that retries haven't been worth the added complexity.
- **Budget-exceeded retry is the caller's responsibility.** When `budgetExceeded=true` is returned, tokens already streamed to the UI stay on screen. The retry starts a fresh generation and the tokens visually concatenate. The right fix is callback-side — not yet written.
