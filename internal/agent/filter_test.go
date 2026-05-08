package agent

import (
	"testing"
)

func toolNames(tools []Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

func TestFilterByAllowlist_NilReturnsAll(t *testing.T) {
	tools := []Tool{stubTool{name: "bash"}, stubTool{name: "read"}, stubTool{name: "edit"}}
	got := FilterByAllowlist(tools, nil)
	if len(got) != 3 {
		t.Errorf("nil allowlist: got %d tools, want 3", len(got))
	}
}

func TestFilterByAllowlist_EmptyReturnsAll(t *testing.T) {
	tools := []Tool{stubTool{name: "bash"}, stubTool{name: "read"}}
	got := FilterByAllowlist(tools, []string{})
	if len(got) != 2 {
		t.Errorf("empty allowlist: got %d tools, want 2", len(got))
	}
}

func TestFilterByAllowlist_Subset(t *testing.T) {
	tools := []Tool{stubTool{name: "bash"}, stubTool{name: "read"}, stubTool{name: "edit"}, stubTool{name: "write"}}
	got := FilterByAllowlist(tools, []string{"bash", "read"})
	if len(got) != 2 {
		t.Fatalf("got %v, want [bash read]", toolNames(got))
	}
	if got[0].Name() != "bash" || got[1].Name() != "read" {
		t.Errorf("got %v, want [bash read]", toolNames(got))
	}
}

func TestFilterByAllowlist_UnknownNamesIgnored(t *testing.T) {
	tools := []Tool{stubTool{name: "bash"}, stubTool{name: "read"}}
	got := FilterByAllowlist(tools, []string{"bash", "nonexistent"})
	if len(got) != 1 || got[0].Name() != "bash" {
		t.Errorf("got %v, want [bash]", toolNames(got))
	}
}

func TestToolsForSkill_EmptyKey(t *testing.T) {
	d := openTestDB(t)
	a := &Agent{db: d, tools: []Tool{stubTool{name: "bash"}, stubTool{name: "read"}}}
	got := a.toolsForSkill()
	if len(got) != 2 {
		t.Errorf("empty config key: got %d tools, want 2", len(got))
	}
}

func TestToolsForSkill_PopulatedKey(t *testing.T) {
	d := openTestDB(t)
	if err := d.Config.Set("session_skill_tools", "bash,read"); err != nil {
		t.Fatalf("Config.Set: %v", err)
	}
	a := &Agent{
		db:    d,
		tools: []Tool{stubTool{name: "bash"}, stubTool{name: "read"}, stubTool{name: "edit"}},
	}
	got := a.toolsForSkill()
	if len(got) != 2 {
		t.Fatalf("got %v, want [bash read]", toolNames(got))
	}
	if got[0].Name() != "bash" || got[1].Name() != "read" {
		t.Errorf("got %v, want [bash read]", toolNames(got))
	}
}

func TestToolsForSkill_IntersectWithRole(t *testing.T) {
	// Role-filtered set is {bash, read}; skill requests {read, write}; result must be {read}.
	d := openTestDB(t)
	if err := d.Config.Set("session_skill_tools", "read,write"); err != nil {
		t.Fatalf("Config.Set: %v", err)
	}
	a := &Agent{
		db:    d,
		tools: []Tool{stubTool{name: "bash"}, stubTool{name: "read"}},
	}
	got := a.toolsForSkill()
	if len(got) != 1 || got[0].Name() != "read" {
		t.Errorf("intersection: got %v, want [read]", toolNames(got))
	}
}
