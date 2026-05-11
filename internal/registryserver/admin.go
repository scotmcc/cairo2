package registryserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// NewAdmin returns the admin mux pre-wired with operator-scoped endpoints.
func NewAdmin(ledger *Ledger, startedAt time.Time) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agents", handleAdminAgents(ledger))
	mux.HandleFunc("GET /agents/{id}", handleAdminAgent(ledger))
	mux.HandleFunc("GET /healthz", handleAdminHealthz(ledger, startedAt))
	mux.HandleFunc("POST /agents/{id}/revoke", handleAdminRevoke(ledger))
	mux.HandleFunc("POST /broadcast", handleAdminBroadcast(ledger))
	return mux
}

func operatorFromHeader(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Operator-Identity"))
}

func handleAdminAgents(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gate(w, r, "agent.list", "agents"); !ok {
			return
		}
		operator := operatorFromHeader(r)
		agents := []Agent{}
		if operator != "" {
			var err error
			agents, err = ledger.ListByOwner(r.Context(), operator)
			if err != nil {
				http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agents)
	}
}

func handleAdminAgent(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := gate(w, r, "agent.get", id); !ok {
			return
		}
		operator := operatorFromHeader(r)
		if operator == "" {
			http.NotFound(w, r)
			return
		}
		agent, err := ledger.GetByOwner(r.Context(), id, operator)
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		if agent == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agent)
	}
}

func handleAdminRevoke(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := gate(w, r, "agent.revoke", id); !ok {
			return
		}
		operator := operatorFromHeader(r)
		if operator == "" {
			http.Error(w, `{"error":"operator required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.Revoke(r.Context(), id, operator); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"revoked"}`))
	}
}

func handleAdminBroadcast(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gate(w, r, "agent.broadcast", "broadcast"); !ok {
			return
		}
		operator := operatorFromHeader(r)
		if operator == "" {
			http.Error(w, `{"error":"operator required"}`, http.StatusBadRequest)
			return
		}
		var body struct {
			Command string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Command == "" {
			http.Error(w, `{"error":"command required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.InsertCommand(r.Context(), operator, body.Command); err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}
}

func handleAdminHealthz(ledger *Ledger, startedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := healthzBody(r.Context(), ledger, startedAt)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
