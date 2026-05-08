//go:build windows

package jobs

// ReadStartToken on Windows has no /proc filesystem; always returns empty.
func ReadStartToken(pid int) (string, error) {
	return "", nil
}

// IsTaskAlive on Windows cannot verify process identity via start token.
// Always returns true — Windows never sweeps orphans via this mechanism.
func IsTaskAlive(t Task) bool {
	return true
}
