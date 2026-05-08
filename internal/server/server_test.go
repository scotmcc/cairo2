package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

func openTestDB(t *testing.T) *sqliteopen.DB {
	t.Helper()
	d, err := sqliteopen.OpenAt(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func newTestServer(t *testing.T, opts Options) (*Server, *SessionBridge) {
	t.Helper()
	fa := newFakeAgent("test-response", []string{"test", "-", "response"})
	bridge := NewBridge(fa)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bridge.Start(ctx)
	d := openTestDB(t)
	srv := New(fa, d, bridge, opts)
	return srv, bridge
}

// TestHealthz_AlwaysOK verifies /healthz returns 200 regardless of auth.
func TestHealthz_AlwaysOK(t *testing.T) {
	for _, auth := range []bool{false, true} {
		srv, _ := newTestServer(t, Options{Auth: auth, Token: "secret"})
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		srv.mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("auth=%v: expected 200, got %d", auth, rr.Code)
		}
	}
}

// TestAuth_Disabled_PassesThrough verifies all requests pass when auth is off.
func TestAuth_Disabled_PassesThrough(t *testing.T) {
	srv, _ := newTestServer(t, Options{Auth: false})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	// Stub returns 501, not 401 — meaning auth did not block it.
	if rr.Code == http.StatusUnauthorized {
		t.Errorf("expected request to pass auth-disabled server, got 401")
	}
}

// TestAuth_Enabled_ValidToken_Passes verifies a correct token is accepted.
func TestAuth_Enabled_ValidToken_Passes(t *testing.T) {
	srv, _ := newTestServer(t, Options{Auth: true, Token: "goodtoken"})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer goodtoken")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized {
		t.Errorf("valid token was rejected (got 401)")
	}
}

// TestAuth_Enabled_BadToken_Returns401 verifies a wrong token is rejected.
func TestAuth_Enabled_BadToken_Returns401(t *testing.T) {
	srv, _ := newTestServer(t, Options{Auth: true, Token: "goodtoken"})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("bad token: expected 401, got %d", rr.Code)
	}
}

// TestAuth_Enabled_MissingToken_Returns401 verifies missing auth header is rejected.
func TestAuth_Enabled_MissingToken_Returns401(t *testing.T) {
	srv, _ := newTestServer(t, Options{Auth: true, Token: "goodtoken"})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("missing token: expected 401, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "unauthorized" {
		t.Errorf("expected error=unauthorized, got %q", body["error"])
	}
}

// TestAuth_Enabled_EmptyToken_Returns401 verifies an empty Bearer token is rejected.
func TestAuth_Enabled_EmptyToken_Returns401(t *testing.T) {
	srv, _ := newTestServer(t, Options{Auth: true, Token: "goodtoken"})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("empty bearer: expected 401, got %d", rr.Code)
	}
}
