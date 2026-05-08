package tools

import (
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/db"
)

// TestSay_LoudFailWhenKokoroURLMissing asserts that the say tool returns an
// IsError result with a discoverable remediation message when kokoro_url is
// unset, instead of silently no-op'ing. Regression guard against the silent
// failure mode where the model thought it spoke but the user heard nothing.
func TestSay_LoudFailWhenKokoroURLMissing(t *testing.T) {
	d := openTestDB(t)

	// Sanity: kokoro_url should be unset on a fresh DB.
	if v, _ := d.Config.Get(db.KeyKokoroURL); v != "" {
		t.Fatalf("expected kokoro_url empty on fresh DB, got %q", v)
	}

	tool := Say(d)
	res := tool.Execute(map[string]any{"text": "hello"}, nil)

	if !res.IsError {
		t.Errorf("expected IsError=true when kokoro_url is unset, got false (silent no-op regression)")
	}
	if !strings.Contains(res.Content, "kokoro_url") {
		t.Errorf("expected error content to mention kokoro_url, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "/config set") {
		t.Errorf("expected error content to include remediation `/config set`, got %q", res.Content)
	}
}

// TestSay_AcceptsConfiguredURL asserts that with kokoro_url set, the tool
// returns a non-error result (it spawns the playback goroutine and returns
// immediately). We do not test actual TTS playback.
func TestSay_AcceptsConfiguredURL(t *testing.T) {
	d := openTestDB(t)

	if err := d.Config.Set(db.KeyKokoroURL, "http://127.0.0.1:1"); err != nil {
		t.Fatalf("set kokoro_url: %v", err)
	}

	tool := Say(d)
	res := tool.Execute(map[string]any{"text": "hello"}, nil)

	if res.IsError {
		t.Errorf("expected non-error result with kokoro_url set, got IsError=true: %q", res.Content)
	}
	if !strings.Contains(res.Content, "speaking") {
		t.Errorf("expected content to indicate speaking, got %q", res.Content)
	}
}
