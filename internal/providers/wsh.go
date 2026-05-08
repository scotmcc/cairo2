package providers

import "os"

// WshProvider detects Wave Terminal by checking environment variables set by
// the terminal itself, not just PATH presence. wsh may be installed globally
// without the user being inside a WaveTerm session.
type WshProvider struct {
	hint string // cached at construction, "" if not in a WaveTerm session
}

// NewWshProvider constructs a WshProvider. Detection runs once here.
func NewWshProvider() *WshProvider {
	p := &WshProvider{}
	if os.Getenv("TERM_PROGRAM") == "WaveTerm" || os.Getenv("WAVETERM") != "" {
		p.hint = "You are running within Wave Terminal (wsh). Use `wsh view <file>`, `wsh edit <file>`, or `wsh browser <url>` to collaborate. Run `wsh -h` to learn more about available commands.\n"
	}
	return p
}

func (p *WshProvider) Name() string { return "wsh" }

// Context returns the cached wsh hint, or "" if not in a WaveTerm session.
func (p *WshProvider) Context(_ string) string { return p.hint }
