package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
)

// Embedder generates vector embeddings for text. The llm.Client satisfies this.
type Embedder interface {
	Embed(ctx context.Context, model, text string) ([]float32, error)
}

// EmbedClient wraps an Embedder and its configured model name.
// Pass *EmbedClient to tools that need embedding; nil means embedding is unavailable.
type EmbedClient struct {
	Embedder Embedder
	Model    string
}

// Embed embeds text using the configured model. Returns nil, nil if client is nil.
// Uses context.Background() — tool Execute callers that need cancellation should
// call the underlying Embedder directly.
func (e *EmbedClient) Embed(text string) ([]float32, error) {
	if e == nil || e.Embedder == nil || e.Model == "" {
		return nil, nil
	}
	return e.Embedder.Embed(context.Background(), e.Model, text)
}

// DispatchAction routes an Execute call to per-action handlers.
// Returns an error result if action is missing or unrecognized.
func DispatchAction(args map[string]any, toolName string, handlers map[string]func() agent.ToolResult) agent.ToolResult {
	action := strArg(args, "action")
	if action == "" {
		return agent.ToolResult{
			Content: fmt.Sprintf("error: action is required for %s", toolName),
			IsError: true,
		}
	}
	fn, ok := handlers[action]
	if !ok {
		var valid []string
		for k := range handlers {
			valid = append(valid, k)
		}
		sort.Strings(valid)
		return agent.ToolResult{
			Content: fmt.Sprintf("error: unknown action %q — valid: %s", action, strings.Join(valid, "|")),
			IsError: true,
		}
	}
	return fn()
}

// requireUnderCWD returns an error if path is not under workDir.
// Uses filepath.Abs to handle relative paths and walks up to find
// the deepest existing ancestor before calling EvalSymlinks, so it
// works even when the target file doesn't exist yet.
func requireUnderCWD(path, workDir string) error {
	// Resolve workDir to its real absolute path, then eval symlinks.
	cwd, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("unsafe_mode: cannot resolve workdir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}

	// Resolve path — walk up until we find an existing ancestor
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("unsafe_mode: cannot resolve path: %w", err)
	}
	existing := target
	for existing != "/" && existing != "." {
		if _, err := filepath.EvalSymlinks(existing); err == nil {
			if real, err := filepath.EvalSymlinks(existing); err == nil {
				existing = real
			}
			break
		}
		existing = filepath.Dir(existing)
	}

	sep := string(filepath.Separator)
	if !strings.HasPrefix(existing+sep, cwd+sep) {
		return fmt.Errorf("unsafe_mode: path %q is outside session CWD %q — set unsafe_mode=true to allow", path, cwd)
	}
	return nil
}

// resolvePath resolves a path to its absolute form.
// If path is already absolute, returns it unchanged.
// Otherwise, joins it with workDir.
func resolvePath(path, workDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workDir, path)
}

// helpers shared across tool implementations

func strArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func intArg(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return def
}

func boolArg(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

func propEnum(desc string, values []string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

func propOptional(typ, desc, defaultVal string) map[string]any {
	return map[string]any{"type": typ, "description": fmt.Sprintf("%s (optional, default: %s)", desc, defaultVal)}
}

func formatDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// SearchResult is a standardized result item for all search tools.
type SearchResult struct {
	ID     int64
	Label  string // primary display text (content, title, name)
	Detail string // optional secondary text (tags, description)
	Date   time.Time
}

// FormatSearchResults formats a slice of SearchResults as a numbered list.
func FormatSearchResults(results []SearchResult) string {
	if len(results) == 0 {
		return "no results found"
	}
	var b strings.Builder
	for _, r := range results {
		if r.Detail != "" {
			fmt.Fprintf(&b, "[%d] %s — %s\n", r.ID, r.Label, r.Detail)
		} else {
			fmt.Fprintf(&b, "[%d] %s\n", r.ID, r.Label)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// checkWritePermission returns an error if path is outside workDir and
// unsafe_mode is not enabled. Must be called before any file write.
//
// Note on unsafe_mode interaction with discipline mode: at discipline < full
// (DisciplineFull=3), bash is already blocked by the tier check in bash.go, so
// unsafe_mode has no effect there. unsafe_mode only opens up cross-CWD writes
// at DisciplineFull. This function does not enforce discipline-tier — that is
// done per-tool in Execute before this function is reached.
func checkWritePermission(ctx *agent.ToolContext, path string) error {
	unsafeMode, _ := ctx.DB.Config.Get("unsafe_mode")
	if unsafeMode != "true" {
		return requireUnderCWD(path, ctx.WorkDir)
	}
	return nil
}

// checkDiscipline returns a DisciplineRefusal ToolResult if ctx.DisciplineMode
// is below the required tier for the named tool (with optional action label).
// Returns a zero ToolResult (IsError=false) when the call is permitted, so
// callers can check IsError without a separate boolean return.
func checkDiscipline(ctx *agent.ToolContext, toolName, action string, requiredTier int) (agent.ToolResult, bool) {
	if ctx == nil || ctx.DisciplineMode == 0 {
		return agent.ToolResult{}, false // no context or mode unset → allow
	}
	if ctx.DisciplineMode < requiredTier {
		return DisciplineRefusal(ctx.DisciplineMode, toolName, action), true
	}
	return agent.ToolResult{}, false
}

// formatTags converts a comma-separated tag string into a JSON array string.
// Empty input returns "[]". Used by tools that store tags in the DB.
func formatTags(raw string) string {
	if raw == "" {
		return "[]"
	}
	parts := strings.Split(raw, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			tags = append(tags, p)
		}
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

func floatArg(args map[string]any, key string, def float64) float64 {
	switch v := args[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return def
}
