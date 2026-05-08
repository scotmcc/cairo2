package jobs

import "strings"

type scanner interface {
	Scan(dest ...any) error
}

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
