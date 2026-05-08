package tui

// prefixes.go — user input prefixes that reshape what Selene sees.
//
// The user types a message; before it goes to Selene, we check for special
// prefixes and expand them. The return from each expander is two strings:
// one for the transcript (what the user sees) and one for Selene (what
// she receives as the user turn). Usually they're identical, but for @file
// references we show the short mention in the transcript and append the
// expanded file contents only to what Selene receives.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// PasteRef describes a single diverted-paste payload kept on disk while the
// user's input still references it via an @paste:N token.
type PasteRef struct {
	Path  string
	Bytes int
	Lines int
}

// PrefixExpander handles !shell, @file, and @paste expansions before
// submitting to the agent.
type PrefixExpander struct {
	WorkDir string
	// PasteRefs is the live registry of diverted pastes for the current
	// session. The map header is captured here at construction; the
	// underlying storage is shared with the model, so writes through the
	// model are visible from Expand. Nil is fine — disables @paste handling.
	PasteRefs map[string]*PasteRef
}

// Expand rewrites a raw input message, handling !shell, @file, and @paste
// expansions. Returns (displayed, sent) — what to render in the transcript
// and what to submit to Selene.
func (e *PrefixExpander) Expand(raw string) (displayed, sent string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "!") {
		return e.expandShell(strings.TrimSpace(strings.TrimPrefix(raw, "!")))
	}
	displayed, sent = e.expandFileRefs(raw)
	sent = e.appendPasteContent(raw, sent)
	return displayed, sent
}

// expandShell runs cmdStr via bash in the session's CWD, captures combined
// stdout+stderr, caps size, and returns both the transcript form and the
// Selene form (identical for shell — the output is the content).
//
// Design notes:
//   - Bash -c for convenience: users can pipe, redirect, quote as they'd
//     expect from a terminal. Security is already "the user is typing
//     into a local TUI with access to their shell" — there's no
//     privilege boundary to worry about.
//   - Output capped at shellOutputMax so a runaway command (e.g. `find /`)
//     doesn't explode the context window.
//   - 30s timeout on the cmd — longer runs should use the agent tool
//     path, not the `!` shortcut.
func (e *PrefixExpander) expandShell(cmdStr string) (displayed, sent string) {
	if cmdStr == "" {
		return "(empty ! command)", "(empty ! command)"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	cmd.Dir = e.WorkDir

	raw, err := cmd.CombinedOutput()
	output := string(raw)
	const shellOutputMax = 16 * 1024
	truncated := false
	if len(output) > shellOutputMax {
		output = output[:shellOutputMax]
		truncated = true
	}

	var tail string
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		tail = "\n[timed out after 30s]"
	case err != nil:
		tail = fmt.Sprintf("\n[exit: %v]", err)
	}
	if truncated {
		tail = "\n[... output truncated]" + tail
	}

	// Format as a shell transcript line + fenced body. The fences make it
	// obvious in Selene's context that this is captured terminal output,
	// not a user statement.
	formatted := fmt.Sprintf("$ %s\n```\n%s%s\n```", cmdStr, strings.TrimRight(output, "\n"), tail)
	return formatted, formatted
}

// --- @file references ---

// fileRefRe finds @<path> tokens. Intentionally greedy on non-whitespace,
// then trimmed of trailing sentence punctuation in the caller — the regex
// can't reliably know where "trailing punctuation" ends vs where a
// filename's own punctuation begins.
var fileRefRe = regexp.MustCompile(`@[\w./\-~]+`)

// File-reference size caps — keep Selene's context reasonable.
const (
	fileRefMaxBytes = 64 * 1024
	fileRefMaxTotal = 256 * 1024
)

// trailing punctuation characters to strip from a matched @path when the
// exact match doesn't exist on disk.
const fileRefTrailingPunct = ".,!?:;)]}'\""

// expandFileRefs walks raw for @<path> tokens that resolve to readable files
// under the session CWD, and appends their contents to what's sent to Selene.
// The transcript keeps the short reference form so the conversation stays
// readable; the expansion only exists in what Selene receives.
func (e *PrefixExpander) expandFileRefs(raw string) (displayed, sent string) {
	matches := fileRefRe.FindAllStringIndex(raw, -1)
	if len(matches) == 0 {
		return raw, raw
	}

	type resolved struct {
		ref      string // the @path as typed (e.g. "@main.go")
		path     string // resolved absolute path
		contents string
		err      string
	}
	var refs []resolved
	seen := make(map[string]bool) // dedupe — user may mention the same file twice

	total := 0
	for _, loc := range matches {
		tok := raw[loc[0]:loc[1]]
		rel := strings.TrimPrefix(tok, "@")
		resolvedPath := e.resolveFileRef(rel)
		if resolvedPath == "" {
			// Try stripping trailing sentence punctuation (e.g. "@foo.go," or
			// "@bar.md.") before giving up.
			trimmed := strings.TrimRight(rel, fileRefTrailingPunct)
			if trimmed != rel {
				resolvedPath = e.resolveFileRef(trimmed)
			}
			if resolvedPath == "" {
				continue
			}
			tok = "@" + trimmed
		}
		if seen[resolvedPath] {
			continue
		}
		seen[resolvedPath] = true

		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			refs = append(refs, resolved{ref: tok, path: resolvedPath, err: err.Error()})
			continue
		}
		if isBinary(data) {
			refs = append(refs, resolved{ref: tok, path: resolvedPath, err: "binary file — skipped"})
			continue
		}
		if len(data) > fileRefMaxBytes {
			data = append(data[:fileRefMaxBytes], []byte("\n[... file truncated]")...)
		}
		if total+len(data) > fileRefMaxTotal {
			refs = append(refs, resolved{ref: tok, path: resolvedPath, err: "aggregate file-ref size cap reached — skipped"})
			continue
		}
		total += len(data)
		refs = append(refs, resolved{ref: tok, path: resolvedPath, contents: string(data)})
	}

	if len(refs) == 0 {
		return raw, raw
	}

	var b strings.Builder
	b.WriteString(raw)
	b.WriteString("\n\n---\nReferenced files:\n")
	for _, r := range refs {
		if r.err != "" {
			fmt.Fprintf(&b, "\n%s: [%s]\n", r.ref, r.err)
			continue
		}
		lang := langHint(r.path)
		fmt.Fprintf(&b, "\n%s:\n```%s\n%s\n```\n", r.ref, lang, strings.TrimRight(r.contents, "\n"))
	}
	return raw, b.String()
}

// resolveFileRef turns a user-typed path (relative or absolute) into an
// absolute path ONLY if it resolves under the session CWD. Anything that
// escapes CWD (e.g. "../../etc/passwd" or "/etc/passwd") returns "" so the
// reference falls back to being treated as literal text. Mirrors the
// requireUnderCWD discipline of the file tools so `!` / `@` don't expand
// the scope of what the user can exfiltrate via the UI.
func (e *PrefixExpander) resolveFileRef(rel string) string {
	if rel == "" {
		return ""
	}
	cwd, err := filepath.Abs(e.WorkDir)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(filepath.Join(cwd, rel))
	if err != nil {
		return ""
	}
	// Walk up until we hit an existing ancestor, then EvalSymlinks the
	// longest existing prefix so we don't get tripped by nonexistent paths.
	existing := abs
	for existing != "/" && existing != "." {
		if _, err := os.Stat(existing); err == nil {
			real, err := filepath.EvalSymlinks(existing)
			if err == nil {
				existing = real
			}
			break
		}
		existing = filepath.Dir(existing)
	}
	if !strings.HasPrefix(existing, cwd) && existing != cwd {
		return ""
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return ""
	}
	return abs
}

// isBinary returns true when data looks binary (contains a NUL byte in the
// first 512 bytes). Cheap enough to do per @-reference; avoids dumping jpeg
// or executable bytes into Selene's context.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// langHints maps lowercase file extensions to their fence language hint.
var langHints = map[string]string{
	".go":       "go",
	".py":       "python",
	".js":       "javascript",
	".mjs":      "javascript",
	".ts":       "typescript",
	".rs":       "rust",
	".rb":       "ruby",
	".sh":       "bash",
	".bash":     "bash",
	".zsh":      "bash",
	".md":       "markdown",
	".markdown": "markdown",
	".json":     "json",
	".yaml":     "yaml",
	".yml":      "yaml",
	".toml":     "toml",
	".sql":      "sql",
	".html":     "html",
	".css":      "css",
}

// langHint returns a short fence language hint for common extensions so
// Selene's renderer can syntax-highlight. Empty for unknown — the fence
// still works, just without highlighting.
func langHint(path string) string {
	return langHints[strings.ToLower(filepath.Ext(path))]
}

// pasteRefRe matches @paste:<digits> tokens. Tight on purpose so it can't
// collide with @file paths.
var pasteRefRe = regexp.MustCompile(`@paste:(\d+)`)

// appendPasteContent looks up @paste:N tokens in raw against the registered
// PasteRefs, reads each tempfile, and appends a "Pasted content" section
// to sent so Selene receives the full payload labeled as a paste (not as
// something the user typed). Returns sent unchanged when there are no
// paste tokens or no PasteRefs registry.
func (e *PrefixExpander) appendPasteContent(raw, sent string) string {
	if e.PasteRefs == nil {
		return sent
	}
	matches := pasteRefRe.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return sent
	}

	type resolved struct {
		token    string
		ref      *PasteRef
		contents string
		err      string
	}
	var refs []resolved
	seen := make(map[string]bool)
	for _, m := range matches {
		token := m[0]
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		ref, ok := e.PasteRefs[id]
		if !ok {
			refs = append(refs, resolved{token: token, err: "paste reference no longer available"})
			continue
		}
		data, err := os.ReadFile(ref.Path)
		if err != nil {
			refs = append(refs, resolved{token: token, ref: ref, err: err.Error()})
			continue
		}
		refs = append(refs, resolved{token: token, ref: ref, contents: string(data)})
		// Best-effort cleanup once the paste has been consumed by Selene.
		// If the user @paste:N's the same token in a later turn it'll be
		// gone — fine, that's a paste-and-forget UX, not a saved-clipboard.
		_ = os.Remove(ref.Path)
		delete(e.PasteRefs, id)
	}

	if len(refs) == 0 {
		return sent
	}
	var b strings.Builder
	b.WriteString(sent)
	b.WriteString("\n\n---\nPasted content (the user pasted this — they did not type it):\n")
	for _, r := range refs {
		if r.err != "" {
			fmt.Fprintf(&b, "\n%s: [%s]\n", r.token, r.err)
			continue
		}
		fmt.Fprintf(&b, "\n%s (%d bytes, %d lines):\n```\n%s\n```\n",
			r.token, r.ref.Bytes, r.ref.Lines,
			strings.TrimRight(r.contents, "\n"))
	}
	return b.String()
}
