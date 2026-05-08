package main

// bundle_io.go — low-level bundle read/write and DB helpers.

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	_ "modernc.org/sqlite"
)

func vacuumInto(src, dst string) error {
	sqldb, err := sql.Open("sqlite", src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer sqldb.Close()
	// VACUUM INTO refuses an existing target and rejects a path with special
	// chars via the normal parser. Quote defensively.
	quoted := "'" + dst + "'"
	if _, err := sqldb.Exec("VACUUM INTO " + quoted); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dst, err)
	}
	return nil
}

func stripHistory(path string) error {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer sqldb.Close()
	// PRAGMA foreign_keys is per-connection and off by default. Pin the pool
	// to one connection so the PRAGMA is guaranteed to apply to the DELETE
	// that follows; the DSN _foreign_keys=on form turned out to be unreliable
	// here (PRAGMA read back as 0 on the next connection).
	sqldb.SetMaxOpenConns(1)
	if _, err := sqldb.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign_keys: %w", err)
	}
	if _, err := sqldb.Exec("DELETE FROM sessions"); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	// Reclaim space after the cascaded deletes.
	if _, err := sqldb.Exec("VACUUM"); err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return nil
}

func writeBundle(outPath, dbPath string, manifest bundleManifest) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// manifest.json
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "manifest.json",
		Mode: 0644,
		Size: int64(len(manifestBytes)),
	}); err != nil {
		return err
	}
	if _, err := tw.Write(manifestBytes); err != nil {
		return err
	}

	// cairo.db
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "cairo.db",
		Mode: 0644,
		Size: dbInfo.Size(),
	}); err != nil {
		return err
	}
	dbFile, err := os.Open(dbPath)
	if err != nil {
		return err
	}
	defer dbFile.Close()
	if _, err := io.Copy(tw, dbFile); err != nil {
		return err
	}
	return nil
}

func unpackBundle(bundlePath, dir string) (bundleManifest, string, error) {
	var manifest bundleManifest
	f, err := os.Open(bundlePath)
	if err != nil {
		return manifest, "", fmt.Errorf("open bundle: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return manifest, "", fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var stagedDB string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return manifest, "", fmt.Errorf("tar: %w", err)
		}
		// Defense against tar traversal — only allow bare names.
		if filepath.Base(hdr.Name) != hdr.Name {
			return manifest, "", fmt.Errorf("bundle contains suspicious path %q", hdr.Name)
		}
		out := filepath.Join(dir, hdr.Name)
		switch hdr.Name {
		case "manifest.json":
			data, err := io.ReadAll(tr)
			if err != nil {
				return manifest, "", err
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				return manifest, "", fmt.Errorf("parse manifest: %w", err)
			}
			if err := os.WriteFile(out, data, 0644); err != nil {
				return manifest, "", err
			}
		case "cairo.db":
			w, err := os.Create(out)
			if err != nil {
				return manifest, "", err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return manifest, "", err
			}
			w.Close()
			stagedDB = out
		default:
			// Ignore unknown entries for forward compatibility.
		}
	}
	if manifest.Version == "" {
		return manifest, "", fmt.Errorf("bundle is missing manifest.json")
	}
	if stagedDB == "" {
		return manifest, "", fmt.Errorf("bundle is missing cairo.db")
	}
	return manifest, stagedDB, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func cairoDBPath() string {
	return filepath.Join(sqliteopen.DefaultDataDir(), "cairo.db")
}
