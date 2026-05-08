package sqliteopen

import (
	"os"
	"path/filepath"
)

// DataDirName is the name of the cairo data directory within the user's home.
const DataDirName = ".cairo"

// DefaultDataDir returns the default cairo data directory (~/.cairo).
// Resolution order: CAIRO_DATA_DIR env var → ~/.cairo.
// The --data-dir CLI flag is handled in main.go and calls db.Open directly
// with the resolved path, bypassing this function.
func DefaultDataDir() string {
	if d := os.Getenv("CAIRO_DATA_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, DataDirName)
}

// busyTimeoutMs is the SQLite busy_timeout value in milliseconds.
const busyTimeoutMs = 15000
