package providers

import "os"

// VsCodeProvider detects VS Code's integrated terminal by checking the
// TERM_PROGRAM environment variable, not just PATH presence. The `code` CLI
// may be installed globally without the user being inside a VS Code terminal.
type VsCodeProvider struct {
	hint string // cached at construction, "" if not in a VS Code session
}

// NewVsCodeProvider constructs a VsCodeProvider. Detection runs once here.
func NewVsCodeProvider() *VsCodeProvider {
	p := &VsCodeProvider{}
	if os.Getenv("TERM_PROGRAM") == "vscode" {
		p.hint = "You are running within VS Code. Use `code -r <file>` to open files, `code <dir>` to open folders. Run `code --help` to learn more.\n"
	}
	return p
}

func (p *VsCodeProvider) Name() string { return "vscode" }

// Context returns the cached VS Code hint, or "" if not in a VS Code terminal.
func (p *VsCodeProvider) Context(_ string) string { return p.hint }
