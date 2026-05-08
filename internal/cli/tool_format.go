package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// formatToolArgs renders a compact JSON view of tool args, truncated to max
// characters (rune-safe) with a trailing ellipsis. Returns "" for nil/empty.
func formatToolArgs(args map[string]any, max int) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	var s string
	if err != nil {
		s = fmt.Sprintf("%v", args)
	} else {
		s = string(b)
	}
	return truncateRunes(s, max)
}

// formatToolPreview strips leading/trailing whitespace, collapses runs of
// whitespace (including newlines) to a single space, and truncates to max
// characters (rune-safe) with a trailing ellipsis.
func formatToolPreview(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	return truncateRunes(b.String(), max)
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
