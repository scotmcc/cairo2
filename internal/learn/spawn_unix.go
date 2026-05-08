//go:build !windows

package learn

import "syscall"

// Detached returns a SysProcAttr that puts the child in its own session,
// so it survives its parent's terminal closing. Pass to SpawnBackground
// from any caller that wants the indexer to outlive a Ctrl-C of the TUI.
func Detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
