package tui

import "github.com/charmbracelet/lipgloss"

// tool_family.go — maps tool names to the semantic-color family they belong
// to, so the transcript line for a memory call glows the same amber as
// "10 mem" in the status bar, an agent call glows the lavender of the
// thread spinner, and so on. Peripheral recognition: the color is the story.
//
// New tools default to the "filesystem" family (soft green) — that's the
// generic "doing work" tint. Promote to a dedicated family only when the
// tool relates to an existing first-class concept in the status bar.

// toolFamily identifies a color-coded group of tools. Families match the
// concepts already named in style.go; adding a new family means adding a
// concept, not just a color.
type toolFamily int

const (
	familyFS        toolFamily = iota // read/write/edit/bash — soft green
	familyMemory                      // memory_tool — amber
	familyThreads                     // agent/task/job — lavender
	familyIdentity                    // soul — Selene-blue
	familyKnowledge                   // skill — amber-dim
	familyWeb                         // search/fetch — parchment
	familyAdmin                       // tool_list_builtin — dim
)

// familyOf returns the family for a tool name. Unknown tools fall back to
// familyFS so custom tools aren't invisible — they just get the generic
// "doing work" tint until we decide they belong to a concept.
func familyOf(name string) toolFamily {
	switch name {
	case "memory_tool":
		return familyMemory
	case "agent", "task", "job":
		return familyThreads
	case "soul":
		return familyIdentity
	case "skill":
		return familyKnowledge
	case "search", "fetch":
		return familyWeb
	case "tool_list_builtin":
		return familyAdmin
	default:
		return familyFS
	}
}

// familyIcon returns the single-char glyph for a family. Diamond/dot shapes
// belong to the same visual vocabulary already in the status bar so the
// transcript doesn't pull in new iconography — it reuses what the eye
// already knows.
func familyIcon(f toolFamily) string {
	switch f {
	case familyMemory:
		return "◉"
	case familyThreads:
		return "◈"
	case familyIdentity:
		return "✦"
	case familyKnowledge:
		return "◈"
	case familyWeb:
		return "◇"
	case familyAdmin:
		return "·"
	default:
		return "▸"
	}
}

// familyColor returns the primary color for a family — the one used on the
// tool line in the transcript and on the activity token in the status bar.
func familyColor(f toolFamily) lipgloss.Color {
	switch f {
	case familyMemory:
		return colMemory
	case familyThreads:
		return colThread
	case familyIdentity:
		return colVoiceSelene
	case familyKnowledge:
		// Amber-dim — adjacent to memory (skills are crystallized practice)
		// but quieter so the status bar's "mem" concept stays distinct.
		return lipgloss.Color("#9c7d48")
	case familyWeb:
		return colVoiceUser
	case familyAdmin:
		return colTextDim
	default:
		return colTool
	}
}

// familyStyle returns a lipgloss style pre-tinted for the family. Used on
// the tool line in the transcript.
func familyStyle(f toolFamily) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(familyColor(f))
}
