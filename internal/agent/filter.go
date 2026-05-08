package agent

// FilterByAllowlist returns the subset of tools whose names appear in allowed.
// An empty (nil) allowlist means unrestricted — all tools pass through.
// Unknown names in allowed are silently ignored; filtering is intersective.
func FilterByAllowlist(tools []Tool, allowed []string) []Tool {
	if len(allowed) == 0 {
		return tools
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, n := range allowed {
		allow[n] = struct{}{}
	}
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if _, ok := allow[t.Name()]; ok {
			out = append(out, t)
		}
	}
	return out
}
