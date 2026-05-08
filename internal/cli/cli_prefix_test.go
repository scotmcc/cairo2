package cli

import "testing"

func TestConsiderPrefix(t *testing.T) {
	cases := []struct {
		in        string
		wantText  string
		wantForce bool
	}{
		{"/c hello", "hello", true},
		{"/c should I refactor?", "should I refactor?", true},
		{"hello", "hello", false},
		{"/c", "/c", false},       // missing trailing space
		{"/cabc", "/cabc", false}, // not a prefix match
		{"", "", false},
		{"/c ", "", true}, // edge: prefix with empty body
	}
	for _, tc := range cases {
		gotText, gotForce := parseConsiderPrefix(tc.in)
		if gotText != tc.wantText || gotForce != tc.wantForce {
			t.Errorf("parseConsiderPrefix(%q) = (%q, %v); want (%q, %v)",
				tc.in, gotText, gotForce, tc.wantText, tc.wantForce)
		}
	}
}
