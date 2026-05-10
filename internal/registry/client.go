package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/scotmcc/cairo2/internal/protocol"
)

// ErrRevoked is returned by Register when the registry responds with 403,
// indicating the agent has been revoked and should not retry.
var ErrRevoked = errors.New("registry: agent revoked")

// Register POSTs to the registry and returns the assigned agent_id.
func Register(ctx context.Context, registryURL, agentID, version string) (string, error) {
	hostname, _ := os.Hostname()
	body, err := json.Marshal(protocol.RegisterRequest{
		AgentID:  agentID,
		Hostname: hostname,
		Version:  version,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registryURL+"/register", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return "", ErrRevoked
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned %s", resp.Status)
	}
	var rr protocol.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return "", err
	}
	return rr.AgentID, nil
}

// HeartbeatLoop re-registers every intervalSeconds until ctx is cancelled.
// Errors are logged but never returned — heartbeat failures must not crash cairo.
// If the registry responds with 403 (revoked), the loop stops permanently.
func HeartbeatLoop(ctx context.Context, registryURL, agentID, version string, intervalSeconds int) {
	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := Register(ctx, registryURL, agentID, version); err != nil {
				if errors.Is(err, ErrRevoked) {
					log.Printf("registry: agent revoked, heartbeat loop stopping")
					return
				}
				log.Printf("registry: heartbeat failed: %v", err)
			}
		}
	}
}

// LivenessStream connects to the registry's WebSocket endpoint for the given
// agentID and keeps the connection alive by responding to ping frames with
// pongs. Reconnects on disconnect until ctx is cancelled.
func LivenessStream(ctx context.Context, registryURL, agentID string) {
	wsURL := toWS(registryURL) + "/agents/" + agentID + "/stream"
	pong, _ := json.Marshal(protocol.Frame{Type: "pong"})

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		conn, wsResp, err := websocket.Dial(ctx, wsURL, nil)
		if err != nil {
			if wsResp != nil && wsResp.StatusCode == http.StatusForbidden {
				log.Printf("registry: agent revoked, liveness stream stopping")
				return
			}
			log.Printf("registry: dial %s: %v — retrying in 5s", wsURL, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				if ctx.Err() != nil {
					conn.Close(websocket.StatusNormalClosure, "")
					return
				}
				log.Printf("registry: read error: %v — reconnecting", err)
				conn.Close(websocket.StatusAbnormalClosure, "")
				break
			}

			var f protocol.Frame
			if err := json.Unmarshal(msg, &f); err != nil {
				continue
			}
			if f.Type == "ping" {
				if err := conn.Write(ctx, websocket.MessageText, pong); err != nil {
					log.Printf("registry: write pong: %v", err)
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func toWS(u string) string {
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://")
	}
	return "ws://" + strings.TrimPrefix(u, "http://")
}
