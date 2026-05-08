package server

import (
	"strings"
	"testing"
)

func TestSanitizeHostname(t *testing.T) {
	cases := []struct{ in, want string }{
		{"MacBook-Pro", "macbook-pro"},
		{"My Host", "my-host"},
		{"myHost_123", "myhost-123"},
		{"-leading", "leading"},
		{"trailing-", "trailing"},
		{"UPPERCASE", "uppercase"},
		{strings.Repeat("a", 70), strings.Repeat("a", 63)},
		{"", "node"},
		{"---", "node"},
	}
	for _, c := range cases {
		if got := sanitizeHostname(c.in); got != c.want {
			t.Errorf("sanitizeHostname(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
