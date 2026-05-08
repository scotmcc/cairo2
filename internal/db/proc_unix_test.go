//go:build !windows

package db

import (
	"testing"
)

func TestParseStartToken(t *testing.T) {
	// Each test input has 20 fields after stripping "pid (comm) " so fields[19]
	// lands on the starttime value (field 22 per man 5 proc, 1-indexed).
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{
			// Normal comm, 20 trailing fields.
			"1234 (cairo) S 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 999999\n",
			"999999", false,
		},
		{
			// Comm with spaces — LastIndex still finds the correct closing paren.
			"5 (my prog with spaces) S 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 12345\n",
			"12345", false,
		},
		{
			// Comm containing parens — LastIndex skips the inner one.
			"7 (cairo (test)) S 0 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 54321\n",
			"54321", false,
		},
		{
			// No closing paren at all.
			"no closing paren here",
			"", true,
		},
		{
			// Too few fields after stripping comm (only "S", need 20).
			"1 (x) S",
			"", true,
		},
	}

	for _, c := range cases {
		got, err := parseStartToken(c.input)
		if (err != nil) != c.wantErr {
			t.Errorf("input %q: err=%v, wantErr=%v", c.input, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("input %q: got %q, want %q", c.input, got, c.want)
		}
	}
}
