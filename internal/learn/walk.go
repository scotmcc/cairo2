// Package learn walks a project directory, summarizes each file via a small
// LLM, embeds the summary, and stores everything in the projects /
// indexed_files tables. Distinct from internal/tools/codeindex (which
// embeds raw content into a single pool with no summaries) — learn is
// project-namespaced, summary-first, and intentional (user runs it).
package learn

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Defaults tuned for the typical "learn about a code repo" use case. Files
// over MaxFileSize get skipped; binary detection looks at the first 512 B.
const (
	MaxFileSize      = 256 * 1024 // 256 KB — bigger files almost always = data, not source
	binaryCheckBytes = 512
)

// builtinExcludes are directories/globs we always skip regardless of
// gitignore. Covers the common cases where gitignore alone is not enough
// (vendor/ may be in repo but useless to summarize per-file, .git is never
// useful, etc).
var builtinExcludes = []string{
	".git", ".svn", ".hg",
	"node_modules", "vendor", "target", "dist", "build", ".next",
	".venv", "venv", "__pycache__", ".pytest_cache", ".tox",
	".idea", ".vscode", ".cairo", ".claude",
	".DS_Store",
}

// binaryExtensions are file extensions we never bother reading even if a
// scan would also catch them — saves the I/O.
var binaryExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".ico": true, ".svg": false, // svg is XML, keep
	".bmp": true, ".tiff": true, ".heic": true,
	".mp3": true, ".wav": true, ".flac": true, ".ogg": true, ".m4a": true,
	".mp4": true, ".mov": true, ".avi": true, ".webm": true, ".mkv": true,
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true,
	".xz": true, ".7z": true, ".rar": true,
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true,
	".db": true, ".sqlite": true, ".sqlite3": true,
	".o": true, ".a": true, ".so": true, ".dylib": true, ".dll": true, ".exe": true,
	".class": true, ".jar": true, ".war": true,
	".pyc": true, ".pyo": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
}

// Candidate is a file the walker has identified as worth indexing — already
// passed the size/binary/exclusion checks but content has not been read.
type Candidate struct {
	AbsPath string
	RelPath string
	Bytes   int
	Type    string // file extension without the leading dot, lowercased
}

// Walk returns the sorted list of files under root that should be indexed.
// extraExcludes are added on top of the builtin exclusions; gitignore
// patterns from .gitignore at root are honored when present.
//
// The walker does not read file contents — only stats them. Returns
// candidates sorted by relative path so progress reporting is deterministic.
func Walk(root string, extraExcludes []string) ([]Candidate, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	matcher := newExcludeMatcher(absRoot, extraExcludes)

	var out []Candidate
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries instead of aborting the whole walk
		}
		if path == absRoot {
			return nil
		}
		rel, _ := filepath.Rel(absRoot, path)

		// Skip symlinks unconditionally: a symlink pointing to an ancestor
		// loops the walk, and one into a sibling tree double-indexes content
		// reachable by its real path. WalkDir does not follow symlinks into
		// directories anyway, but file symlinks are still visited.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		if d.IsDir() {
			if matcher.matches(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if matcher.matches(rel, false) {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if binaryExtensions[ext] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > MaxFileSize {
			return nil
		}
		if info.Size() == 0 {
			return nil
		}
		if isBinary(path) {
			return nil
		}

		typ := strings.TrimPrefix(ext, ".")
		out = append(out, Candidate{
			AbsPath: path,
			RelPath: rel,
			Bytes:   int(info.Size()),
			Type:    typ,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	return out, nil
}

// SHA256 returns the hex SHA-256 of a file. Used by the indexer to skip
// unchanged files on refresh — cheap relative to summarizing+embedding.
func SHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// isBinary opens the file, reads up to binaryCheckBytes bytes, and reports
// whether they contain a NUL — a cheap heuristic that catches actual
// binaries while accepting code, markdown, JSON, etc.
func isBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true // can't read it — treat as binary so we skip
	}
	defer f.Close()
	buf := make([]byte, binaryCheckBytes)
	n, _ := f.Read(buf)
	return bytes.IndexByte(buf[:n], 0) >= 0
}

// --- exclusion matcher ---

// excludeMatcher composes builtin excludes, user-supplied extras, and any
// .gitignore entries at the project root. We deliberately do *not* parse
// nested .gitignore files in subdirectories — that adds complexity for
// modest gain in the "learn about a single project" use case.
//
// Three buckets:
//   - dirNames: raw basenames to skip wherever they appear in the tree
//     (e.g. "node_modules" excludes a/node_modules and b/c/node_modules).
//   - rootAnchored: paths that only match at the project root — populated
//     from gitignore lines that start with '/' (e.g. "/cairo" means the
//     `cairo` binary at root, NOT every cmd/cairo/*.go file).
//   - patterns: full filepath.Match globs matched against base AND rel.
type excludeMatcher struct {
	dirNames     map[string]bool
	rootAnchored map[string]bool
	patterns     []string
}

func newExcludeMatcher(root string, extras []string) *excludeMatcher {
	m := &excludeMatcher{
		dirNames:     map[string]bool{},
		rootAnchored: map[string]bool{},
	}
	for _, e := range builtinExcludes {
		m.dirNames[e] = true
	}
	for _, e := range extras {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.ContainsAny(e, "/*?[") {
			m.dirNames[e] = true
		} else {
			m.patterns = append(m.patterns, e)
		}
	}
	// Layer .gitignore on top.
	for _, raw := range readGitignore(filepath.Join(root, ".gitignore")) {
		anchored := strings.HasPrefix(raw, "/")
		line := strings.TrimPrefix(raw, "/")
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			continue
		}
		hasGlob := strings.ContainsAny(line, "*?[")
		switch {
		case anchored && !hasGlob:
			// Root-anchored literal path (e.g. "/cairo", "/bin"). Matches
			// only at the top of the project — preserves the file in
			// nested dirs like cmd/cairo/.
			m.rootAnchored[line] = true
		case anchored && hasGlob:
			// Root-anchored glob (e.g. "/dist/*.tmp"). Treat as a pattern
			// matched only against the rel path (not basename).
			m.patterns = append(m.patterns, line)
		case !anchored && !hasGlob:
			// Free-floating basename (e.g. "node_modules", "*.log without
			// the *"). Matches at any depth.
			m.dirNames[line] = true
		default:
			m.patterns = append(m.patterns, line)
		}
	}
	return m
}

// matches reports whether the relative path should be excluded. isDir is
// passed so directory-only patterns (trailing /) can short-circuit
// SkipDir behavior.
func (m *excludeMatcher) matches(rel string, isDir bool) bool {
	base := filepath.Base(rel)

	// Root-anchored literals match only when rel == name (no path
	// separator). Preserves cmd/cairo/* when gitignore says "/cairo".
	if !strings.ContainsRune(rel, filepath.Separator) && m.rootAnchored[rel] {
		return true
	}

	if m.dirNames[base] {
		return true
	}
	// Also check parent components — a file deep in node_modules/ should
	// match even if WalkDir didn't SkipDir on the parent (defensive).
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if m.dirNames[part] {
			return true
		}
	}
	for _, pat := range m.patterns {
		if matched, _ := filepath.Match(pat, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pat, rel); matched {
			return true
		}
	}
	_ = isDir
	return false
}

// readGitignore parses a .gitignore file into trimmed pattern strings.
// Comments (# ...) and blank lines are dropped. Negation (!pattern) is
// not honored — false positives from skipped negation are tolerable for
// the "learn about" use case.
//
// The leading slash is preserved in the returned strings — newExcludeMatcher
// inspects it to decide root-anchored vs free-floating semantics. Stripping
// it here would lose that distinction and cause "/cairo" to match every
// path component named "cairo" (incl. cmd/cairo/*), which is wrong.
func readGitignore(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		out = append(out, line)
	}
	return out
}
