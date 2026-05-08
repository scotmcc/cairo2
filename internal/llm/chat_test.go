package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSerializeMessages_ErrorPrefix verifies that a Message with IsError=true
// gets the "[tool error] " prefix applied to its content by serializeMessages.
func TestSerializeMessages_ErrorPrefix(t *testing.T) {
	msgs := []Message{
		{Role: "tool", Name: "bash", Content: "command not found", IsError: true},
	}
	out := serializeMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	want := "[tool error] command not found"
	if out[0].Content != want {
		t.Errorf("content = %q, want %q", out[0].Content, want)
	}
}

// TestSerializeMessages_NoErrorPrefix verifies that a non-error message is
// not modified by serializeMessages.
func TestSerializeMessages_NoErrorPrefix(t *testing.T) {
	msgs := []Message{
		{Role: "tool", Name: "bash", Content: "hello world", IsError: false},
	}
	out := serializeMessages(msgs)
	if out[0].Content != "hello world" {
		t.Errorf("content = %q, want %q", out[0].Content, "hello world")
	}
}

// TestSerializeMessages_NoDuplicatePrefix verifies that a message already
// carrying the prefix is not double-prefixed.
func TestSerializeMessages_NoDuplicatePrefix(t *testing.T) {
	msgs := []Message{
		{Role: "tool", Name: "bash", Content: "[tool error] already prefixed", IsError: true},
	}
	out := serializeMessages(msgs)
	want := "[tool error] already prefixed"
	if out[0].Content != want {
		t.Errorf("content = %q, want %q", out[0].Content, want)
	}
}

// TestSerializeMessages_DefaultsToolCallType verifies that tool calls with empty
// Type are defaulted to "function", and pre-set Type values are preserved.
func TestSerializeMessages_DefaultsToolCallType(t *testing.T) {
	msgs := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{Type: "", ID: "call_1", Function: struct {
					Name      string `json:"name"`
					Arguments any    `json:"arguments"`
				}{Name: "bash", Arguments: `{"cmd":"ls"}`}},
				{Type: "function", ID: "call_2", Function: struct {
					Name      string `json:"name"`
					Arguments any    `json:"arguments"`
				}{Name: "read", Arguments: `{"path":"/etc/passwd"}`}},
			},
		},
	}
	out := serializeMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if len(out[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(out[0].ToolCalls))
	}
	// First call should be defaulted to "function"
	if out[0].ToolCalls[0].Type != "function" {
		t.Errorf("call[0].Type = %q, want %q", out[0].ToolCalls[0].Type, "function")
	}
	// Second call should be preserved
	if out[0].ToolCalls[1].Type != "function" {
		t.Errorf("call[1].Type = %q, want %q", out[0].ToolCalls[1].Type, "function")
	}
}

// TestSerializeMessages_DemotesMidConversationSystem verifies that system messages
// appearing mid-conversation are demoted to user role with "[harness] " prefix.
func TestSerializeMessages_DemotesMidConversationSystem(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "system", Content: "nudge"},
		{Role: "user", Content: "u2"},
	}
	out := serializeMessages(msgs)
	if len(out) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(out))
	}
	// Position 0: system message at start should remain unchanged
	if out[0].Role != "system" || out[0].Content != "sys" {
		t.Errorf("out[0] = {%q, %q}, want {system, sys}", out[0].Role, out[0].Content)
	}
	// Position 1: user should be unchanged
	if out[1].Role != "user" || out[1].Content != "u1" {
		t.Errorf("out[1] = {%q, %q}, want {user, u1}", out[1].Role, out[1].Content)
	}
	// Position 2: assistant should be unchanged
	if out[2].Role != "assistant" || out[2].Content != "a1" {
		t.Errorf("out[2] = {%q, %q}, want {assistant, a1}", out[2].Role, out[2].Content)
	}
	// Position 3: system message mid-conversation should be demoted to user with prefix
	if out[3].Role != "user" || out[3].Content != "[harness] nudge" {
		t.Errorf("out[3] = {%q, %q}, want {user, [harness] nudge}", out[3].Role, out[3].Content)
	}
	// Position 4: user should be unchanged
	if out[4].Role != "user" || out[4].Content != "u2" {
		t.Errorf("out[4] = {%q, %q}, want {user, u2}", out[4].Role, out[4].Content)
	}
}

// TestSerializeMessages_KeepsPositionZeroSystem verifies that a system message
// at position 0 is not demoted.
func TestSerializeMessages_KeepsPositionZeroSystem(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u"},
	}
	out := serializeMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[0].Role != "system" || out[0].Content != "sys" {
		t.Errorf("out[0] = {%q, %q}, want {system, sys}", out[0].Role, out[0].Content)
	}
	if out[1].Role != "user" || out[1].Content != "u" {
		t.Errorf("out[1] = {%q, %q}, want {user, u}", out[1].Role, out[1].Content)
	}
}

// TestSerializeMessages_DemotesMultipleSystemMessages verifies that when multiple
// system messages exist, only the first is kept at position 0; others are demoted.
func TestSerializeMessages_DemotesMultipleSystemMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "a"},
		{Role: "system", Content: "b"},
		{Role: "user", Content: "u"},
	}
	out := serializeMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	// Position 0: first system message is kept
	if out[0].Role != "system" || out[0].Content != "a" {
		t.Errorf("out[0] = {%q, %q}, want {system, a}", out[0].Role, out[0].Content)
	}
	// Position 1: second system message is demoted
	if out[1].Role != "user" || out[1].Content != "[harness] b" {
		t.Errorf("out[1] = {%q, %q}, want {user, [harness] b}", out[1].Role, out[1].Content)
	}
	// Position 2: user message is unchanged
	if out[2].Role != "user" || out[2].Content != "u" {
		t.Errorf("out[2] = {%q, %q}, want {user, u}", out[2].Role, out[2].Content)
	}
}

// TestSerializeMessages_NoSystemAtZero_AllSystemDemoted verifies that if no system
// message appears at position 0, all system messages are demoted.
func TestSerializeMessages_NoSystemAtZero_AllSystemDemoted(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "u"},
		{Role: "system", Content: "x"},
		{Role: "assistant", Content: "a"},
	}
	out := serializeMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	// Position 0: user should be unchanged
	if out[0].Role != "user" || out[0].Content != "u" {
		t.Errorf("out[0] = {%q, %q}, want {user, u}", out[0].Role, out[0].Content)
	}
	// Position 1: system message (no system at position 0) is demoted
	if out[1].Role != "user" || out[1].Content != "[harness] x" {
		t.Errorf("out[1] = {%q, %q}, want {user, [harness] x}", out[1].Role, out[1].Content)
	}
	// Position 2: assistant should be unchanged
	if out[2].Role != "assistant" || out[2].Content != "a" {
		t.Errorf("out[2] = {%q, %q}, want {assistant, a}", out[2].Role, out[2].Content)
	}
}

// TestSerializeMessages_ArgumentsCoercedToString verifies that a ToolCall whose
// Arguments is a map[string]any (as returned from a DB round-trip) is coerced
// to a JSON-encoded string, and that a pre-string Arguments is left unchanged.
func TestSerializeMessages_ArgumentsCoercedToString(t *testing.T) {
	mapArgs := map[string]any{"action": "all"}
	stringArgs := `{"action":"all"}`

	msgs := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{Type: "function", ID: "call_1", Function: struct {
					Name      string `json:"name"`
					Arguments any    `json:"arguments"`
				}{Name: "config", Arguments: mapArgs}},
				{Type: "function", ID: "call_2", Function: struct {
					Name      string `json:"name"`
					Arguments any    `json:"arguments"`
				}{Name: "config", Arguments: stringArgs}},
			},
		},
	}
	out := serializeMessages(msgs)
	if len(out[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(out[0].ToolCalls))
	}

	// map Arguments must be coerced to a JSON string
	got0, ok := out[0].ToolCalls[0].Function.Arguments.(string)
	if !ok {
		t.Fatalf("call[0].Arguments type = %T, want string", out[0].ToolCalls[0].Function.Arguments)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got0), &decoded); err != nil {
		t.Fatalf("call[0].Arguments is not valid JSON: %v (value: %q)", err, got0)
	}
	if decoded["action"] != "all" {
		t.Errorf("call[0].Arguments decoded action = %v, want all", decoded["action"])
	}

	// string Arguments must be unchanged
	got1, ok := out[0].ToolCalls[1].Function.Arguments.(string)
	if !ok {
		t.Fatalf("call[1].Arguments type = %T, want string", out[0].ToolCalls[1].Function.Arguments)
	}
	if got1 != stringArgs {
		t.Errorf("call[1].Arguments = %q, want %q", got1, stringArgs)
	}
}

// TestAuth_HeaderSent (T11) — Bearer header is present when apiKey is set.
func TestAuth_HeaderSent(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "testkey")
	_ = c.Ping()

	if captured != "Bearer testkey" {
		t.Errorf("Authorization = %q, want %q", captured, "Bearer testkey")
	}
}

// TestAuth_NoHeaderWhenEmpty (T12) — no Authorization header when apiKey is empty.
func TestAuth_NoHeaderWhenEmpty(t *testing.T) {
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_ = c.Ping()

	if captured != "" {
		t.Errorf("Authorization = %q, want empty", captured)
	}
}

// TestNonStreamingChat_HappyPath (T7) — choices[0].message.content decoded.
func TestNonStreamingChat_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hello world"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.Complete(context.Background(), "model", []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "hello world" {
		t.Errorf("content = %q, want %q", got, "hello world")
	}
}

// TestNonStreamingChat_ToolCalls (T8) — arguments JSON string round-trips through normalizeArgs.
func TestNonStreamingChat_ToolCalls(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		// Response with tool_calls (content empty when tool_calls present)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"bash","arguments":"{\"cmd\":\"ls\"}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	// Send a conversation that includes an assistant tool-call message
	msgs := []Message{
		{Role: "user", Content: "list files"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			Type: "function",
			ID:   "call_abc",
			Function: struct {
				Name      string `json:"name"`
				Arguments any    `json:"arguments"`
			}{Name: "bash", Arguments: `{"cmd":"ls"}`},
		}}},
		{Role: "tool", Name: "bash", Content: "file.txt", ToolCallID: "call_abc"},
	}
	content, err := c.Complete(context.Background(), "model", msgs, ChatOptions{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	_ = content // empty when tool_calls present

	// Verify the request body serialized the tool call correctly
	var req struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("parse captured body: %v", err)
	}

	// Verify normalizeArgs handles JSON-string arguments correctly
	tc := ToolCall{}
	tc.Type = "function"
	tc.Function.Name = "bash"
	tc.Function.Arguments = `{"cmd":"ls"}`
	args := tc.Args()
	if args == nil || args["cmd"] != "ls" {
		t.Errorf("normalizeArgs(json-string) = %v, want cmd=ls", args)
	}
}

// TestEmbeddings_HappyPath (T9) — data[0].embedding extracted.
func TestEmbeddings_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q, want /v1/embeddings", r.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Input != "hello" {
			t.Errorf("input = %q, want hello", body.Input)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3],"index":0}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	vec, err := c.Embed(context.Background(), "embed-model", "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("embedding len = %d, want 3", len(vec))
	}
	if vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Errorf("embedding = %v, want [0.1 0.2 0.3]", vec)
	}
}

// TestEmbeddings_EmptyData (T10) — empty data[] returns clean error, no panic.
func TestEmbeddings_EmptyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	vec, err := c.Embed(context.Background(), "embed-model", "text")
	if err == nil {
		t.Error("expected error for empty data[], got nil")
	}
	if vec != nil {
		t.Errorf("vec = %v, want nil on error", vec)
	}
}

// TestErrorEnvelope (T13) — 400 with OpenAI envelope surfaces the message field.
func TestErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad model","type":"invalid_request_error","code":"model_not_found"}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.Complete(context.Background(), "model", []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bad model") {
		t.Errorf("error = %q, want it to contain 'bad model'", err.Error())
	}
}

// TestListModels_OpenAIShape (T14) — data[].id decoded into name list.
func TestListModels_OpenAIShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4","object":"model"},{"id":"gpt-3.5-turbo","object":"model"}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	models, err := c.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models len = %d, want 2", len(models))
	}
	if models[0] != "gpt-4" || models[1] != "gpt-3.5-turbo" {
		t.Errorf("models = %v, want [gpt-4 gpt-3.5-turbo]", models)
	}
}

// TestPing_Health200 (T15) — 200 from /health returns no error.
func TestPing_Health200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %q, want /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if err := c.Ping(); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

// TestStripThinkBlocks verifies the <think>...</think> stripper used in Complete().
func TestStripThinkBlocks(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"no blocks here", "no blocks here"},
		{"<think>reasoning</think>answer", "answer"},
		{"before<think>hidden</think>after", "beforeafter"},
		{"<think>no close", ""},
		{"<think>first</think>middle<think>second</think>end", "middleend"},
	}
	for _, tc := range cases {
		got := stripThinkBlocks(tc.in)
		if got != tc.want {
			t.Errorf("stripThinkBlocks(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestComplete_ThinkBlockStripped verifies that Complete() strips <think> blocks.
func TestComplete_ThinkBlockStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"<think>reasoning here</think>actual answer"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.Complete(context.Background(), "model", []Message{{Role: "user", Content: "q"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "actual answer" {
		t.Errorf("content = %q, want %q", got, "actual answer")
	}
}

// TestChatRequest_DisableThinking — opts.DisableThinking=true → chat_template_kwargs in both
// Complete and StreamOnce request bodies.
func TestChatRequest_DisableThinking(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		streaming := streaming
		t.Run(fmt.Sprintf("streaming=%v", streaming), func(t *testing.T) {
			var capturedBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedBody, _ = io.ReadAll(r.Body)
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w,
						"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"+
							"data: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`)
				}
			}))
			defer srv.Close()

			c := New(srv.URL, "")
			opts := ChatOptions{DisableThinking: true}
			msgs := []Message{{Role: "user", Content: "q"}}
			if streaming {
				_, _, _, _ = c.StreamOnce(context.Background(), "model", msgs, nil, opts, ChatCallbacks{})
			} else {
				_, _ = c.Complete(context.Background(), "model", msgs, opts)
			}

			var body map[string]any
			if err := json.Unmarshal(capturedBody, &body); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			ktw, ok := body["chat_template_kwargs"].(map[string]any)
			if !ok {
				t.Fatalf("chat_template_kwargs missing or wrong type; body keys: %v", body)
			}
			if ktw["enable_thinking"] != false {
				t.Errorf("enable_thinking = %v, want false", ktw["enable_thinking"])
			}
		})
	}
}

// TestChatRequest_DefaultThinking — opts.DisableThinking=false → no chat_template_kwargs in body.
func TestChatRequest_DefaultThinking(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		streaming := streaming
		t.Run(fmt.Sprintf("streaming=%v", streaming), func(t *testing.T) {
			var capturedBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedBody, _ = io.ReadAll(r.Body)
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w,
						"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"+
							"data: [DONE]\n\n")
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`)
				}
			}))
			defer srv.Close()

			c := New(srv.URL, "")
			opts := ChatOptions{DisableThinking: false}
			msgs := []Message{{Role: "user", Content: "q"}}
			if streaming {
				_, _, _, _ = c.StreamOnce(context.Background(), "model", msgs, nil, opts, ChatCallbacks{})
			} else {
				_, _ = c.Complete(context.Background(), "model", msgs, opts)
			}

			var body map[string]any
			if err := json.Unmarshal(capturedBody, &body); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			if _, exists := body["chat_template_kwargs"]; exists {
				t.Errorf("chat_template_kwargs should be absent when DisableThinking=false, got: %v", body["chat_template_kwargs"])
			}
		})
	}
}

// TestSSE_ReasoningContent — server-side reasoning_content delta fires cb.Thinking exactly once;
// content field is unaffected.
func TestSSE_ReasoningContent(t *testing.T) {
	body := sseBody(
		`{"choices":[{"delta":{"reasoning_content":"step 1..."},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"reasoning_content":"step 2..."},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":"answer"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	srv := sseServer(t, body)
	defer srv.Close()

	var thinkCalls int
	c := New(srv.URL, "")
	text, _, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "q"}}, nil, ChatOptions{},
		ChatCallbacks{
			Thinking: func(s string) { thinkCalls++ },
		})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if thinkCalls != 1 {
		t.Errorf("cb.Thinking calls = %d, want exactly 1", thinkCalls)
	}
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
}

// sseBody builds a complete SSE response body from a list of event payloads.
// Each payload is wrapped in "data: <payload>\n\n". [DONE] is appended automatically.
func sseBody(payloads ...string) string {
	var b strings.Builder
	for _, p := range payloads {
		b.WriteString("data: ")
		b.WriteString(p)
		b.WriteString("\n\n")
	}
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
}

// TestSSE_HappyPath (T1) — 4-event stream produces correct assembled text.
func TestSSE_HappyPath(t *testing.T) {
	body := sseBody(
		`{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	srv := sseServer(t, body)
	defer srv.Close()

	var calls int
	c := New(srv.URL, "")
	text, tcs, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{},
		ChatCallbacks{Content: func(s string) { calls++ }})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if text != "Hello world" {
		t.Errorf("text = %q, want %q", text, "Hello world")
	}
	if len(tcs) != 0 {
		t.Errorf("tool calls = %d, want 0", len(tcs))
	}
	if calls < 1 {
		t.Errorf("cb.Content called %d times, want ≥1", calls)
	}
}

// TestSSE_DataNoSpace (T2) — "data:{...}" (no space) parses identically.
func TestSSE_DataNoSpace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w,
			"data:{\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	text, _, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "q"}}, nil, ChatOptions{}, ChatCallbacks{})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if text != "hi" {
		t.Errorf("text = %q, want %q", text, "hi")
	}
}

// TestSSE_FragmentedToolCall (T3) — one tool call across 4 deltas reassembles correctly.
func TestSSE_FragmentedToolCall(t *testing.T) {
	body := sseBody(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	srv := sseServer(t, body)
	defer srv.Close()

	c := New(srv.URL, "")
	_, tcs, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{}, ChatCallbacks{})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if len(tcs) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(tcs))
	}
	if tcs[0].Function.Name != "bash" {
		t.Errorf("name = %q, want bash", tcs[0].Function.Name)
	}
	if tcs[0].Type != "function" {
		t.Errorf("type = %q, want function", tcs[0].Type)
	}
	args := tcs[0].Args()
	if args == nil || args["cmd"] != "ls" {
		t.Errorf("args = %v, want cmd=ls", args)
	}
}

// TestSSE_ParallelToolCalls (T4) — two tool calls interleaved by index both reconstruct.
func TestSSE_ParallelToolCalls(t *testing.T) {
	body := sseBody(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read","arguments":""}},{"index":1,"id":"call_b","type":"function","function":{"name":"write","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a\"}"}},{"index":1,"function":{"arguments":"{\"path\":\"b\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	srv := sseServer(t, body)
	defer srv.Close()

	c := New(srv.URL, "")
	_, tcs, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{}, ChatCallbacks{})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if len(tcs) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(tcs))
	}
	if tcs[0].Function.Name != "read" || tcs[1].Function.Name != "write" {
		t.Errorf("names = %q %q, want read write", tcs[0].Function.Name, tcs[1].Function.Name)
	}
	if tcs[0].Type != "function" || tcs[1].Type != "function" {
		t.Errorf("types = %q %q, want function function", tcs[0].Type, tcs[1].Type)
	}
	if a0 := tcs[0].Args(); a0 == nil || a0["path"] != "a" {
		t.Errorf("tc[0].args = %v, want path=a", a0)
	}
	if a1 := tcs[1].Args(); a1 == nil || a1["path"] != "b" {
		t.Errorf("tc[1].args = %v, want path=b", a1)
	}
}

// TestSSE_ServerCutsBeforeDone (T5) — no [DONE] → error returned, partial text preserved.
func TestSSE_ServerCutsBeforeDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
		// Close without [DONE]
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	text, _, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{}, ChatCallbacks{})
	if err == nil {
		t.Fatal("expected error for missing [DONE], got nil")
	}
	if !strings.Contains(err.Error(), "stream ended without completion marker") {
		t.Errorf("err = %q, want 'stream ended without completion marker'", err.Error())
	}
	if text != "partial" {
		t.Errorf("text = %q, want %q", text, "partial")
	}
}

// TestSSE_ContextCancellation (T6) — cancel mid-stream → ctx.Err() returned, partial preserved.
func TestSSE_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // hold connection open until client cancels
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New(srv.URL, "")

	// contentReceived is closed when cb.Content is first called, ensuring the
	// partial text is in textBuf before we cancel.
	contentReceived := make(chan struct{})
	var gotText string
	var gotErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		gotText, _, _, gotErr = c.StreamOnce(ctx, "model",
			[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{},
			ChatCallbacks{Content: func(s string) {
				select {
				case <-contentReceived:
				default:
					close(contentReceived)
				}
			}})
	}()

	<-contentReceived // wait until partial content is processed
	cancel()
	<-done

	if !errors.Is(gotErr, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", gotErr)
	}
	if gotText != "hello" {
		t.Errorf("text = %q, want hello", gotText)
	}
}

// TestThinkSplit_AcrossDeltas (T16) — "<thi" / "nk>...</think>" → cb.Thinking once, no tag in content.
func TestThinkSplit_AcrossDeltas(t *testing.T) {
	body := sseBody(
		`{"choices":[{"delta":{"content":"before<thi"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":"nk>thinking</think>after"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	srv := sseServer(t, body)
	defer srv.Close()

	var thinkCalls int
	var content strings.Builder
	c := New(srv.URL, "")
	text, _, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{},
		ChatCallbacks{
			Content:  func(s string) { content.WriteString(s) },
			Thinking: func(s string) { thinkCalls++ },
		})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if thinkCalls != 1 {
		t.Errorf("cb.Thinking calls = %d, want 1", thinkCalls)
	}
	if strings.Contains(text, "<") || strings.Contains(text, "think") {
		t.Errorf("text contains tag bytes: %q", text)
	}
	if text != "beforeafter" {
		t.Errorf("text = %q, want %q", text, "beforeafter")
	}
}

// TestThinkSplit_MultipleBlocks (T17) — two <think> blocks → cb.Thinking fires twice.
func TestThinkSplit_MultipleBlocks(t *testing.T) {
	body := sseBody(
		`{"choices":[{"delta":{"content":"a<think>first</think>b<think>second</think>c"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	srv := sseServer(t, body)
	defer srv.Close()

	var thinkCalls int
	c := New(srv.URL, "")
	text, _, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{},
		ChatCallbacks{Thinking: func(s string) { thinkCalls++ }})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if thinkCalls != 2 {
		t.Errorf("cb.Thinking calls = %d, want 2", thinkCalls)
	}
	if text != "abc" {
		t.Errorf("text = %q, want abc", text)
	}
}

// TestThinkSplit_OpenWithoutClose (T18) — stream ends mid-thinking → no crash, no tag bytes in content.
func TestThinkSplit_OpenWithoutClose(t *testing.T) {
	body := sseBody(
		`{"choices":[{"delta":{"content":"text<think>thinking forever"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	)
	srv := sseServer(t, body)
	defer srv.Close()

	c := New(srv.URL, "")
	text, _, _, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{}, ChatCallbacks{})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if strings.Contains(text, "<") || strings.Contains(text, "think") {
		t.Errorf("text contains tag bytes: %q", text)
	}
	if text != "text" {
		t.Errorf("text = %q, want text", text)
	}
}

// TestThinkSplit_BudgetExceeded (T22) — think content > ThinkBudget → budgetExceeded=true, no panic.
func TestThinkSplit_BudgetExceeded(t *testing.T) {
	longThink := strings.Repeat("x", 20)
	chunk := `{"choices":[{"delta":{"content":"before<think>` + longThink + `</think>after"},"finish_reason":null}]}`
	body := sseBody(chunk, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	srv := sseServer(t, body)
	defer srv.Close()

	c := New(srv.URL, "")
	text, _, exceeded, err := c.StreamOnce(context.Background(), "model",
		[]Message{{Role: "user", Content: "hi"}}, nil, ChatOptions{ThinkBudget: 5}, ChatCallbacks{})
	if err != nil {
		t.Fatalf("StreamOnce: %v", err)
	}
	if !exceeded {
		t.Error("budgetExceeded = false, want true")
	}
	if text != "beforeafter" {
		t.Errorf("text = %q, want beforeafter", text)
	}
}
