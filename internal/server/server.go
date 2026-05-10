package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// Options configures the HTTP server at startup.
type Options struct {
	Port   int
	Auth   bool
	Token  string
	DBPath string
}

// Server owns the HTTP listener, auth middleware, and route registration.
type Server struct {
	agent     Prompter
	db        *sqliteopen.DB
	bridge    *SessionBridge
	opts      Options
	mux       *http.ServeMux
	httpSrv   *http.Server
	streams   *streamRegistry
	startedAt time.Time
}

// New creates a Server. Call Serve to start accepting on a listener.
func New(a Prompter, database *sqliteopen.DB, bridge *SessionBridge, opts Options) *Server {
	s := &Server{
		agent:     a,
		db:        database,
		bridge:    bridge,
		opts:      opts,
		mux:       http.NewServeMux(),
		streams:   newStreamRegistry(),
		startedAt: time.Now(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// Health check — no auth.
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	// API routes — auth applied in handler wrappers.
	s.mux.HandleFunc("POST /api/chat", s.auth(s.handleChat))

	// OpenAI-compatible routes (Phase 4).
	s.mux.HandleFunc("GET /v1/models", s.auth(s.handleModels))
	s.mux.HandleFunc("POST /v1/chat/completions", s.auth(s.handleCompletions))

	// JSONRPC surface (Phase 5).
	s.mux.HandleFunc("POST /rpc", s.auth(s.handleRPC))
	s.mux.HandleFunc("GET /rpc/stream/", s.auth(s.handleRPCStream))

	// Phase 3.1 — read endpoints.
	s.mux.HandleFunc("GET /api/health", s.handleAPIHealth)
	s.mux.HandleFunc("GET /api/config/snapshot", s.auth(s.handleConfigSnapshot))
	s.mux.HandleFunc("GET /api/sessions", s.auth(s.handleSessionsList))
	s.mux.HandleFunc("GET /api/sessions/{id}", s.auth(s.handleSessionsGet))
	s.mux.HandleFunc("GET /api/sessions/{id}/messages", s.auth(s.handleSessionsMessages))
	s.mux.HandleFunc("GET /api/metrics", s.auth(s.handleMetrics))
}

// Serve accepts requests on ln until ctx is done.
// On cancellation, performs a graceful 5-second shutdown.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.httpSrv = &http.Server{Handler: s.mux}
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

// auth wraps a handler with bearer-token authentication when opts.Auth is true.
// Requests missing or presenting an invalid token receive 401.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Auth {
			tok := bearerToken(r)
			if subtle.ConstantTimeCompare([]byte(tok), []byte(s.opts.Token)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
		}
		next(w, r)
	}
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
// Returns "" if the header is absent or malformed.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) {
		return ""
	}
	return h[len(prefix):]
}

// handleHealthz returns 200 OK with a minimal JSON body.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
