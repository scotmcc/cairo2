package main

// bundle_manifest.go — bundleManifest type and version constant.

import "time"

// bundleManifest describes a .cairo bundle's contents. Versioned so future
// bundles can declare additive/incompatible shape changes; keep it simple for
// now — a single schema version for the DB contents.
type bundleManifest struct {
	Version         string         `json:"version"`
	ExportedAt      time.Time      `json:"exported_at"`
	IncludesHistory bool           `json:"includes_history"`
	Counts          map[string]int `json:"counts"`
}

const manifestVersion = "1"
