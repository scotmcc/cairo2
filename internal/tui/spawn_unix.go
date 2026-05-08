//go:build !windows

package tui

import "syscall"

// spawnDetached returns SysProcAttr that puts the child in its own session
// so the background subprocess survives its parent's terminal closing.
// Mirrors the same helper in learn/spawn_unix.go and tools/spawn_unix.go.
func spawnDetached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
