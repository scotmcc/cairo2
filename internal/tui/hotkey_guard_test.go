package tui

// hotkey_guard_test.go — unit tests for the validateToggleKey startup guard.
// The guard enforces that all registered panel toggle keys are ctrl+-prefixed,
// or one of the explicit exceptions (/, @, ?). See panels.go for rationale.

import (
	"strings"
	"testing"
)

func expectPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("%s: expected panic but got none", name)
		}
	}()
	fn()
}

func expectNoPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: unexpected panic: %v", name, r)
		}
	}()
	fn()
}

func TestValidateToggleKey_Accepted(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"ctrl+l", "ctrl+l"},
		{"ctrl+enter", "ctrl+enter"},
		{"ctrl+shift+x", "ctrl+shift+x"},
		{"ctrl+d", "ctrl+d"},
		{"ctrl+k upper", "CTRL+K"}, // case-insensitive
		{"slash exception", "/"},
		{"at exception", "@"},
		{"question-mark exception", "?"},
		{"empty key (no-op)", ""},
	}
	for _, tc := range cases {
		tc := tc
		expectNoPanic(t, tc.name, func() {
			validateToggleKey(tc.key, panelID("test-panel"))
		})
	}
}

func TestValidateToggleKey_Rejected(t *testing.T) {
	cases := []struct {
		name        string
		key         string
		wantInPanic string
	}{
		{"bare g", "g", "g"},
		{"bare G", "G", "G"},
		{"bare q", "q", "q"},
		{"bare letter z", "z", "z"},
		{"bare digit 1", "1", "1"},
		{"bare esc word", "esc", "esc"},
		{"empty-ish space", " ", " "},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Errorf("key %q: expected panic but got none", tc.key)
					return
				}
				msg, ok := r.(string)
				if !ok {
					t.Errorf("key %q: panic value is not a string: %v", tc.key, r)
					return
				}
				if !strings.Contains(msg, tc.wantInPanic) {
					t.Errorf("key %q: panic message %q does not contain %q", tc.key, msg, tc.wantInPanic)
				}
				// The message should also suggest ctrl+
				if !strings.Contains(msg, "ctrl+") {
					t.Errorf("key %q: panic message %q does not mention ctrl+", tc.key, msg)
				}
			}()
			validateToggleKey(tc.key, panelID("test-panel"))
		})
	}
}
