package tui

import "testing"

// TestFormatTokens verifies the status-bar token formatter.
// Spec: show raw digits below 1000; show "N.Nk" with one decimal above.
func TestFormatTokens(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0k"},
		{1200, "1.2k"},
		{1234, "1.2k"},
		{9999, "10.0k"},
		{10000, "10.0k"},
		{12345, "12.3k"},
		{99999, "100.0k"},
		{-1, "0"},
	}
	for _, c := range cases {
		got := formatTokens(c.n)
		if got != c.want {
			t.Errorf("formatTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
