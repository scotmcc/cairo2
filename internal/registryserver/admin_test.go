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

func TestAdminAgentsScopedByOperator(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	_, _, _ = l.Register(ctx, "", "alice", "a-host", "a.ts.net", "v1")
	_, _, _ = l.Register(ctx, "", "bob", "b-host", "b.ts.net", "v1")
	// alice is a super-admin so ListVisible returns all agents.
	if err := l.AddSuperAdmin(ctx, "alice"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("GET", "/agents", nil)
	req.Header.Set("X-Operator-Identity", "alice")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var agents []Agent
	if err := json.NewDecoder(rr.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Super-admin alice sees all agents (alice + bob).
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents for super-admin alice, got %d", len(agents))
	}
}

func TestAdminAgentsHeaderMissing(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	_, _, _ = l.Register(ctx, "", "alice", "a-host", "a.ts.net", "v1")

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("GET", "/agents", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var agents []Agent
	if err := json.NewDecoder(rr.Body).Decode(&agents); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected empty array without header, got %d agents", len(agents))
	}
}

func TestAdminGetAgent(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	aliceID, _, _ := l.Register(ctx, "", "alice", "a-host", "a.ts.net", "v1")
	_, _, _ = l.Register(ctx, "", "bob", "b-host", "b.ts.net", "v1")
	// alice is super-admin so gate passes for any target.
	if err := l.AddSuperAdmin(ctx, "alice"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	h := NewAdmin(l, time.Now(), nil)

	t.Run("hit", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/agents/"+aliceID, nil)
		req.Header.Set("X-Operator-Identity", "alice")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var a Agent
		if err := json.NewDecoder(rr.Body).Decode(&a); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if a.AgentID != aliceID {
			t.Errorf("expected agent_id=%s, got %s", aliceID, a.AgentID)
		}
	})

	t.Run("miss-by-id", func(t *testing.T) {
		// Super-admin alice passes the gate; non-existent agent → 404.
		req := httptest.NewRequest("GET", "/agents/00000000-0000-0000-0000-000000000000", nil)
		req.Header.Set("X-Operator-Identity", "alice")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("miss-by-owner", func(t *testing.T) {
		// bob (not super-admin, not owner) is denied at the gate → 403.
		req := httptest.NewRequest("GET", "/agents/"+aliceID, nil)
		req.Header.Set("X-Operator-Identity", "bob")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Fatalf("expected 403 (gate denies non-owner), got %d", rr.Code)
		}
	})
}

func TestAdminRevoke(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	aliceID, _, err := l.Register(ctx, "", "alice", "a-host", "a.ts.net", "v1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// alice is the owner of her agent; the gate allows owner to revoke own agent.
	// Seed alice as super-admin to be explicit.
	if err := l.AddSuperAdmin(ctx, "alice"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("POST", "/agents/"+aliceID+"/revoke", nil)
	req.Header.Set("X-Operator-Identity", "alice")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"revoked"`) {
		t.Errorf("expected revoked status in body, got %q", rr.Body.String())
	}
	status, err := l.GetStatus(ctx, aliceID)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status != "revoked" {
		t.Errorf("expected status=revoked, got %q", status)
	}
}

func TestAdminRevokeNotFound(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	// Seed alice as super-admin so the gate passes; Revoke should return 404.
	if err := l.AddSuperAdmin(ctx, "alice"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("POST", "/agents/00000000-0000-0000-0000-000000000000/revoke", nil)
	req.Header.Set("X-Operator-Identity", "alice")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestAdminBroadcast(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	// broadcast is super-admin-only; seed alice as super-admin.
	if err := l.AddSuperAdmin(ctx, "alice"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("POST", "/broadcast", strings.NewReader(`{"command":"test"}`))
	req.Header.Set("X-Operator-Identity", "alice")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"queued"`) {
		t.Errorf("expected queued status in body, got %q", rr.Body.String())
	}
	var count int
	if err := l.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM commands`).Scan(&count); err != nil {
		t.Fatalf("count commands: %v", err)
	}
	if count < 1 {
		t.Errorf("expected commands >= 1, got %d", count)
	}
}

func TestAdminHealthz(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body healthzResponse
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" && body.Status != "degraded" {
		t.Errorf("unexpected status: %q", body.Status)
	}
}

// --- New negative tests (carryover from 4.2) ---

func TestAdminBroadcastDeniedNonSuperAdmin(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("POST", "/broadcast", strings.NewReader(`{"command":"hello"}`))
	req.Header.Set("X-Operator-Identity", "bob") // bob has no role
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-super-admin broadcast, got %d", rr.Code)
	}
}

func TestAdminRevokeDeniedNonOwner(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	aliceID, _, err := l.Register(ctx, "", "alice", "a-host", "a.ts.net", "v1")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	// Register bob so he exists but is not alice's agent owner.
	_, _, _ = l.Register(ctx, "", "bob", "b-host", "b.ts.net", "v1")

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("POST", "/agents/"+aliceID+"/revoke", nil)
	req.Header.Set("X-Operator-Identity", "bob") // bob is not owner, not super-admin
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner revoke, got %d", rr.Code)
	}
}

func TestAdminGetAgentDeniedNonOwner(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	aliceID, _, err := l.Register(ctx, "", "alice", "a-host", "a.ts.net", "v1")
	if err != nil {
		t.Fatalf("register alice: %v", err)
	}
	_, _, _ = l.Register(ctx, "", "bob", "b-host", "b.ts.net", "v1")

	h := NewAdmin(l, time.Now(), nil)

	req := httptest.NewRequest("GET", "/agents/"+aliceID, nil)
	req.Header.Set("X-Operator-Identity", "bob") // bob is not owner, not super-admin
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner get-agent, got %d", rr.Code)
	}
}
