package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

func newReadTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	fa := newFakeAgent("test", nil)
	d, err := sqliteopen.OpenAt(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	bridge := NewBridge(fa)
	srv := New(fa, d, bridge, opts)
	return srv
}

func TestAPIHealthPublic(t *testing.T) {
	srv := newReadTestServer(t, Options{Auth: true, Token: "secret"})
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 without token, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("expected ok=true, got %v", body["ok"])
	}
	if _, ok := body["version"]; !ok {
		t.Error("missing version field")
	}
}

func TestConfigSnapshotAuth(t *testing.T) {
	srv := newReadTestServer(t, Options{Auth: true, Token: "secret"})
	req := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rr.Code)
	}
}

func TestConfigSnapshotOK(t *testing.T) {
	srv := newReadTestServer(t, Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/config/snapshot", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"config", "roles", "considerAspects"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in snapshot", key)
		}
	}
}

func TestSessionsListOK(t *testing.T) {
	srv := newReadTestServer(t, Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body []any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestSessionsGetNotFound(t *testing.T) {
	srv := newReadTestServer(t, Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/99999", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestSessionsMessagesOK(t *testing.T) {
	fa := newFakeAgent("test", nil)
	d, err := sqliteopen.OpenAt(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	sess, err := d.Sessions.Create("test", "/tmp", "thinking-partner")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := d.Messages.Add(sess.ID, "user", fmt.Sprintf("msg %d", i), "", "", ""); err != nil {
			t.Fatalf("add message: %v", err)
		}
	}

	bridge := NewBridge(fa)
	srv := New(fa, d, bridge, Options{})

	url := fmt.Sprintf("/api/sessions/%d/messages?limit=2", sess.ID)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var msgs []any
	if err := json.NewDecoder(rr.Body).Decode(&msgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages (limit=2), got %d", len(msgs))
	}
}

func TestMetricsOK(t *testing.T) {
	srv := newReadTestServer(t, Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"sessions", "memories", "jobs"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in metrics", key)
		}
	}
}
