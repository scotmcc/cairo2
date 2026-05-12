package registryserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDeptCreateAndList verifies department creation and listing via admin API.
func TestDeptCreateAndList(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	if err := l.AddSuperAdmin(ctx, "admin"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	h := NewAdmin(l, time.Now(), nil)

	// Create department.
	req := httptest.NewRequest("POST", "/departments", strings.NewReader(`{"name":"infra"}`))
	req.Header.Set("X-Operator-Identity", "admin")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create dept: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// List departments.
	req2 := httptest.NewRequest("GET", "/departments", nil)
	req2.Header.Set("X-Operator-Identity", "admin")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("list depts: expected 200, got %d", rr2.Code)
	}
	var depts []Department
	if err := json.NewDecoder(rr2.Body).Decode(&depts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(depts) != 1 || depts[0].Name != "infra" {
		t.Errorf("want [{infra}], got %v", depts)
	}
}

// TestDeptCreateDuplicateName verifies conflict on duplicate name.
func TestDeptCreateDuplicateName(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	if err := l.AddSuperAdmin(ctx, "admin"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	h := NewAdmin(l, time.Now(), nil)
	createDept := func() int {
		req := httptest.NewRequest("POST", "/departments", strings.NewReader(`{"name":"infra"}`))
		req.Header.Set("X-Operator-Identity", "admin")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if code := createDept(); code != http.StatusCreated {
		t.Fatalf("first create: want 201, got %d", code)
	}
	if code := createDept(); code != http.StatusConflict {
		t.Errorf("second create: want 409, got %d", code)
	}
}

// TestDeptCreateForbiddenForNonSuperAdmin verifies non-super-admin cannot create departments.
func TestDeptCreateForbiddenForNonSuperAdmin(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	h := NewAdmin(l, time.Now(), nil)
	req := httptest.NewRequest("POST", "/departments", strings.NewReader(`{"name":"infra"}`))
	req.Header.Set("X-Operator-Identity", "alice") // not super-admin
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rr.Code)
	}
}

// TestAddMemberAndAssignAgent is the end-to-end test:
// non-member sees 0 agents; member sees 1 after assign.
func TestAddMemberAndAssignAgent(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	if err := l.AddSuperAdmin(ctx, "admin"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}
	agentID, _, err := l.Register(ctx, "", "svc-account", "svc-host", "svc.ts.net", "v1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	h := NewAdmin(l, time.Now(), nil)

	// Create dept.
	req := httptest.NewRequest("POST", "/departments", strings.NewReader(`{"name":"infra"}`))
	req.Header.Set("X-Operator-Identity", "admin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create dept: %d", rr.Code)
	}
	var dept Department
	if err := json.NewDecoder(strings.NewReader(rr.Body.String())).Decode(&dept); err != nil {
		t.Fatalf("decode dept: %v", err)
	}

	// Assign agent as departmental in infra.
	body := `{"agent_type":"departmental","dept_id":"` + dept.ID + `"}`
	req2 := httptest.NewRequest("POST", "/agents/"+agentID+"/assign", strings.NewReader(body))
	req2.Header.Set("X-Operator-Identity", "admin")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("assign: expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}

	// Add alice to infra.
	addBody := `{"user":"alice","role":"developer"}`
	req3 := httptest.NewRequest("POST", "/departments/"+dept.ID+"/members", strings.NewReader(addBody))
	req3.Header.Set("X-Operator-Identity", "admin")
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusCreated {
		t.Fatalf("add member: expected 201, got %d: %s", rr3.Code, rr3.Body.String())
	}

	listAgents := func(operator string) []Agent {
		r := httptest.NewRequest("GET", "/agents", nil)
		r.Header.Set("X-Operator-Identity", operator)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("list agents (%s): expected 200, got %d", operator, w.Code)
		}
		var agents []Agent
		if err := json.NewDecoder(w.Body).Decode(&agents); err != nil {
			t.Fatalf("decode agents: %v", err)
		}
		return agents
	}

	// Non-member bob: should see 0 agents.
	bobAgents := listAgents("bob")
	if len(bobAgents) != 0 {
		t.Errorf("bob (non-member) should see 0 agents, got %d: %v", len(bobAgents), bobAgents)
	}

	// Member alice: should see the infra agent.
	aliceAgents := listAgents("alice")
	if len(aliceAgents) != 1 {
		t.Errorf("alice (member) should see 1 agent, got %d: %v", len(aliceAgents), aliceAgents)
	}
	if len(aliceAgents) == 1 && aliceAgents[0].AgentID != agentID {
		t.Errorf("alice sees wrong agent: %s", aliceAgents[0].AgentID)
	}
}

// TestSuperAdminIdempotent verifies AddSuperAdmin is idempotent.
func TestSuperAdminIdempotent(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := l.AddSuperAdmin(ctx, "admin"); err != nil {
			t.Fatalf("AddSuperAdmin call %d: %v", i+1, err)
		}
	}
	n, err := l.CountSuperAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("want 1 super-admin, got %d", n)
	}
}
