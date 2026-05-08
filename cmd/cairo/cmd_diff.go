package main

// cmd_diff.go — runDiff, printRoleModelDiff, roleModelMap, countEntities.

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func runDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cairo diff <bundle.cairo>")
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

	tmpDir, err := os.MkdirTemp("", "cairo-diff-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	manifest, stagedDB, err := unpackBundle(in, tmpDir)
	if err != nil {
		return err
	}

	local := cairoDBPath()
	if _, err := os.Stat(local); err != nil {
		return fmt.Errorf("no local cairo DB at %s to diff against", local)
	}
	localCounts, err := countEntities(local)
	if err != nil {
		return fmt.Errorf("count local: %w", err)
	}

	fmt.Printf("bundle (%s) vs local:\n", manifest.ExportedAt.Local().Format("2006-01-02T15:04:05Z07:00"))
	keys := []string{"memories", "skills", "notes", "roles", "prompt_parts", "custom_tools", "config_keys"}
	for _, k := range keys {
		l := localCounts[k]
		b := manifest.Counts[k]
		marker := " "
		if l != b {
			marker = "*"
		}
		fmt.Printf("  %s %-15s local=%-4d bundle=%-4d\n", marker, k, l, b)
	}

	localSoul, _ := readConfigValue(local, "soul_prompt")
	bundleSoul, _ := readConfigValue(stagedDB, "soul_prompt")
	if localSoul != bundleSoul {
		fmt.Println("\nsoul differs:")
		fmt.Printf("  local:  %s\n", trunc(localSoul, 200))
		fmt.Printf("  bundle: %s\n", trunc(bundleSoul, 200))
	} else {
		fmt.Println("\nsoul matches")
	}

	if err := printRoleModelDiff(local, stagedDB); err != nil {
		fmt.Fprintf(os.Stderr, "role model diff: %v\n", err)
	}

	return nil
}

func printRoleModelDiff(localPath, bundlePath string) error {
	localMap, err := roleModelMap(localPath)
	if err != nil {
		return err
	}
	bundleMap, err := roleModelMap(bundlePath)
	if err != nil {
		return err
	}
	names := make(map[string]struct{})
	for k := range localMap {
		names[k] = struct{}{}
	}
	for k := range bundleMap {
		names[k] = struct{}{}
	}
	var diffs []string
	for name := range names {
		if localMap[name] != bundleMap[name] {
			diffs = append(diffs, fmt.Sprintf("  %s: local=%q bundle=%q", name, localMap[name], bundleMap[name]))
		}
	}
	if len(diffs) == 0 {
		fmt.Println("role→model assignments match")
		return nil
	}
	fmt.Println("\nrole→model differs:")
	for _, d := range diffs {
		fmt.Println(d)
	}
	return nil
}

func roleModelMap(path string) (map[string]string, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer sqldb.Close()
	rows, err := sqldb.Query("SELECT name, model FROM roles")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var n, m string
		if err := rows.Scan(&n, &m); err != nil {
			return nil, err
		}
		out[n] = m
	}
	return out, rows.Err()
}

func countEntities(path string) (map[string]int, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer sqldb.Close()
	out := make(map[string]int)
	tables := []string{"memories", "skills", "notes", "roles", "prompt_parts", "custom_tools", "sessions", "messages", "summaries", "facts", "jobs", "tasks"}
	for _, t := range tables {
		var n int
		if err := sqldb.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&n); err != nil {
			continue
		}
		out[t] = n
	}
	var n int
	if err := sqldb.QueryRow("SELECT COUNT(*) FROM config").Scan(&n); err == nil {
		out["config_keys"] = n
	}
	return out, nil
}

func readConfigValue(path, key string) (string, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	defer sqldb.Close()
	var val string
	err = sqldb.QueryRow("SELECT value FROM config WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
