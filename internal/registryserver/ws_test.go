package registryserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/scotmcc/cairo2/internal/protocol"
)

// newTestServer spins up an httptest.Server with a short ping interval for fast tests.
// Wires routes directly (same package) to use 1s ping interval instead of production 10s.
func newTestServer(t *testing.T) (*httptest.Server, *Ledger) {
	t.Helper()
	ledger, err := OpenLedger(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	t.Cleanup(func() { ledger.Close() })

	mux := http.NewServeMux()
	srv := &Server{ledger: ledger, tsnetSrv: nil, mux: mux}
	mux.HandleFunc("GET /healthz", srv.handleHealthz)
	mux.HandleFunc("POST /register", srv.handleRegister)
	mux.HandleFunc("GET /agents", srv.handleAgents)
	ws := &wsHandler{ledger: ledger, pingInterval: 1 * time.Second}
	mux.HandleFunc("GET /agents/{id}/stream", ws.handle)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, ledger
}

// registerTestAgent POSTs /register and returns the agent_id.
func registerTestAgent(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	body := `{"hostname":"test","version":"v0","tailnet_node":"test.ts.net"}`
	resp, err := http.Post(ts.URL+"/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /register: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /register: status %d", resp.StatusCode)
	}
	var rr protocol.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		t.Fatalf("decode /register response: %v", err)
	}
	return rr.AgentID
}

// TestWsRoundtrip: connect, receive ping within 1.5s, send pong, verify last_seen_at advanced.
func TestWsRoundtrip(t *testing.T) {
	ts, ledger := newTestServer(t)
	agentID := registerTestAgent(t, ts)

	agents, err := ledger.List(context.Background())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ledger.List: %v, len=%d", err, len(agents))
	}
	beforeAt := agents[0].LastSeenAt

	time.Sleep(10 * time.Millisecond)

	wsURL := "ws" + ts.URL[4:] + "/agents/" + agentID + "/stream"
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial WS: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	var frame protocol.Frame
	if err := wsjson.Read(ctx, conn, &frame); err != nil {
		t.Fatalf("waiting for ping: %v", err)
	}
	if frame.Type != "ping" {
		t.Fatalf("expected ping, got %q", frame.Type)
	}

	if err := wsjson.Write(context.Background(), conn, protocol.Frame{Type: "pong"}); err != nil {
		t.Fatalf("send pong: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	agents, err = ledger.List(context.Background())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ledger.List after pong: %v, len=%d", err, len(agents))
	}
	if agents[0].LastSeenAt <= beforeAt {
		t.Errorf("expected last_seen_at > %d after pong, got %d", beforeAt, agents[0].LastSeenAt)
	}
}

// TestWsUnknownAgent: opening WS for a non-existent ID returns 404.
func TestWsUnknownAgent(t *testing.T) {
	ts, _ := newTestServer(t)
	wsURL := "ws" + ts.URL[4:] + "/agents/00000000-0000-0000-0000-000000000000/stream"
	_, resp, err := websocket.Dial(context.Background(), wsURL, nil)
	if err == nil {
		t.Fatal("expected dial to fail for unknown agent_id")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected 404, got %d (err=%v)", status, err)
	}
}

// TestWsDisconnectClearsWsConnected: connect verifies ws_connected=1; close verifies ws_connected=0.
func TestWsDisconnectClearsWsConnected(t *testing.T) {
	ts, ledger := newTestServer(t)
	agentID := registerTestAgent(t, ts)

	wsURL := "ws" + ts.URL[4:] + "/agents/" + agentID + "/stream"
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial WS: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	agents, err := ledger.List(context.Background())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ledger.List: %v", err)
	}
	if agents[0].WsConnected != 1 {
		t.Fatalf("expected ws_connected=1, got %d", agents[0].WsConnected)
	}

	conn.Close(websocket.StatusNormalClosure, "")

	// Poll for ws_connected=0 (up to 500ms at 10ms granularity).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		agents, err = ledger.List(context.Background())
		if err == nil && len(agents) > 0 && agents[0].WsConnected == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("ledger.List: %v", err)
	}
	t.Fatalf("expected ws_connected=0 after close, got %d", agents[0].WsConnected)
}
