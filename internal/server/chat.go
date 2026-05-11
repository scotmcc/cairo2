package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// chatRequest is the JSON body for POST /api/chat.
type chatRequest struct {
	Message  string        `json:"message"`
	Context  []chatContext `json:"context"`
	Stream   bool          `json:"stream"`
	Messages []chatMessage `json:"messages"`
}

// chatMessage mirrors the OpenAI messages[] format.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatContext carries ambient context attached to a request (URL, document, etc).
type chatContext struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Value   string `json:"value"`
	Content string `json:"content"`
	Source  string `json:"source"`
	Path    string `json:"path"`
}

// chatResponse is the JSON body returned for non-streaming POST /api/chat.
type chatResponse struct {
	Response  string `json:"response"`
	SessionID int64  `json:"session_id"`
	TurnID    int64  `json:"turn_id"`
}

// handleChat handles POST /api/chat.
// Decision Q1 (plan): system messages are dropped entirely — do not inject.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r, "chat.send", "chat"); !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON"})
		return
	}

	text := extractMessage(req)
	if text == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no message"})
		return
	}

	text += formatContext(req.Context)

	forceConsider := false
	if strings.HasPrefix(text, "/c ") {
		text = strings.TrimPrefix(text, "/c ")
		forceConsider = true
	}

	if req.Stream {
		s.handleChatStreamWithOpts(w, r, text, forceConsider)
		return
	}

	resp, turnID, err := s.bridge.SendWithOpts(r.Context(), text, forceConsider, "api")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	var sessionID int64
	if sess := s.agent.Session(); sess != nil {
		sessionID = sess.ID
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatResponse{
		Response:  resp,
		SessionID: sessionID,
		TurnID:    turnID,
	})
}

// handleChatStreamWithOpts is the SSE streaming path with explicit consider opts.
func (s *Server) handleChatStreamWithOpts(w http.ResponseWriter, r *http.Request, text string, forceConsider bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	tokens := make(chan string, 64)
	errCh := make(chan error, 1)

	go func() {
		_, _, err := s.bridge.SendStreamWithOpts(r.Context(), text, tokens, forceConsider, "api")
		errCh <- err
	}()

	for tok := range tokens {
		data, _ := json.Marshal(map[string]string{"token": tok})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	<-errCh
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// extractMessage picks the user-facing text from a chatRequest.
// Uses req.Message if set; otherwise scans req.Messages from the end for the
// last role=="user" entry. System messages are skipped entirely.
func extractMessage(req chatRequest) string {
	if req.Message != "" {
		return req.Message
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role == "user" {
			return m.Content
		}
	}
	return ""
}

// formatContext formats context items into annotated text appended to the message.
func formatContext(items []chatContext) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range items {
		switch c.Type {
		case "url":
			fmt.Fprintf(&b, "\n\n[URL] %s — %s", c.Title, c.Value)
		case "document":
			content := c.Content
			if len(content) > 4000 {
				content = content[:4000]
			}
			fmt.Fprintf(&b, "\n\n[DOCUMENT] %s\n%s", c.Title, content)
		case "selection":
			fmt.Fprintf(&b, "\n\n[SELECTION from %s]\n%s", c.Source, c.Content)
		case "file":
			if c.Content != "" {
				fmt.Fprintf(&b, "\n\n[FILE] %s\n%s", c.Path, c.Content)
			} else {
				fmt.Fprintf(&b, "\n\n[FILE] %s", c.Path)
			}
		}
	}
	return b.String()
}
