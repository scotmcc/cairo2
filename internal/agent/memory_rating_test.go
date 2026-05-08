package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/scotmcc/cairo2/internal/llm"
)

// stubLLMServer returns an httptest server that responds to all completion
// requests with the given content string.
func stubLLMServer(t *testing.T, content string) (*llm.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": content}, "finish_reason": "stop"},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, string(body))
	}))
	t.Cleanup(srv.Close)
	return llm.New(srv.URL, ""), srv
}

func TestRateImportance_ValidScore(t *testing.T) {
	client, _ := stubLLMServer(t, "This memory is specific and actionable.\nSCORE: 4")
	got, err := RateImportance(context.Background(), client, "test-model", "", "User prefers small focused Go files (~100 lines)")
	if err != nil {
		t.Fatalf("RateImportance: %v", err)
	}
	if got != 0.8 { // 4/5 = 0.8
		t.Errorf("score: want 0.8 (4/5), got %v", got)
	}
}

func TestRateImportance_ScoreOne(t *testing.T) {
	client, _ := stubLLMServer(t, "Too vague.\nSCORE: 1")
	got, err := RateImportance(context.Background(), client, "test-model", "", "The session was productive.")
	if err != nil {
		t.Fatalf("RateImportance: %v", err)
	}
	if got != 0.2 { // 1/5 = 0.2
		t.Errorf("score: want 0.2 (1/5), got %v", got)
	}
}

func TestRateImportance_NoScoreLine(t *testing.T) {
	client, _ := stubLLMServer(t, "This memory is not very useful, no score provided.")
	_, err := RateImportance(context.Background(), client, "test-model", "", "some memory")
	if err == nil {
		t.Fatal("want error when response has no SCORE: line, got nil")
	}
}

func TestParseScore_Valid(t *testing.T) {
	cases := []struct {
		input string
		want  float64
	}{
		{"Some justification.\nSCORE: 4", 0.8},
		{"SCORE: 1", 0.2},
		{"SCORE: 5", 1.0},
		{"SCORE: 3 extra tokens after score", 0.6},
		{"line one\nline two\nSCORE: 2\nline four", 0.4},
	}
	for _, tc := range cases {
		got, err := parseScore(tc.input)
		if err != nil {
			t.Errorf("parseScore(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseScore(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestParseScore_Invalid(t *testing.T) {
	cases := []string{
		"no score here at all",
		"SCORE: 6",
		"SCORE: 0",
		"SCORE: -1",
		"SCORE: abc",
		"SCORE:",
	}
	for _, input := range cases {
		_, err := parseScore(input)
		if err == nil {
			t.Errorf("parseScore(%q): want error, got nil", input)
		}
	}
}
