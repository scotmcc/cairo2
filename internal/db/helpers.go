package db

import (
	"strings"
	"time"
)

// buildPlaceholders returns n comma-separated SQL placeholders ("?,?,?").
func buildPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// int64SliceToAny converts []int64 to []any for use as variadic sql args.
func int64SliceToAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// parseTimestamp parses a TIMESTAMP string returned by modernc.org/sqlite.
// The driver may return either ISO 8601 ("2006-01-02T15:04:05Z") or
// SQLite's canonical space-separated format ("2006-01-02 15:04:05").
// Returns nil if s is empty or unparseable.
func parseTimestamp(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
