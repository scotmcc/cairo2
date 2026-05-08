package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// openAIModel is the model object returned by GET /v1/models.
type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// openAIModelList is the response body for GET /v1/models.
type openAIModelList struct {
	Object string        `json:"object"`
	Data   []openAIModel `json:"data"`
}

// openAIMessage is a single message in an OpenAI chat completion request/response.
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAICompletionRequest is the body for POST /v1/chat/completions.
type openAICompletionRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

// openAIChoice is a single choice in a completion response.
type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// openAIUsage holds token-count fields (always zero — Ollama does not expose these).
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// openAICompletionResponse is the non-streaming response body.
type openAICompletionResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

// openAIDelta is the delta in a streaming chunk.
type openAIDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// openAIStreamChoice is a single choice in a streaming chunk.
type openAIStreamChoice struct {
	Index        int         `json:"index"`
	Delta        openAIDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// openAIStreamChunk is one SSE data payload in a streaming response.
type openAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
}

// handleModels handles GET /v1/models.
// Returns the single "cairo" model entry required by OpenAI clients at startup.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	list := openAIModelList{
		Object: "list",
		Data: []openAIModel{
			{
				ID:      ModelID,
				Object:  "model",
				Created: 1714000000,
				OwnedBy: "cairo",
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// handleCompletions handles POST /v1/chat/completions.
// Extracts the last user message, runs the full agent loop, and returns an
// OpenAI-shaped response. System messages are dropped per the design doc.
func (s *Server) handleCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req openAICompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Extract last user message; skip system messages entirely.
	text := extractOpenAIMessage(req.Messages)
	if text == "" {
		writeJSONError(w, http.StatusBadRequest, "no user message found")
		return
	}

	if req.Stream {
		s.handleCompletionsStream(w, r, text)
		return
	}

	resp, turnID, err := s.bridge.Send(r.Context(), text)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	turnStr := fmt.Sprintf("cairo-turn-%d", turnID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(openAICompletionResponse{
		ID:      turnStr,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   ModelID,
		Choices: []openAIChoice{
			{
				Index:        0,
				Message:      openAIMessage{Role: "assistant", Content: resp},
				FinishReason: "stop",
			},
		},
		Usage: openAIUsage{},
	})
}

// handleCompletionsStream handles SSE streaming for POST /v1/chat/completions.
func (s *Server) handleCompletionsStream(w http.ResponseWriter, r *http.Request, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	turnID := fmt.Sprintf("cairo-turn-stream-%d", time.Now().UnixMilli())
	now := time.Now().Unix()

	// Send an initial chunk with the role delta.
	roleChunk := openAIStreamChunk{
		ID:      turnID,
		Object:  "chat.completion.chunk",
		Created: now,
		Model:   ModelID,
		Choices: []openAIStreamChoice{
			{Index: 0, Delta: openAIDelta{Role: "assistant"}, FinishReason: nil},
		},
	}
	sendSSEChunk(w, flusher, roleChunk)

	tokens := make(chan string, 64)
	errCh := make(chan error, 1)

	go func() {
		_, _, err := s.bridge.SendStream(r.Context(), text, tokens)
		errCh <- err
	}()

	for tok := range tokens {
		chunk := openAIStreamChunk{
			ID:      turnID,
			Object:  "chat.completion.chunk",
			Created: now,
			Model:   ModelID,
			Choices: []openAIStreamChoice{
				{Index: 0, Delta: openAIDelta{Content: tok}, FinishReason: nil},
			},
		}
		sendSSEChunk(w, flusher, chunk)
	}

	<-errCh

	// Send the final chunk with finish_reason=stop.
	stopReason := "stop"
	finalChunk := openAIStreamChunk{
		ID:      turnID,
		Object:  "chat.completion.chunk",
		Created: now,
		Model:   ModelID,
		Choices: []openAIStreamChoice{
			{Index: 0, Delta: openAIDelta{}, FinishReason: &stopReason},
		},
	}
	sendSSEChunk(w, flusher, finalChunk)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// extractOpenAIMessage returns the content of the last user-role message.
// System messages are skipped entirely per the design doc.
func extractOpenAIMessage(msgs []openAIMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

// sendSSEChunk marshals v and writes it as a single SSE data line.
func sendSSEChunk(w http.ResponseWriter, f http.Flusher, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}

// writeJSONError writes a JSON error body with the given HTTP status code.
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
