//go:build !windows

package jobs

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// parseStartToken extracts the starttime field (field 22 in man 5 proc,
// 1-indexed) from the raw content of /proc/<pid>/stat. It finds the last ')'
// to safely skip the comm field, which may itself contain spaces and parens.
func parseStartToken(data string) (string, error) {
	end := strings.LastIndex(data, ")")
	if end < 0 {
		return "", fmt.Errorf("malformed stat: no closing paren")
	}
	fields := strings.Fields(data[end+2:]) // skip ") "
	if len(fields) < 20 {
		return "", fmt.Errorf("stat too short: need 20 fields after comm, got %d", len(fields))
	}
	return fields[19], nil // field 22 (man page, 1-indexed) = index 19 after stripping "pid (comm) "
}

// ReadStartToken reads /proc/<pid>/stat and returns the starttime field as a
// string token. Returns "", nil if the process has already exited.
func ReadStartToken(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	return parseStartToken(string(data))
}

// IsTaskAlive reports whether the process recorded in t is still the same
// process that was spawned. It uses PID liveness (syscall.Kill 0) plus a
// start-token comparison to guard against PID reuse.
func IsTaskAlive(t Task) bool {
	if t.PID == nil || *t.PID == 0 {
		return false
	}
	if err := syscall.Kill(*t.PID, 0); err != nil {
		return false
	}
	// Empty token: old row pre-migration; fall back to PID-only liveness.
	if t.StartToken == "" {
		return true
	}
	current, err := ReadStartToken(*t.PID)
	if err != nil {
		return false
	}
	return current == t.StartToken
}
