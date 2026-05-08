package main

// cmd_export.go — runExport and its direct helpers.

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	full := fs.Bool("full", false, "include sessions, messages, summaries, facts, jobs, and tasks (default: omit conversation history)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cairo export [--full] <output.cairo>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one output path")
	}
	out := fs.Arg(0)

	src := cairoDBPath()
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("source DB not found at %s — run cairo once to initialize before exporting", src)
	}

	tmp, err := os.CreateTemp("", "cairo-export-*.db")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath) // VACUUM INTO refuses an existing target
	defer os.Remove(tmpPath)

	if err := vacuumInto(src, tmpPath); err != nil {
		return err
	}

	if !*full {
		if err := stripHistory(tmpPath); err != nil {
			return fmt.Errorf("strip history: %w", err)
		}
	}

	counts, err := countEntities(tmpPath)
	if err != nil {
		return fmt.Errorf("count entities: %w", err)
	}

	manifest := bundleManifest{
		Version:         manifestVersion,
		ExportedAt:      time.Now().UTC(),
		IncludesHistory: *full,
		Counts:          counts,
	}

	if err := writeBundle(out, tmpPath, manifest); err != nil {
		return err
	}

	fmt.Printf("exported to %s\n  format: %s\n  memories: %d  skills: %d  roles: %d  prompt_parts: %d\n",
		out,
		historyLabel(*full),
		counts["memories"], counts["skills"], counts["roles"], counts["prompt_parts"])
	return nil
}

func historyLabel(full bool) string {
	if full {
		return "full (includes sessions, messages, summaries, facts, jobs, tasks)"
	}
	return "identity-only (no conversation history)"
}
