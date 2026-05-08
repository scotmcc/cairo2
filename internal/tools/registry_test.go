package tools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenAt(path)
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// stubEmbedder implements the Embedder interface with a deterministic fake
// embedding so search-by-similarity tests don't need a live Ollama. Maps
// text → a 2-dim vector seeded by content length, which is enough to make
// cosine similarity order predictable for a handful of test strings.
type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, model, text string) ([]float32, error) {
	return []float32{float32(len(text)), 1.0}, nil
}

// fakeTool is a minimal agent.Tool for FilterByAllowlist tests.
type fakeTool struct{ name string }

func (f fakeTool) Name() string               { return f.name }
func (f fakeTool) Description() string        { return "fake" }
func (f fakeTool) Parameters() map[string]any { return map[string]any{} }
func (f fakeTool) Execute(map[string]any, *agent.ToolContext) agent.ToolResult {
	return agent.ToolResult{}
}

func TestFilterByAllowlist_EmptyAllowlistIsUnrestricted(t *testing.T) {
	tools := []agent.Tool{fakeTool{"a"}, fakeTool{"b"}, fakeTool{"c"}}

	got := FilterByAllowlist(tools, nil)
	if len(got) != 3 {
		t.Errorf("nil allowlist: expected unrestricted (3 tools), got %d", len(got))
	}

	got = FilterByAllowlist(tools, []string{})
	if len(got) != 3 {
		t.Errorf("empty allowlist: expected unrestricted (3 tools), got %d", len(got))
	}
}

func TestFilterByAllowlist_Intersects(t *testing.T) {
	tools := []agent.Tool{fakeTool{"a"}, fakeTool{"b"}, fakeTool{"c"}}

	got := FilterByAllowlist(tools, []string{"a", "c"})
	if len(got) != 2 {
		t.Fatalf("expected 2 tools after filter, got %d", len(got))
	}
	names := []string{got[0].Name(), got[1].Name()}
	if names[0] != "a" || names[1] != "c" {
		t.Errorf("expected order [a c], got %v", names)
	}
}

func TestFilterByAllowlist_UnknownNamesIgnored(t *testing.T) {
	tools := []agent.Tool{fakeTool{"a"}, fakeTool{"b"}}

	got := FilterByAllowlist(tools, []string{"a", "does_not_exist"})
	if len(got) != 1 {
		t.Errorf("expected 1 matching tool, got %d", len(got))
	}
	if got[0].Name() != "a" {
		t.Errorf("expected 'a', got %q", got[0].Name())
	}
}

// TestDefault_AllNamesUnique is a regression guard: after the consolidation
// pass, every built-in tool must have a unique Name(). Two tools with the
// same name would be an easy mistake and would silently shadow each other
// in the loop's toolMap lookup.
func TestDefault_AllNamesUnique(t *testing.T) {
	d := openTestDB(t)

	tools := Default(d, nil, "", nil)
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		name := tool.Name()
		if seen[name] {
			t.Errorf("duplicate tool name in Default(): %q", name)
		}
		seen[name] = true
	}
}

// TestThinkingPartner_AllowlistCoversDefaultTools asserts that every tool
// returned by Default() is present in thinking_partner's allowlist (seed +
// applied migrations). This is the regression gate for the recurring omission
// pattern: worktree (v084), tool_list_builtin (v085), merge_job (v115).
// If you add a new tool to Default() and intentionally exclude it from
// thinking_partner, add its name to intentionallyExcluded with a comment.
func TestThinkingPartner_AllowlistCoversDefaultTools(t *testing.T) {
	// Tools in Default() that are deliberately excluded from thinking_partner.
	// worktree and merge_job are wired separately in cmd/cairo (need a
	// *worktree.Manager), but they ARE in the allowlist — no intentional
	// exclusions at the time this test was written.
	intentionallyExcluded := map[string]string{
		// example: "some_tool": "reason it is excluded from thinking_partner"
	}

	d := openTestDB(t)

	tools := Default(d, nil, "", nil)
	allowed, err := d.Roles.AllowedTools("thinking_partner")
	if err != nil {
		t.Fatalf("AllowedTools(thinking_partner): %v", err)
	}
	allowSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowSet[name] = true
	}

	for _, tool := range tools {
		name := tool.Name()
		if intentionallyExcluded[name] != "" {
			continue
		}
		if !allowSet[name] {
			t.Errorf("tool %q is in Default() but missing from thinking_partner allowlist — add a migration or add it to intentionallyExcluded with a reason", name)
		}
	}
}

// TestDefault_RespectsSeededRoleAllowlists asserts that every tool name
// referenced in a seeded role's allowlist actually exists in Default().
// This catches the drift class where a seed string references a tool that
// got renamed or deleted — the filter would silently drop it.
func TestDefault_RespectsSeededRoleAllowlists(t *testing.T) {
	d := openTestDB(t)

	tools := Default(d, nil, "", nil)
	known := make(map[string]bool, len(tools))
	for _, tool := range tools {
		known[tool.Name()] = true
	}
	// Tools constructed outside Default() because they need wiring not
	// available here (e.g. *worktree.Manager): registered in cmd/cairo
	// alongside the default set. Treat as known so role allowlists that
	// reference them don't false-positive.
	for _, name := range []string{"worktree", "merge_job"} {
		known[name] = true
	}

	roles, err := d.Roles.List()
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	for _, r := range roles {
		allowed, err := d.Roles.AllowedTools(r.Name)
		if err != nil {
			t.Fatalf("AllowedTools(%s): %v", r.Name, err)
		}
		for _, name := range allowed {
			if !known[name] {
				t.Errorf("role %q lists unknown tool %q (not in Default() or externally wired)", r.Name, name)
			}
		}
	}
}
