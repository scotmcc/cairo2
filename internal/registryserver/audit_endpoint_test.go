package registryserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/audit"
)

func TestAuditEndpoint_SuperAdminSeesEvents(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	if err := l.AddSuperAdmin(ctx, "admin"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	sink := audit.NewSQLiteSink(l.DB())

	// Pre-seed two audit events so there's something to read.
	_ = sink.Write(ctx, audit.Event{
		Timestamp: time.Now(),
		Actor:     "admin",
		Gate:      "access",
		Action:    "department.create",
		Target:    "departments",
		Decision:  "granted",
	})
	_ = sink.Write(ctx, audit.Event{
		Timestamp: time.Now(),
		Actor:     "bob",
		Gate:      "access",
		Action:    "broadcast",
		Target:    "broadcast",
		Decision:  "denied",
		Reason:    "super-admin required",
	})

	h := NewAdmin(l, time.Now(), sink)

	req := httptest.NewRequest("GET", "/audit", nil)
	req.Header.Set("X-Operator-Identity", "admin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for super-admin, got %d: %s", rr.Code, rr.Body.String())
	}

	var events []audit.Event
	if err := json.NewDecoder(rr.Body).Decode(&events); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestAuditEndpoint_NonSuperAdminForbidden(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	sink := audit.NewSQLiteSink(l.DB())
	h := NewAdmin(l, time.Now(), sink)

	req := httptest.NewRequest("GET", "/audit", nil)
	req.Header.Set("X-Operator-Identity", "bob") // not super-admin
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-super-admin, got %d", rr.Code)
	}
}

func TestAuditEndpoint_QueryFilters(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	if err := l.AddSuperAdmin(ctx, "admin"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	sink := audit.NewSQLiteSink(l.DB())
	_ = sink.Write(ctx, audit.Event{
		Timestamp: time.Now(),
		Actor:     "alice",
		Gate:      "access",
		Action:    "agent.list",
		Target:    "agents",
		Decision:  "granted",
	})
	_ = sink.Write(ctx, audit.Event{
		Timestamp: time.Now(),
		Actor:     "bob",
		Gate:      "access",
		Action:    "broadcast",
		Target:    "broadcast",
		Decision:  "denied",
	})

	h := NewAdmin(l, time.Now(), sink)

	req := httptest.NewRequest("GET", "/audit?actor=alice", nil)
	req.Header.Set("X-Operator-Identity", "admin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var events []audit.Event
	if err := json.NewDecoder(rr.Body).Decode(&events); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event for actor=alice, got %d", len(events))
	}
	if len(events) == 1 && events[0].Actor != "alice" {
		t.Errorf("expected alice, got %s", events[0].Actor)
	}
}

// TestDeptCreateAuditVisible checks end-to-end: after departments create,
// the audit log shows a department.create granted row for admin.
func TestDeptCreateAuditVisible(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	if err := l.AddSuperAdmin(ctx, "admin"); err != nil {
		t.Fatalf("add super-admin: %v", err)
	}

	sink := audit.NewSQLiteSink(l.DB())
	audit.SetDefaultSink(sink)
	defer audit.SetDefaultSink(audit.NopSink{})

	h := NewAdmin(l, time.Now(), sink)

	// Create a department.
	req := httptest.NewRequest("POST", "/departments", strings.NewReader(`{"name":"infra"}`))
	req.Header.Set("X-Operator-Identity", "admin")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create dept: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	// Query the audit log for admin's department.create event.
	req2 := httptest.NewRequest("GET", "/audit?actor=admin&action=department.create", nil)
	req2.Header.Set("X-Operator-Identity", "admin")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("audit query: expected 200, got %d: %s", rr2.Code, rr2.Body.String())
	}

	var events []audit.Event
	if err := json.NewDecoder(rr2.Body).Decode(&events); err != nil {
		t.Fatalf("decode audit events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 audit event for department.create, got 0")
	}
	if events[0].Decision != "granted" {
		t.Errorf("expected decision=granted, got %s", events[0].Decision)
	}
}
