package tools

import (
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
)

func execMemoryTool(t *testing.T, d *db.DB, args map[string]any) agent.ToolResult {
	t.Helper()
	embed := &EmbedClient{Embedder: stubEmbedder{}, Model: "stub-embed-model"}
	tool := MemoryTool(d, embed)
	return tool.Execute(args, &agent.ToolContext{DB: d, DisciplineMode: agent.DisciplineFull})
}

func TestMemoryToolConsolidated_AddWritesMemory(t *testing.T) {
	d := openTestDB(t)

	res := execMemoryTool(t, d, map[string]any{"action": "add", "content": "consolidated memory"})
	if res.IsError {
		t.Fatalf("add failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory saved") {
		t.Errorf("unexpected response: %s", res.Content)
	}

	// Verify via direct DB read.
	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	if len(mems) == 0 {
		t.Fatal("add wrote nothing to memories table")
	}
	found := false
	for _, m := range mems {
		if m.Content == "consolidated memory" {
			found = true
			break
		}
	}
	if !found {
		t.Error("added memory content not found in DB")
	}
}

func TestMemoryToolConsolidated_AddRequiresContent(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryTool(t, d, map[string]any{"action": "add"})
	if !res.IsError {
		t.Error("add without content should be an error")
	}
}

func TestMemoryToolConsolidated_BadAction(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryTool(t, d, map[string]any{"action": "frobnicate"})
	if !res.IsError {
		t.Error("unknown action should be an error")
	}
	if !strings.Contains(res.Content, "unknown action") {
		t.Errorf("error should mention unknown action: %s", res.Content)
	}
}

func TestMemoryToolConsolidated_DeleteSoftDeletes(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory.
	addRes := execMemoryTool(t, d, map[string]any{"action": "add", "content": "memory to be deleted"})
	if addRes.IsError {
		t.Fatalf("add failed: %s", addRes.Content)
	}

	// Retrieve the seeded memory to get its ID.
	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	if len(mems) == 0 {
		t.Fatal("no memories found after add")
	}
	var memID int64
	for _, m := range mems {
		if m.Content == "memory to be deleted" {
			memID = m.ID
			break
		}
	}
	if memID == 0 {
		t.Fatal("seeded memory not found")
	}

	// Delete via memory_tool.
	delRes := execMemoryTool(t, d, map[string]any{"action": "delete", "id": memID})
	if delRes.IsError {
		t.Fatalf("delete failed: %s", delRes.Content)
	}
	if !strings.Contains(delRes.Content, "deleted") {
		t.Errorf("unexpected delete response: %s", delRes.Content)
	}

	// Verify Get returns no row (deleted_at IS NULL filter excludes it).
	got, err := d.Memories.Get(memID)
	if err == nil && got != nil {
		t.Error("Get should return nothing for a soft-deleted memory")
	}

	// Verify it no longer appears in AllContent (which filters deleted_at IS NULL).
	mems2, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent after delete: %v", err)
	}
	for _, m := range mems2 {
		if m.ID == memID {
			t.Error("deleted memory still returned by AllContent")
		}
	}
}

func TestMemoryToolConsolidated_DeleteRequiresID(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryTool(t, d, map[string]any{"action": "delete"})
	if !res.IsError {
		t.Error("delete without id should be an error")
	}
	if !strings.Contains(res.Content, "id is required") {
		t.Errorf("error should mention id: %s", res.Content)
	}
}

func TestMemoryToolConsolidated_MissingAction(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryTool(t, d, map[string]any{})
	if !res.IsError {
		t.Error("missing action should be an error")
	}
}

func TestMemoryToolConsolidated_SearchScopeMemories(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory.
	_ = execMemoryTool(t, d, map[string]any{"action": "add", "content": "scope test memory"})

	res := execMemoryTool(t, d, map[string]any{
		"action": "search",
		"query":  "scope test",
		"scope":  "memories",
		"mode":   "semantic",
	})
	if res.IsError {
		t.Fatalf("search errored: %s", res.Content)
	}
	rows, ok := res.Details.([]memoryResult)
	if !ok {
		t.Fatalf("details type: want []memoryResult, got %T", res.Details)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one result")
	}
	for _, r := range rows {
		if r.Source != "memories" {
			t.Errorf("got source %q, want memories", r.Source)
		}
	}
}

func TestMemoryToolConsolidated_SearchScopeAll(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory so we get at least one result.
	_ = execMemoryTool(t, d, map[string]any{"action": "add", "content": "all scope memory"})

	res := execMemoryTool(t, d, map[string]any{
		"action": "search",
		"query":  "scope",
		"scope":  "all",
		"mode":   "semantic",
	})
	// scope=all searches memories, facts, summaries; facts/summaries may be empty on a fresh DB.
	// The important thing is it doesn't error and returns at least the seeded memory.
	if res.IsError {
		t.Fatalf("search all errored: %s", res.Content)
	}
	if res.Details == nil {
		t.Fatal("expected non-nil Details")
	}
	rows := res.Details.([]memoryResult)
	if len(rows) == 0 {
		t.Fatal("expected at least one result from all-scope search")
	}
}

func TestMemoryToolConsolidated_SearchRequiresQuery(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryTool(t, d, map[string]any{"action": "search"})
	if !res.IsError {
		t.Error("search without query should be an error")
	}
}

// TestMemoryTool_DedupWarnsOnNearDuplicate seeds a memory and attempts to add
// another with identical embedding (same content length via stubEmbedder), then
// verifies the warning is returned and no second row is written.
func TestMemoryTool_DedupWarnsOnNearDuplicate(t *testing.T) {
	d := openTestDB(t)

	// Both strings are 28 runes so stubEmbedder returns [28.0, 1.0] for both —
	// cosine similarity = 1.0, well above the 0.85 default threshold.
	const original = "near-duplicate test memory A"
	const duplicate = "near-duplicate test memory B"

	// Seed the original.
	res := execMemoryTool(t, d, map[string]any{"action": "add", "content": original})
	if res.IsError {
		t.Fatalf("seed add failed: %s", res.Content)
	}

	// Attempt to add the near-duplicate.
	res = execMemoryTool(t, d, map[string]any{"action": "add", "content": duplicate})
	if res.IsError {
		t.Fatalf("dedup should warn, not error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "near-duplicate found") {
		t.Errorf("expected near-duplicate warning, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "force=true") {
		t.Errorf("warning should mention force=true, got: %s", res.Content)
	}

	// The duplicate must not have been written.
	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	for _, m := range mems {
		if m.Content == duplicate {
			t.Error("near-duplicate should not have been written to the DB")
		}
	}
}

// TestMemoryTool_DedupForceOverride verifies that force=true bypasses dedup and
// writes the memory even when a near-duplicate already exists.
func TestMemoryTool_DedupForceOverride(t *testing.T) {
	d := openTestDB(t)

	const original = "near-duplicate test memory A"
	const duplicate = "near-duplicate test memory B"

	// Seed the original.
	res := execMemoryTool(t, d, map[string]any{"action": "add", "content": original})
	if res.IsError {
		t.Fatalf("seed add failed: %s", res.Content)
	}

	// Add duplicate with force=true — must succeed and write.
	res = execMemoryTool(t, d, map[string]any{"action": "add", "content": duplicate, "force": true})
	if res.IsError {
		t.Fatalf("force add failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory saved") {
		t.Errorf("expected 'memory saved', got: %s", res.Content)
	}

	// Verify both rows exist.
	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	foundOriginal, foundDuplicate := false, false
	for _, m := range mems {
		if m.Content == original {
			foundOriginal = true
		}
		if m.Content == duplicate {
			foundDuplicate = true
		}
	}
	if !foundOriginal {
		t.Error("original memory not found in DB")
	}
	if !foundDuplicate {
		t.Error("force-added duplicate should be in DB but was not found")
	}
}

func execMemoryToolWithRole(t *testing.T, d *db.DB, role string, args map[string]any) agent.ToolResult {
	t.Helper()
	embed := &EmbedClient{Embedder: stubEmbedder{}, Model: "stub-embed-model"}
	tool := MemoryTool(d, embed)
	return tool.Execute(args, &agent.ToolContext{
		DB:             d,
		DisciplineMode: agent.DisciplineFull,
		Session:        &db.Session{Role: role},
	})
}

func TestMemoryToolConsolidated_AddBlockedForCoderRole(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryToolWithRole(t, d, "coder", map[string]any{"action": "add", "content": "coder should not write"})
	if res.IsError {
		t.Fatalf("blocked response should not set IsError: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory write skipped") {
		t.Errorf("expected skip message, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"coder"`) {
		t.Errorf("expected role name in message, got: %s", res.Content)
	}
}

func TestMemoryToolConsolidated_AddBlockedForReviewerRole(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryToolWithRole(t, d, "reviewer", map[string]any{"action": "add", "content": "reviewer should not write"})
	if strings.Contains(res.Content, "memory saved") {
		t.Errorf("reviewer role should not be able to write memories: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory write skipped") {
		t.Errorf("expected skip message for reviewer, got: %s", res.Content)
	}
}

func TestMemoryToolConsolidated_AddAllowedForThinkingPartnerRole(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryToolWithRole(t, d, "thinking_partner", map[string]any{"action": "add", "content": "thinking partner memory"})
	if res.IsError {
		t.Fatalf("thinking_partner add failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory saved") {
		t.Errorf("expected memory saved, got: %s", res.Content)
	}
}

func TestMemoryToolConsolidated_AddAllowedForOrchestratorRole(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryToolWithRole(t, d, "orchestrator", map[string]any{"action": "add", "content": "orchestrator memory"})
	if res.IsError {
		t.Fatalf("orchestrator add failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory saved") {
		t.Errorf("expected memory saved, got: %s", res.Content)
	}
}

func TestMemoryToolConsolidated_DeleteBlockedForCoderRole(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory as thinking_partner.
	addRes := execMemoryToolWithRole(t, d, "thinking_partner", map[string]any{"action": "add", "content": "memory coder cannot delete"})
	if addRes.IsError {
		t.Fatalf("seed add failed: %s", addRes.Content)
	}

	// Get the memory ID.
	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	var memID int64
	for _, m := range mems {
		if m.Content == "memory coder cannot delete" {
			memID = m.ID
			break
		}
	}
	if memID == 0 {
		t.Fatal("seeded memory not found")
	}

	// Attempt delete as coder — must be skipped.
	res := execMemoryToolWithRole(t, d, "coder", map[string]any{"action": "delete", "id": memID})
	if res.IsError {
		t.Fatalf("blocked delete should not set IsError: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory delete skipped") {
		t.Errorf("expected skip message, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"coder"`) {
		t.Errorf("expected role name in message, got: %s", res.Content)
	}

	// Memory must still exist (not soft-deleted).
	got, err := d.Memories.Get(memID)
	if err != nil || got == nil {
		t.Error("memory should still exist after blocked delete")
	}
}

func TestMemoryToolConsolidated_DeleteAllowedForThinkingPartnerRole(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory as thinking_partner.
	addRes := execMemoryToolWithRole(t, d, "thinking_partner", map[string]any{"action": "add", "content": "thinking partner delete test"})
	if addRes.IsError {
		t.Fatalf("seed add failed: %s", addRes.Content)
	}

	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	var memID int64
	for _, m := range mems {
		if m.Content == "thinking partner delete test" {
			memID = m.ID
			break
		}
	}
	if memID == 0 {
		t.Fatal("seeded memory not found")
	}

	// Delete as thinking_partner — must succeed.
	res := execMemoryToolWithRole(t, d, "thinking_partner", map[string]any{"action": "delete", "id": memID})
	if res.IsError {
		t.Fatalf("delete failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "deleted") {
		t.Errorf("expected deleted confirmation, got: %s", res.Content)
	}

	// Memory must be soft-deleted (Get returns nothing).
	got, err := d.Memories.Get(memID)
	if err == nil && got != nil {
		t.Error("Get should return nothing for a soft-deleted memory")
	}
}

func TestMemoryToolConsolidated_DeleteAllowedForOrchestratorRole(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory as thinking_partner (orchestrator can also seed, but thinking_partner is fine).
	addRes := execMemoryToolWithRole(t, d, "thinking_partner", map[string]any{"action": "add", "content": "orchestrator delete test"})
	if addRes.IsError {
		t.Fatalf("seed add failed: %s", addRes.Content)
	}

	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	var memID int64
	for _, m := range mems {
		if m.Content == "orchestrator delete test" {
			memID = m.ID
			break
		}
	}
	if memID == 0 {
		t.Fatal("seeded memory not found")
	}

	// Delete as orchestrator — must succeed.
	res := execMemoryToolWithRole(t, d, "orchestrator", map[string]any{"action": "delete", "id": memID})
	if res.IsError {
		t.Fatalf("delete failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "deleted") {
		t.Errorf("expected deleted confirmation, got: %s", res.Content)
	}

	// Memory must be soft-deleted.
	got, err := d.Memories.Get(memID)
	if err == nil && got != nil {
		t.Error("Get should return nothing for a soft-deleted memory")
	}
}

func TestMemoryToolConsolidated_AddAllowedWhenNoRole(t *testing.T) {
	d := openTestDB(t)
	// No session — falls through the role check; behavior unchanged.
	res := execMemoryTool(t, d, map[string]any{"action": "add", "content": "no-role memory"})
	if res.IsError {
		t.Fatalf("no-role add failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory saved") {
		t.Errorf("expected memory saved, got: %s", res.Content)
	}
}

func TestMemoryToolPin_RoundTrip(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory via the tool.
	addRes := execMemoryTool(t, d, map[string]any{"action": "add", "content": "pin round-trip memory"})
	if addRes.IsError {
		t.Fatalf("add failed: %s", addRes.Content)
	}

	// Retrieve the seeded memory to get its ID.
	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	var memID int64
	for _, m := range mems {
		if m.Content == "pin round-trip memory" {
			memID = m.ID
			break
		}
	}
	if memID == 0 {
		t.Fatal("seeded memory not found")
	}

	// Pin via memory_tool.
	pinRes := execMemoryTool(t, d, map[string]any{"action": "pin", "id": memID})
	if pinRes.IsError {
		t.Fatalf("pin failed: %s", pinRes.Content)
	}

	// Verify PinnedAt is set.
	got, err := d.Memories.Get(memID)
	if err != nil || got == nil {
		t.Fatalf("Get after pin: %v", err)
	}
	if got.PinnedAt == nil {
		t.Error("expected PinnedAt to be set after pin, got nil")
	}
}

func TestMemoryToolUnpin(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory and pin it directly via DB helper.
	addRes := execMemoryTool(t, d, map[string]any{"action": "add", "content": "unpin test memory"})
	if addRes.IsError {
		t.Fatalf("add failed: %s", addRes.Content)
	}
	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	var memID int64
	for _, m := range mems {
		if m.Content == "unpin test memory" {
			memID = m.ID
			break
		}
	}
	if memID == 0 {
		t.Fatal("seeded memory not found")
	}
	if err := d.Memories.Pin(memID); err != nil {
		t.Fatalf("DB Pin: %v", err)
	}

	// Confirm pinned before unpin.
	before, err := d.Memories.Get(memID)
	if err != nil || before == nil {
		t.Fatalf("Get before unpin: %v", err)
	}
	if before.PinnedAt == nil {
		t.Fatal("expected PinnedAt set before unpin test")
	}

	// Unpin via memory_tool.
	unpinRes := execMemoryTool(t, d, map[string]any{"action": "unpin", "id": memID})
	if unpinRes.IsError {
		t.Fatalf("unpin failed: %s", unpinRes.Content)
	}

	// Verify PinnedAt is cleared.
	got, err := d.Memories.Get(memID)
	if err != nil || got == nil {
		t.Fatalf("Get after unpin: %v", err)
	}
	if got.PinnedAt != nil {
		t.Errorf("expected PinnedAt nil after unpin, got %v", got.PinnedAt)
	}
}

func TestMemoryToolPin_BlockedForCoder(t *testing.T) {
	d := openTestDB(t)

	// Seed a memory as thinking_partner so we have an ID to try to pin.
	addRes := execMemoryToolWithRole(t, d, "thinking_partner", map[string]any{"action": "add", "content": "coder cannot pin"})
	if addRes.IsError {
		t.Fatalf("seed add failed: %s", addRes.Content)
	}
	mems, err := d.Memories.AllContent()
	if err != nil {
		t.Fatalf("AllContent: %v", err)
	}
	var memID int64
	for _, m := range mems {
		if m.Content == "coder cannot pin" {
			memID = m.ID
			break
		}
	}
	if memID == 0 {
		t.Fatal("seeded memory not found")
	}

	// Attempt pin as coder — must be skipped, not errored.
	res := execMemoryToolWithRole(t, d, "coder", map[string]any{"action": "pin", "id": memID})
	if res.IsError {
		t.Fatalf("blocked pin should not set IsError: %s", res.Content)
	}
	if !strings.Contains(res.Content, "memory pin skipped") {
		t.Errorf("expected skip message, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"coder"`) {
		t.Errorf("expected role name in message, got: %s", res.Content)
	}

	// Memory must remain unpinned.
	got, err := d.Memories.Get(memID)
	if err != nil || got == nil {
		t.Fatalf("Get after blocked pin: %v", err)
	}
	if got.PinnedAt != nil {
		t.Error("memory should not be pinned after blocked pin attempt")
	}
}

func TestMemoryToolPin_MissingID(t *testing.T) {
	d := openTestDB(t)
	res := execMemoryTool(t, d, map[string]any{"action": "pin"})
	if !res.IsError {
		t.Error("pin without id should be an error")
	}
	if !strings.Contains(res.Content, "id is required") {
		t.Errorf("error should mention id is required: %s", res.Content)
	}
}
