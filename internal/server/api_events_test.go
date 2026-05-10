package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

func newEventsTestServer(t *testing.T, opts Options) (*Server, *fakeAgent) {
	t.Helper()
	fa := newFakeAgent("test", nil)
	d, err := sqliteopen.OpenAt(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	bridge := NewBridge(fa)
	return New(fa, d, bridge, opts), fa
}

func TestEventsAuthRejected(t *testing.T) {
	srv, _ := newEventsTestServer(t, Options{Auth: true, Token: "secret"})
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestEventsConnectAndReceive(t *testing.T) {
	t.Parallel()
	srv, fa := newEventsTestServer(t, Options{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.mux.ServeHTTP(rr, req)
	}()

	// Wait deterministically for the handler to subscribe before publishing.
	for fa.bus.SubscriberCount() < 1 {
		runtime.Gosched()
	}

	fa.bus.Publish(agent.Event{Type: agent.EventAgentStart})

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler did not exit after context cancel")
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream content-type, got %q", ct)
	}
	if !strings.Contains(rr.Body.String(), "agent_start") {
		t.Errorf("expected agent_start in SSE body, got: %q", rr.Body.String())
	}
}

func TestEventsFiltersTokens(t *testing.T) {
	t.Parallel()
	srv, fa := newEventsTestServer(t, Options{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.mux.ServeHTTP(rr, req)
	}()

	for fa.bus.SubscriberCount() < 1 {
		runtime.Gosched()
	}

	fa.bus.Publish(agent.Event{
		Type:    agent.EventTokens,
		Payload: agent.PayloadTokens{Token: "filtered-token"},
	})

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler did not exit after context cancel")
	}

	if strings.Contains(rr.Body.String(), "tokens") {
		t.Errorf("EventTokens should be filtered but appeared in SSE body: %q", rr.Body.String())
	}
}
