package access

import (
	"context"
	"testing"
)

// fakeAssignment is a test assignment.
type fakeAssignment struct {
	AgentType string
	DeptID    string
}

// fakeLedger is a minimal in-memory implementation of Ledger for tests.
type fakeLedger struct {
	superAdmins map[string]bool
	userDepts   map[string][]string    // user -> dept IDs
	assignments map[string]*Assignment // agentID -> assignment
	agents      []Agent
}

func (f *fakeLedger) IsSuperAdmin(_ context.Context, user string) (bool, error) {
	return f.superAdmins[user], nil
}

func (f *fakeLedger) DeptsForUser(_ context.Context, user string) ([]string, error) {
	return f.userDepts[user], nil
}

func (f *fakeLedger) GetAssignment(_ context.Context, agentID string) (*Assignment, error) {
	a, ok := f.assignments[agentID]
	if !ok {
		return nil, nil
	}
	return a, nil
}

func (f *fakeLedger) GetAgentOwner(_ context.Context, agentID string) (string, bool, error) {
	for _, ag := range f.agents {
		if ag.AgentID == agentID {
			return ag.Owner, true, nil
		}
	}
	return "", false, nil
}

func (f *fakeLedger) ListAll(_ context.Context) ([]Agent, error) {
	return f.agents, nil
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{
		superAdmins: map[string]bool{},
		userDepts:   map[string][]string{},
		assignments: map[string]*Assignment{},
	}
}

func TestCanAddress(t *testing.T) {
	ctx := context.Background()

	t.Run("personal: owner allowed", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag1", Owner: "alice"}}
		d := New(l)
		ok, reason := d.CanAddress(ctx, "alice", "ag1")
		if !ok {
			t.Errorf("want allowed, got denied (reason=%q)", reason)
		}
		if reason != "owner" {
			t.Errorf("want reason=owner, got %q", reason)
		}
	})

	t.Run("personal: non-owner denied", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag1", Owner: "alice"}}
		d := New(l)
		ok, _ := d.CanAddress(ctx, "bob", "ag1")
		if ok {
			t.Error("want denied, got allowed")
		}
	})

	t.Run("personal: super-admin allowed", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag1", Owner: "alice"}}
		l.superAdmins["root"] = true
		d := New(l)
		ok, reason := d.CanAddress(ctx, "root", "ag1")
		if !ok {
			t.Errorf("want allowed, got denied (reason=%q)", reason)
		}
		if reason != "super-admin" {
			t.Errorf("want reason=super-admin, got %q", reason)
		}
	})

	t.Run("departmental: dept member allowed", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag2", Owner: "svc"}}
		l.assignments["ag2"] = &Assignment{AgentID: "ag2", AgentType: "departmental", DeptID: "dept-1"}
		l.userDepts["alice"] = []string{"dept-1"}
		d := New(l)
		ok, reason := d.CanAddress(ctx, "alice", "ag2")
		if !ok {
			t.Errorf("want allowed, got denied (reason=%q)", reason)
		}
		if reason != "dept-member" {
			t.Errorf("want reason=dept-member, got %q", reason)
		}
	})

	t.Run("departmental: non-member denied", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag2", Owner: "svc"}}
		l.assignments["ag2"] = &Assignment{AgentID: "ag2", AgentType: "departmental", DeptID: "dept-1"}
		d := New(l)
		ok, _ := d.CanAddress(ctx, "bob", "ag2")
		if ok {
			t.Error("want denied, got allowed")
		}
	})

	t.Run("departmental: super-admin allowed", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag2", Owner: "svc"}}
		l.assignments["ag2"] = &Assignment{AgentID: "ag2", AgentType: "departmental", DeptID: "dept-1"}
		l.superAdmins["root"] = true
		d := New(l)
		ok, reason := d.CanAddress(ctx, "root", "ag2")
		if !ok {
			t.Errorf("want allowed, got denied (reason=%q)", reason)
		}
		if reason != "super-admin" {
			t.Errorf("want reason=super-admin, got %q", reason)
		}
	})

	t.Run("enterprise: any identity allowed", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag3", Owner: "svc"}}
		l.assignments["ag3"] = &Assignment{AgentID: "ag3", AgentType: "enterprise"}
		d := New(l)
		for _, id := range []string{"alice", "bob", "nobody"} {
			ok, reason := d.CanAddress(ctx, id, "ag3")
			if !ok {
				t.Errorf("identity=%s want allowed, got denied (reason=%q)", id, reason)
			}
		}
	})

	t.Run("enterprise: super-admin allowed", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag3", Owner: "svc"}}
		l.assignments["ag3"] = &Assignment{AgentID: "ag3", AgentType: "enterprise"}
		l.superAdmins["root"] = true
		d := New(l)
		ok, _ := d.CanAddress(ctx, "root", "ag3")
		if !ok {
			t.Error("want allowed")
		}
	})

	t.Run("aggregate target agents: always allowed", func(t *testing.T) {
		l := newFakeLedger()
		d := New(l)
		ok, reason := d.CanAddress(ctx, "nobody", "agents")
		if !ok {
			t.Errorf("want allowed, got denied (reason=%q)", reason)
		}
		if reason != "open-catalog" {
			t.Errorf("want reason=open-catalog, got %q", reason)
		}
	})

	t.Run("aggregate target broadcast: non-super-admin denied", func(t *testing.T) {
		l := newFakeLedger()
		d := New(l)
		ok, _ := d.CanAddress(ctx, "alice", "broadcast")
		if ok {
			t.Error("want denied for non-super-admin broadcast")
		}
	})

	t.Run("aggregate target broadcast: super-admin allowed", func(t *testing.T) {
		l := newFakeLedger()
		l.superAdmins["root"] = true
		d := New(l)
		ok, reason := d.CanAddress(ctx, "root", "broadcast")
		if !ok {
			t.Errorf("want allowed, got denied (reason=%q)", reason)
		}
		if reason != "super-admin" {
			t.Errorf("want reason=super-admin, got %q", reason)
		}
	})
}

func TestListVisible(t *testing.T) {
	ctx := context.Background()

	t.Run("super-admin sees all", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{
			{AgentID: "a1", Owner: "alice"},
			{AgentID: "a2", Owner: "bob"},
		}
		l.superAdmins["root"] = true
		d := New(l)
		ids, err := d.ListVisible(ctx, "root")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 2 {
			t.Errorf("want 2, got %d", len(ids))
		}
	})

	t.Run("non-member sees no dept agent", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag2", Owner: "svc"}}
		l.assignments["ag2"] = &Assignment{AgentID: "ag2", AgentType: "departmental", DeptID: "dept-1"}
		d := New(l)
		ids, err := d.ListVisible(ctx, "bob")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 0 {
			t.Errorf("want 0, got %d: %v", len(ids), ids)
		}
	})

	t.Run("member sees dept agent", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag2", Owner: "svc"}}
		l.assignments["ag2"] = &Assignment{AgentID: "ag2", AgentType: "departmental", DeptID: "dept-1"}
		l.userDepts["alice"] = []string{"dept-1"}
		d := New(l)
		ids, err := d.ListVisible(ctx, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != "ag2" {
			t.Errorf("want [ag2], got %v", ids)
		}
	})

	t.Run("personal: owner sees own agent", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag1", Owner: "alice"}}
		d := New(l)
		ids, err := d.ListVisible(ctx, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != "ag1" {
			t.Errorf("want [ag1], got %v", ids)
		}
	})

	t.Run("personal: non-owner sees nothing", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag1", Owner: "alice"}}
		d := New(l)
		ids, err := d.ListVisible(ctx, "bob")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 0 {
			t.Errorf("want 0, got %d", len(ids))
		}
	})

	t.Run("enterprise: anyone sees it", func(t *testing.T) {
		l := newFakeLedger()
		l.agents = []Agent{{AgentID: "ag3", Owner: "svc"}}
		l.assignments["ag3"] = &Assignment{AgentID: "ag3", AgentType: "enterprise"}
		d := New(l)
		ids, err := d.ListVisible(ctx, "nobody")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != "ag3" {
			t.Errorf("want [ag3], got %v", ids)
		}
	})
}

func TestPackageLevelCanAddress(t *testing.T) {
	// Package-level stub must remain a no-op for non-ledger callers.
	ok, reason := CanAddress(context.Background(), "alice", "sessions")
	if !ok {
		t.Errorf("want allowed=true, got false")
	}
	if reason != "stub: no-op access control" {
		t.Errorf("want reason=%q, got %q", "stub: no-op access control", reason)
	}
}
