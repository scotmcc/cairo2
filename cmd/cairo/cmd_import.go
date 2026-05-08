package main

// cmd_import.go — runImport and its direct helpers.

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	force := fs.Bool("force", false, "skip the interactive confirmation")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cairo import [--force] <bundle.cairo>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one bundle path")
	}
	in := fs.Arg(0)

	tmpDir, err := os.MkdirTemp("", "cairo-import-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	manifest, stagedDB, err := unpackBundle(in, tmpDir)
	if err != nil {
		return err
	}

	if manifest.Version != manifestVersion {
		return fmt.Errorf("bundle version %q does not match current cairo version %q", manifest.Version, manifestVersion)
	}

	fmt.Printf("bundle:\n  exported: %s\n  format:   %s\n  memories: %d  skills: %d  roles: %d  prompt_parts: %d\n",
		manifest.ExportedAt.Local().Format(time.RFC3339),
		historyLabel(manifest.IncludesHistory),
		manifest.Counts["memories"], manifest.Counts["skills"], manifest.Counts["roles"], manifest.Counts["prompt_parts"])

	dst := cairoDBPath()
	if _, err := os.Stat(dst); err == nil && !*force {
		fmt.Fprintln(os.Stderr, "this will REPLACE your current cairo identity with the contents of the bundle.")
		fmt.Fprint(os.Stderr, "a backup will be written alongside. proceed? [y/N]: ")
		var resp string
		fmt.Fscanln(os.Stdin, &resp)
		if resp != "y" && resp != "Y" && resp != "yes" {
			return fmt.Errorf("aborted")
		}
	}

	if _, err := os.Stat(dst); err == nil {
		backup := fmt.Sprintf("%s.pre-import-%s", dst, time.Now().UTC().Format("20060102T150405Z"))
		if err := os.Rename(dst, backup); err != nil {
			return fmt.Errorf("backup current DB: %w", err)
		}
		fmt.Printf("backup: %s\n", backup)
	}

	if err := copyFile(stagedDB, dst); err != nil {
		return fmt.Errorf("install bundle DB: %w", err)
	}

	fmt.Printf("imported into %s — next cairo run uses the bundle's identity\n", dst)
	return nil
}
