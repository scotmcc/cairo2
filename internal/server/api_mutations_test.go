package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

func newMutationTestServer(t *testing.T, opts Options) (*Server, *sqliteopen.DB) {
	t.Helper()
	fa := newFakeAgent("test", nil)
	d, err := sqliteopen.OpenAt(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	bridge := NewBridge(fa)
	srv := New(fa, d, bridge, opts)
	return srv, d
}

func TestConfigPutRawJSON(t *testing.T) {
	srv, _ := newMutationTestServer(t, Options{})
	body := bytes.NewBufferString(`"llama3.2"`)
	req := httptest.NewRequest(http.MethodPut, "/api/config/llm_model", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["key"] != "llm_model" || resp["value"] != "llama3.2" {
		t.Errorf("unexpected response: %v", resp)
	}
}

func TestConfigPutAuthRejected(t *testing.T) {
	srv, _ := newMutationTestServer(t, Options{Auth: true, Token: "secret"})
	body := bytes.NewBufferString(`"llama3.2"`)
	req := httptest.NewRequest(http.MethodPut, "/api/config/llm_model", body)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestSessionsPatchRename(t *testing.T) {
	srv, d := newMutationTestServer(t, Options{})
	sess, err := d.Sessions.Create("original", "/tmp", "thinking_partner")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	body := bytes.NewBufferString(`{"name":"renamed"}`)
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/sessions/%d", sess.ID), body)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var updated map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated["name"] != "renamed" {
		t.Errorf("expected name 'renamed', got %v", updated["name"])
	}
}

func TestSessionsDeleteOK(t *testing.T) {
	srv, d := newMutationTestServer(t, Options{})
	sess, err := d.Sessions.Create("todelete", "/tmp", "thinking_partner")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/sessions/%d", sess.ID), nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/sessions/%d", sess.ID), nil)
	rr2 := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rr2.Code)
	}
}

func TestAspectPutUpsert(t *testing.T) {
	srv, _ := newMutationTestServer(t, Options{})
	body := bytes.NewBufferString(`{"traits":"curious, methodical","enabled":true,"position":1}`)
	req := httptest.NewRequest(http.MethodPut, "/api/consider/aspects/testaspect", body)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var aspect map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&aspect); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if aspect["name"] != "testaspect" {
		t.Errorf("expected name 'testaspect', got %v", aspect["name"])
	}
}

func TestAspectPatchToggle(t *testing.T) {
	srv, d := newMutationTestServer(t, Options{})
	if err := d.ConsiderAspects.Upsert("patchaspect", "trait", true, 0); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	body := bytes.NewBufferString(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/consider/aspects/patchaspect", body)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
}

func TestAspectDeleteOK(t *testing.T) {
	srv, d := newMutationTestServer(t, Options{})
	if err := d.ConsiderAspects.Upsert("delaspect", "trait", true, 0); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/consider/aspects/delaspect", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAspectPatchMissing404(t *testing.T) {
	srv, _ := newMutationTestServer(t, Options{})
	body := bytes.NewBufferString(`{"enabled":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/consider/aspects/doesnotexist", body)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}
