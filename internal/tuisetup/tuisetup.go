// Package tuisetup hardens Lip Gloss configuration before Bubble Tea's
// package init runs. It has one job: pin the background-color answer so
// bubbletea's init() — which unconditionally calls lipgloss.HasDarkBackground()
// at process start — does not send an OSC 11 query to the terminal.
//
// Why this matters: some emulators (Waveterm observed) respond slowly or
// inconsistently to OSC queries. When bubbletea's probe arrives before the
// program has acquired the terminal, three things can go wrong:
//
//  1. The 5-second probe timeout blocks the first frame, so the alt-screen
//     stays on "initializing…" until the user hits a key (which aborts the
//     read) or the timeout fires.
//  2. The terminal's OSC/CSI response arrives AFTER bubbletea starts its
//     input reader and gets parsed as typed input — the user sees
//     "]11;rgb:0000/0000/0000\" get typed into their prompt.
//  3. The filter in internal/tui/tui.go can mop up most of (2) but can't
//     do anything about (1).
//
// The fix: this package has a package-level var whose initializer runs
// BEFORE any init() in packages that depend on it. By calling
// SetHasDarkBackground on Lip Gloss's default renderer, we set
// explicitBackgroundColor=true — which makes bubbletea's probe skip the
// terminal query entirely (see lipgloss/renderer.go HasDarkBackground).
//
// Import order guarantee: cmd/cairo/main.go imports this package with a
// blank import listed BEFORE any package that transitively imports
// bubbletea. Go's package initialization visits imports in source order,
// so this package's var initializer fires before bubbletea's init.
package tuisetup

import "github.com/charmbracelet/lipgloss"

// Cairo ships a moonlight palette designed against a dark background, so
// pinning to dark is not just a probe-avoidance trick — it's also what
// we'd want anyway. Users on a light terminal will still see our palette;
// they were always going to, because color choices are explicit in style.go.
var _ = func() bool {
	lipgloss.DefaultRenderer().SetHasDarkBackground(true)
	return true
}()
