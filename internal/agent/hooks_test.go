package agent

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// addHook inserts an enabled hook for event+command and returns its ID.
func addHook(t *testing.T, database *sqliteopen.DB, event, command string) int64 {
	t.Helper()
	h, err := database.Hooks.Add(event, command)
	if err != nil {
		t.Fatalf("add hook: %v", err)
	}
	return h.ID
}

// setHookMatcher updates the matcher column for an existing hook.
func setHookMatcher(t *testing.T, database *sqliteopen.DB, hookID int64, matcher string) {
	t.Helper()
	if err := database.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE hooks SET matcher = ? WHERE id = ?`, matcher, hookID)
		return err
	}); err != nil {
		t.Fatalf("set hook matcher: %v", err)
	}
}

func TestRunHooks_EmptyMatcher_MatchesAll(t *testing.T) {
	d := openTestDB(t)
	sentinel := filepath.Join(t.TempDir(), "fired")
	addHook(t, d, "pre_tool", fmt.Sprintf("touch %s", sentinel))

	RunHooks(d, "pre_tool", "any_target", nil)

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("hook with empty matcher should fire for any target: %v", err)
	}
}

func TestRunHooks_SpecificMatcher_Matches(t *testing.T) {
	d := openTestDB(t)
	sentinel := filepath.Join(t.TempDir(), "fired")
	id := addHook(t, d, "pre_tool", fmt.Sprintf("touch %s", sentinel))
	setHookMatcher(t, d, id, "Plan|Review")

	// Non-matching target: hook must not fire.
	RunHooks(d, "pre_tool", "Implement", nil)
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("hook fired for non-matching target 'Implement'")
	}

	// Matching target: hook must fire.
	RunHooks(d, "pre_tool", "Plan", nil)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("hook did not fire for matching target 'Plan': %v", err)
	}
}

func TestRunHooks_BadRegex_DoesNotCrash(t *testing.T) {
	d := openTestDB(t)
	id := addHook(t, d, "pre_tool", "echo skipped")
	setHookMatcher(t, d, id, "[") // invalid regex — must not panic

	// Must not panic; chain continues with default Continue=true.
	result := RunHooks(d, "pre_tool", "anything", nil)
	if !result.Continue {
		t.Fatal("bad regex should skip the hook and not abort chain: expected Continue=true")
	}
}

func TestRunHooks_ValidJSON_Continue_False(t *testing.T) {
	d := openTestDB(t)
	addHook(t, d, "pre_tool", `echo '{"continue":false}'`)

	result := RunHooks(d, "pre_tool", "", nil)
	if result.Continue {
		t.Fatal(`expected Continue=false from JSON {"continue":false}`)
	}
}

func TestRunHooks_ValidJSON_SuppressOutput(t *testing.T) {
	d := openTestDB(t)
	addHook(t, d, "pre_tool", `echo '{"continue":true,"suppressOutput":true}'`)

	result := RunHooks(d, "pre_tool", "", nil)
	if !result.Continue {
		t.Fatal("expected Continue=true")
	}
	if !result.SuppressOutput {
		t.Fatal("expected SuppressOutput=true")
	}
}

func TestRunHooks_InvalidJSON_FallsThrough(t *testing.T) {
	d := openTestDB(t)
	addHook(t, d, "pre_tool", `echo not-json`)

	result := RunHooks(d, "pre_tool", "", nil)
	if !result.Continue {
		t.Fatal("invalid JSON stdout should fall through to exit-code semantics: expected Continue=true")
	}
}

func TestRunHooks_EmptyStdout_FallsThrough(t *testing.T) {
	d := openTestDB(t)
	addHook(t, d, "pre_tool", `true`) // exits 0, no stdout

	result := RunHooks(d, "pre_tool", "", nil)
	if !result.Continue {
		t.Fatal("empty stdout should fall through: expected Continue=true")
	}
}

func TestRunHooks_ChainAbort(t *testing.T) {
	d := openTestDB(t)
	sentinel := filepath.Join(t.TempDir(), "second_ran")

	// First hook aborts the chain.
	addHook(t, d, "pre_tool", `echo '{"continue":false}'`)
	// Second hook would create sentinel if reached.
	addHook(t, d, "pre_tool", fmt.Sprintf("touch %s", sentinel))

	result := RunHooks(d, "pre_tool", "", nil)
	if result.Continue {
		t.Fatal("expected Continue=false after first hook aborts chain")
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("second hook must not execute after chain abort")
	}
}
