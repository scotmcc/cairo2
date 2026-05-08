package commands

// registry.go — shared command registry used by both the TUI and CLI.
// Commands operate against a CommandEnv interface so they remain
// environment-agnostic; each frontend supplies its own implementation.

import (
	"sort"
	"strings"
)

// CommandEnv is implemented by both the TUI and CLI. Commands use it
// to interact with their environment without knowing which one they're in.
type CommandEnv interface {
	// Output renders text to the user (TUI: append to transcript; CLI: print).
	Output(text string)
	// Submit sends a message to the agent as if the user typed it.
	Submit(text string)
	// SetPanel opens or closes a named panel (TUI only; CLI ignores).
	SetPanel(name string)
	// IsStreaming reports whether the agent is currently mid-turn.
	IsStreaming() bool
}

// Command is a slash command available in both TUI and CLI.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	HotKey      string // optional; TUI-only display hint
	Handler     func(args string, env CommandEnv) error
}

// Registry holds the registered commands.
type Registry struct {
	cmds []*Command
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Register(cmd *Command) {
	r.cmds = append(r.cmds, cmd)
}

func (r *Registry) All() []*Command { return r.cmds }

// Find returns the command whose Name or Aliases matches the given bare token
// (no leading slash). Returns nil if no match.
func (r *Registry) Find(name string) *Command {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range r.cmds {
		if strings.EqualFold(c.Name, name) {
			return c
		}
		for _, a := range c.Aliases {
			if strings.EqualFold(a, name) {
				return c
			}
		}
	}
	return nil
}

// Filter returns commands matching query. Substring-match over Name and
// Aliases (case-insensitive); prefix matches are ranked before mid-string
// matches; within rank, alphabetical. Empty query returns all.
func (r *Registry) Filter(query string) []*Command {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]*Command, len(r.cmds))
		copy(out, r.cmds)
		sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}

	type scored struct {
		cmd  *Command
		rank int // 0 = prefix, 1 = substring; lower is better
	}
	var matches []scored
	for _, c := range r.cmds {
		rank := -1
		n := strings.ToLower(c.Name)
		if strings.HasPrefix(n, query) {
			rank = 0
		} else if strings.Contains(n, query) {
			rank = 1
		}
		for _, a := range c.Aliases {
			al := strings.ToLower(a)
			if strings.HasPrefix(al, query) && (rank == -1 || rank > 0) {
				rank = 0
			} else if strings.Contains(al, query) && rank == -1 {
				rank = 1
			}
		}
		if rank >= 0 {
			matches = append(matches, scored{c, rank})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		return matches[i].cmd.Name < matches[j].cmd.Name
	})
	out := make([]*Command, len(matches))
	for i, m := range matches {
		out[i] = m.cmd
	}
	return out
}
