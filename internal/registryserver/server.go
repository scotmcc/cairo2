package registryserver

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tsnet"

	"github.com/scotmcc/cairo2/internal/protocol"
)

// Server handles HTTP for the registry.
type Server struct {
	ledger    *Ledger
	tsnetSrv  *tsnet.Server
	mux       *http.ServeMux
	httpSrv   *http.Server
	startedAt time.Time
}

// New creates a Server wired to the given ledger. tsnetSrv may be nil when running with --no-tsnet.
func New(ledger *Ledger, tsnetSrv *tsnet.Server, startedAt time.Time) *Server {
	s := &Server{
		ledger:    ledger,
		tsnetSrv:  tsnetSrv,
		mux:       http.NewServeMux(),
		startedAt: startedAt,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("POST /register", s.handleRegister)
	s.mux.HandleFunc("GET /agents", s.handleAgents)
	ws := newWsHandler(s.ledger)
	s.mux.HandleFunc("GET /agents/{id}/stream", ws.handle)
}

// Serve accepts requests on ln until ctx is done, then shuts down gracefully.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.httpSrv = &http.Server{Handler: LogRequests(s.mux)}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutdownCtx)
	}()
	if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// healthzResponse is the JSON shape returned by GET /healthz.
type healthzResponse struct {
	Status        string `json:"status"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Total         int64  `json:"total"`
	Active        int64  `json:"active"`
	Stale         int64  `json:"stale"`
	WsConnected   int64  `json:"ws_connected"`
}

func healthzBody(ctx context.Context, ledger *Ledger, startedAt time.Time) healthzResponse {
	resp := healthzResponse{
		Status:        "ok",
		UptimeSeconds: int64(time.Since(startedAt).Seconds()),
	}
	if c, err := ledger.CountAgents(ctx); err == nil {
		resp.Total = c.Total
		resp.Active = c.Active
		resp.Stale = c.Stale
		resp.WsConnected = c.WsConnected
	} else {
		resp.Status = "degraded"
		log.Printf("healthz: count agents: %v", err)
	}
	return resp
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	resp := healthzBody(r.Context(), s.ledger, s.startedAt)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req protocol.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}

	var owner string
	if s.tsnetSrv != nil {
		lc, err := s.tsnetSrv.LocalClient()
		if err == nil {
			who, _ := lc.WhoIs(r.Context(), r.RemoteAddr)
			owner = ownerFromWhoIs(who)
		} else {
			owner = "unknown"
		}
	} else {
		owner = "local"
	}

	if owner == "unknown" {
		log.Printf("register: WhoIs returned unknown owner for %s", r.RemoteAddr)
	}

	agentID, registeredAt, err := s.ledger.Register(r.Context(), req.AgentID, owner, req.Hostname, req.TailnetNode, req.Version)
	if err != nil {
		if errors.Is(err, ErrRevoked) {
			http.Error(w, `{"error":"agent revoked"}`, http.StatusForbidden)
			return
		}
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("register: agent_id=%s owner=%s hostname=%s version=%s", agentID, owner, req.Hostname, req.Version)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(protocol.RegisterResponse{
		AgentID:      agentID,
		RegisteredAt: registeredAt,
	})
}

// statusRecorder captures the response code for request logging.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController (and coder/websocket) access the underlying ResponseWriter
// for interface checks like http.Hijacker.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// LogRequests wraps h with one-line-per-request logging:
//
//	request: METHOD path code latency=Xms
func LogRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		h.ServeHTTP(rec, r)
		log.Printf("request: %s %s %d latency=%s", r.Method, r.URL.Path, rec.code, time.Since(start).Round(time.Microsecond))
	})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.ledger.List(r.Context())
	if err != nil {
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	if agents == nil {
		agents = []Agent{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agents)
}

func ownerFromWhoIs(who *apitype.WhoIsResponse) string {
	if who == nil {
		return "unknown"
	}
	if who.UserProfile != nil && who.UserProfile.LoginName != "" {
		return who.UserProfile.LoginName
	}
	if who.Node != nil && len(who.Node.Tags) > 0 {
		return who.Node.Tags[0]
	}
	return "unknown"
}
