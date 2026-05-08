package tui

// quote_reply_test.go — unit tests for the quote-reply panel helpers.
// Tests the range parser and the blockquote formatter in isolation.

import (
	"testing"
)

// --- parseRange tests ---

func TestParseRange_SingleLine(t *testing.T) {
	start, end, ok := parseRange("5")
	if !ok {
		t.Fatal("expected ok=true for single-line range \"5\"")
	}
	if start != 5 || end != 5 {
		t.Errorf("expected (5,5), got (%d,%d)", start, end)
	}
}

func TestParseRange_MultiLine(t *testing.T) {
	start, end, ok := parseRange("5-12")
	if !ok {
		t.Fatal("expected ok=true for range \"5-12\"")
	}
	if start != 5 || end != 12 {
		t.Errorf("expected (5,12), got (%d,%d)", start, end)
	}
}

func TestParseRange_ReversedRejected(t *testing.T) {
	_, _, ok := parseRange("12-5")
	if ok {
		t.Error("expected ok=false for reversed range \"12-5\"")
	}
}

func TestParseRange_EmptyRejected(t *testing.T) {
	_, _, ok := parseRange("")
	if ok {
		t.Error("expected ok=false for empty range")
	}
}

func TestParseRange_ZeroRejected(t *testing.T) {
	_, _, ok := parseRange("0")
	if ok {
		t.Error("expected ok=false for zero line number")
	}
}

func TestParseRange_NegativeRejected(t *testing.T) {
	_, _, ok := parseRange("-5")
	if ok {
		t.Error("expected ok=false for \"-5\" (no valid start)")
	}
}

func TestParseRange_SameStartEnd(t *testing.T) {
	start, end, ok := parseRange("3-3")
	if !ok {
		t.Fatal("expected ok=true for \"3-3\"")
	}
	if start != 3 || end != 3 {
		t.Errorf("expected (3,3), got (%d,%d)", start, end)
	}
}

// --- quoteFormat tests ---

func TestQuoteFormat_SingleLine(t *testing.T) {
	got := quoteFormat("hello world")
	want := "> hello world\n\n"
	if got != want {
		t.Errorf("quoteFormat single-line: got %q, want %q", got, want)
	}
}

func TestQuoteFormat_MultiLine(t *testing.T) {
	got := quoteFormat("hello\nworld")
	want := "> hello\n> world\n\n"
	if got != want {
		t.Errorf("quoteFormat multi-line: got %q, want %q", got, want)
	}
}

func TestQuoteFormat_EmptyLine(t *testing.T) {
	got := quoteFormat("")
	want := "> \n\n"
	if got != want {
		t.Errorf("quoteFormat empty: got %q, want %q", got, want)
	}
}

// --- quoteResolveRange tests ---

func TestQuoteResolveRange_ValidSingle(t *testing.T) {
	lines := []string{"alpha", "beta", "gamma"}
	text, ok := quoteResolveRange("2", lines)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if text != "beta" {
		t.Errorf("got %q, want %q", text, "beta")
	}
}

func TestQuoteResolveRange_ValidMulti(t *testing.T) {
	lines := []string{"alpha", "beta", "gamma", "delta"}
	text, ok := quoteResolveRange("2-3", lines)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if text != "beta\ngamma" {
		t.Errorf("got %q, want %q", text, "beta\ngamma")
	}
}

func TestQuoteResolveRange_ClampsBeyondEnd(t *testing.T) {
	lines := []string{"alpha", "beta"}
	text, ok := quoteResolveRange("1-99", lines)
	if !ok {
		t.Fatal("expected ok=true (clamped)")
	}
	if text != "alpha\nbeta" {
		t.Errorf("got %q, want %q", text, "alpha\nbeta")
	}
}

func TestQuoteResolveRange_StartBeyondEnd_Rejected(t *testing.T) {
	lines := []string{"alpha"}
	_, ok := quoteResolveRange("5", lines)
	if ok {
		t.Error("expected ok=false when start exceeds line count")
	}
}
