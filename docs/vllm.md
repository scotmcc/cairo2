# Calling the vLLM Stack from Go

The stack exposes an OpenAI-compatible API via LiteLLM. This doc is the
reference for application developers writing clients against it.

---

## Endpoint

All clients hit a single LiteLLM endpoint. There are no per-model URLs.

- **Direct (LAN/host)**: `http://<host>:4000/v1`
- **Via Tailscale (HTTPS)**: `https://<TS_HOSTNAME>/v1`

Auth header on every request:

```
Authorization: Bearer $LITELLM_MASTER_KEY
```

`LITELLM_MASTER_KEY` is set in the server `.env`. For per-app rotation, ask
the server operator to issue a virtual key.

---

## Available models

Use the **short name** in `model:`. Aliases exist for legacy callers.

| `model` | Backend | Best for | Context |
|---|---|---|---|
| `strong` *(alias `Qwen3.6-35B-A3B`)* | Qwen3.6-35B MoE | Highest quality, code, complex reasoning | 32k |
| `medium` *(alias `Qwen3-14B`)* | Qwen3-14B | Balanced quality/speed for chat | 32k |
| `fast` *(alias `Qwen3-4B`)* | Qwen3-4B | Quick replies, classification, light tools | 32k |
| `embed` *(alias `Qwen3-Embedding-8B`)* | Qwen3-Embedding-8B | Vector embeddings (4096 dims) | 32k |

Removed (do not call): `coder`, `guard`, `reranker`. Use `strong` for code.

---

## Thinking mode (READ THIS)

`strong`, `medium`, and `fast` are **thinking models**. By default they
generate a `<think>...</think>` block before the visible answer.

The server is configured with the `qwen3` reasoning parser, so:

- The thinking block is **stripped from `content`** and returned as a
  separate `reasoning_content` field on the assistant message.
- Most OpenAI Go clients ignore `reasoning_content`. That's fine — you just
  lose the trace.
- To skip thinking entirely (saves latency + output tokens), pass either:
  - `extra_body: {"chat_template_kwargs": {"enable_thinking": false}}`, or
  - append `/no_think` at the end of the user prompt.

---

## Sampling parameters — required care

**Never send `temperature: 0`** to a thinking model. The Qwen team is
explicit: greedy decoding causes endless repetition loops.

| Mode | temperature | top_p | top_k | min_p |
|---|---|---|---|---|
| Thinking (default) | **0.6** | 0.95 | 20 | 0 |
| Non-thinking | **0.7** | 0.8 | 20 | 0 |

`top_k` is not in the standard OpenAI schema — pass it via `extra_body`. If
you skip it, the model still works but quality drops in long generations.

For **code / structured output** with `strong` in thinking mode the docs
recommend the same `0.6 / 0.95 / 20` plus `presence_penalty: 0` (default
is 1.5).

---

## Go example — chat completion (typed client)

Using `github.com/sashabaranov/go-openai`:

```go
package main

import (
    "context"
    "fmt"
    "os"

    openai "github.com/sashabaranov/go-openai"
)

func main() {
    cfg := openai.DefaultConfig(os.Getenv("LITELLM_MASTER_KEY"))
    cfg.BaseURL = "https://<TS_HOSTNAME>/v1"  // or http://<host>:4000/v1
    client := openai.NewClientWithConfig(cfg)

    resp, err := client.CreateChatCompletion(
        context.Background(),
        openai.ChatCompletionRequest{
            Model:       "medium",
            Temperature: 0.6,
            TopP:        0.95,
            MaxTokens:   2048,        // OUTPUT tokens; never near 32k
            Messages: []openai.ChatCompletionMessage{
                {Role: "system", Content: "You are a helpful assistant."},
                {Role: "user",   Content: "Summarize the Treaty of Westphalia in 3 bullets."},
            },
        },
    )
    if err != nil { panic(err) }
    fmt.Println(resp.Choices[0].Message.Content)
}
```

`go-openai` does not expose `extra_body`. To pass `top_k` or
`chat_template_kwargs`, use a raw `net/http` request (next section).

---

## Go example — raw HTTP (when you need extra_body)

```go
body := map[string]any{
    "model": "medium",
    "messages": []map[string]string{
        {"role": "user", "content": "..."},
    },
    "temperature": 0.6,
    "top_p":       0.95,
    "top_k":       20,                                          // non-standard
    "max_tokens":  2048,
    // disable thinking for this request:
    "chat_template_kwargs": map[string]any{"enable_thinking": false},
}

buf, _ := json.Marshal(body)
req, _ := http.NewRequestWithContext(ctx, "POST",
    "https://<TS_HOSTNAME>/v1/chat/completions", bytes.NewReader(buf))
req.Header.Set("Authorization", "Bearer "+os.Getenv("LITELLM_MASTER_KEY"))
req.Header.Set("Content-Type", "application/json")
resp, err := http.DefaultClient.Do(req)
```

---

## Go example — tool / function calling

Tool calls work via the standard OpenAI `tools` schema. The server's parsers
(`hermes` for medium/fast, `qwen3_coder` for strong) translate the model's
internal `<tool_call>{...}</tool_call>` output into structured `tool_calls`
on the response. Use the standard `go-openai` `FunctionDefinition` and
`ToolType: openai.ToolTypeFunction` flow — no special handling required
beyond the OpenAI docs.

---

## Go example — embeddings

```go
resp, err := client.CreateEmbeddings(
    context.Background(),
    openai.EmbeddingRequestStrings{
        Model: "embed",
        Input: []string{"first document", "second document"},
    },
)
// resp.Data[i].Embedding is a []float32 of length 4096
```

For **retrieval queries** (asymmetric search), the Qwen embedding model
recommends prefixing the *query* (not the documents) with an instruction
for ~1–5% retrieval gain:

```
Instruct: Given a web search query, retrieve relevant passages.
Query: <actual query text here>
```

Documents on the indexing side are embedded as-is.

---

## Streaming

All chat models support streaming. Use
`client.CreateChatCompletionStream(...)` — works exactly like upstream
OpenAI. The SSE protocol is identical.

---

## Gotchas / FAQ

- **`max_tokens` is OUTPUT only**, but vLLM enforces
  `prompt + max_tokens ≤ 32768`. If you set `max_tokens=16384` and send a
  20k-token prompt, you get HTTP 400 from litellm.
- **First request after a server restart** is slow (~5–30s) because vLLM
  lazy-allocates KV cache slots and JIT-compiles CUDA graphs. Subsequent
  requests are fast.
- **Vision** on `strong`: the model technically has a vision encoder, but
  the serve config doesn't enable image input routes. Don't send images.
- **Concurrency**: vLLM batches automatically. Throw concurrent requests at
  it; don't manually serialize from your client.
- **Health check**: `GET http://<host>:4000/health` (litellm) returns 200
  when alive. Per-backend `/health` lives on ports 8000 (strong), 8001
  (medium), 8002 (fast), 8004 (embed) on the server but is not exposed via
  Tailscale.
- **Removed models**: requests to `coder`, `guard`, or `reranker` will
  return a "model not found" error as of 2026-05-02.
- **Don't trust `reasoning_content`** as part of the contract for tools —
  some clients drop it on JSON unmarshalling. If you need the reasoning
  trace, capture the raw HTTP response.

---

## Upstream model docs

- `strong`: https://huggingface.co/Qwen/Qwen3.6-35B-A3B
- `medium`: https://huggingface.co/Qwen/Qwen3-14B
- `fast`: https://huggingface.co/Qwen/Qwen3-4B
- `embed`: https://huggingface.co/Qwen/Qwen3-Embedding-8B