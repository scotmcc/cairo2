package registryserver

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/scotmcc/cairo2/internal/access"
	"github.com/scotmcc/cairo2/internal/authn"
	"github.com/scotmcc/cairo2/internal/protocol"
)

type wsHandler struct {
	ledger       *Ledger
	decider      *access.Decider
	resolver     authn.Resolver
	pingInterval time.Duration
}

func newWsHandler(ledger *Ledger, decider *access.Decider, resolver authn.Resolver) *wsHandler {
	return &wsHandler{ledger: ledger, decider: decider, resolver: resolver, pingInterval: 10 * time.Second}
}

func (h *wsHandler) handle(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	if _, ok := gateWith(h.decider, h.resolver, w, r, "agent.stream", agentID); !ok {
		return
	}

	// Verify agent exists; Touch also bumps last_seen_at.
	if err := h.ledger.Touch(r.Context(), agentID); err != nil {
		if err == sql.ErrNoRows {
			log.Printf("ws: register_unknown agent_id=%s", agentID)
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		} else {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		}
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("ws: agent_id=%s event=accept_error err=%v", agentID, err)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	defer func() {
		_ = h.ledger.SetWsConnected(context.Background(), agentID, false)
		conn.Close(websocket.StatusNormalClosure, "")
		log.Printf("ws: agent_id=%s event=close", agentID)
	}()

	if err := h.ledger.SetWsConnected(ctx, agentID, true); err != nil {
		log.Printf("ws: agent_id=%s event=set_connected_error err=%v", agentID, err)
		return
	}
	log.Printf("ws: agent_id=%s event=open", agentID)

	// Ping loop.
	go func() {
		t := time.NewTicker(h.pingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				ping := protocol.Frame{Type: "ping"}
				if err := wsjson.Write(ctx, conn, ping); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Read loop — runs until the client disconnects or context is done.
	for {
		var frame protocol.Frame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			reason := "read_error"
			if ctx.Err() != nil {
				reason = "context_done"
			} else if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				reason = "client_close"
			}
			log.Printf("ws: agent_id=%s event=close reason=%s", agentID, reason)
			return
		}
		if frame.Type == "pong" {
			log.Printf("ws: agent_id=%s event=pong", agentID)
			if err := h.ledger.Touch(ctx, agentID); err != nil {
				log.Printf("ws: agent_id=%s touch_error=%v", agentID, err)
			}
			if status, err := h.ledger.GetStatus(ctx, agentID); err == nil && status == "revoked" {
				log.Printf("ws: agent_id=%s event=close reason=revoked", agentID)
				cancel()
			}
		} else {
			log.Printf("ws: agent_id=%s event=unknown_frame type=%s", agentID, frame.Type)
		}
	}
}
