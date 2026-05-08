package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newChatRequest(t *testing.T, body any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// TestChat_SimpleMessage verifies a basic non-streaming chat round-trip.
func TestChat_SimpleMessage(t *testing.T) {
	fa := newFakeAgent("world", nil)
	bridge := NewBridge(fa)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge.Start(ctx)
	srv := New(fa, openTestDB(t), bridge, Options{})

	req := newChatRequest(t, map[string]any{"message": "hello"})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp chatResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Response != "world" {
		t.Errorf("expected %q, got %q", "world", resp.Response)
	}
}

// TestChat_MessagesArray_LastUserExtracted verifies that the last user message
// is used when the messages[] array is provided.
func TestChat_MessagesArray_LastUserExtracted(t *testing.T) {
	fa := newFakeAgent("from-messages", nil)
	bridge := NewBridge(fa)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge.Start(ctx)
	srv := New(fa, openTestDB(t), bridge, Options{})

	req := newChatRequest(t, map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "first user"},
			{"role": "assistant", "content": "first assistant"},
			{"role": "user", "content": "last user message"},
		},
	})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp chatResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Response != "from-messages" {
		t.Errorf("expected %q, got %q", "from-messages", resp.Response)
	}
}

// TestChat_SystemMessagesDropped verifies system messages don't become the user text.
func TestChat_SystemMessagesDropped(t *testing.T) {
	fa := newFakeAgent("ok", nil)
	bridge := NewBridge(fa)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge.Start(ctx)
	srv := New(fa, openTestDB(t), bridge, Options{})

	// Only system messages, no user — should return 400 (no message found).
	req := newChatRequest(t, map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": "do not use this"},
		},
	})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when only system messages present, got %d", rr.Code)
	}
}

// TestChat_ContextURL_Formatted verifies URL context is appended to the message.
func TestChat_ContextURL_Formatted(t *testing.T) {
	req := chatRequest{
		Message: "look at this",
		Context: []chatContext{
			{Type: "url", Title: "Go Blog", Value: "https://go.dev/blog"},
		},
	}
	text := extractMessage(req)
	text += formatContext(req.Context)

	if !strings.Contains(text, "[URL] Go Blog — https://go.dev/blog") {
		t.Errorf("URL context not formatted correctly in: %q", text)
	}
	if !strings.HasPrefix(text, "look at this") {
		t.Errorf("message prefix missing in: %q", text)
	}
}

// TestChat_ContextDocument_Truncated verifies documents over 4000 chars are truncated.
func TestChat_ContextDocument_Truncated(t *testing.T) {
	longContent := strings.Repeat("x", 5000)
	req := chatRequest{
		Message: "doc",
		Context: []chatContext{
			{Type: "document", Title: "Big Doc", Content: longContent},
		},
	}
	text := extractMessage(req)
	text += formatContext(req.Context)

	if strings.Contains(text, strings.Repeat("x", 4001)) {
		t.Error("document content was not truncated to 4000 chars")
	}
	if !strings.Contains(text, "[DOCUMENT] Big Doc") {
		t.Error("[DOCUMENT] header missing")
	}
}

// TestChat_ContextSelection_Formatted verifies selection context format.
func TestChat_ContextSelection_Formatted(t *testing.T) {
	req := chatRequest{
		Message: "selection",
		Context: []chatContext{
			{Type: "selection", Source: "main.go", Content: "func main() {}"},
		},
	}
	text := extractMessage(req)
	text += formatContext(req.Context)

	if !strings.Contains(text, "[SELECTION from main.go]") {
		t.Errorf("selection context not formatted: %q", text)
	}
	if !strings.Contains(text, "func main() {}") {
		t.Errorf("selection content missing: %q", text)
	}
}

// TestChat_Streaming_TokensThenDone verifies SSE streaming delivers tokens then [DONE].
func TestChat_Streaming_TokensThenDone(t *testing.T) {
	fa := newFakeAgent("ab", []string{"a", "b"})
	bridge := NewBridge(fa)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge.Start(ctx)
	srv := New(fa, openTestDB(t), bridge, Options{})

	req := newChatRequest(t, map[string]any{"message": "stream", "stream": true})
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream content-type, got %q", ct)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("missing [DONE] in SSE response: %q", body)
	}

	// Verify token data lines appear.
	scanner := bufio.NewScanner(strings.NewReader(body))
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			dataLines = append(dataLines, line)
		}
	}
	if len(dataLines) == 0 {
		t.Error("no data lines with token content in SSE stream")
	}
}

// TestChat_ClientDisconnect_CancelsRequest verifies context cancellation propagates.
func TestChat_ClientDisconnect_CancelsRequest(t *testing.T) {
	fa := newFakeAgent("delayed", nil)
	fa.delay = 2 * time.Second
	bridge := NewBridge(fa)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge.Start(ctx)
	srv := New(fa, openTestDB(t), bridge, Options{})

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer reqCancel()

	r := newChatRequest(t, map[string]any{"message": "slow"})
	r = r.WithContext(reqCtx)
	rr := httptest.NewRecorder()

	start := time.Now()
	srv.mux.ServeHTTP(rr, r)
	elapsed := time.Since(start)

	// Handler should return promptly after context cancellation.
	if elapsed > 500*time.Millisecond {
		t.Errorf("handler took too long after client disconnect: %v", elapsed)
	}
}
