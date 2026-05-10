package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/scotmcc/cairo2/internal/version"
)

func (s *Server) handleAPIHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":             true,
		"version":        version.Version,
		"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		"db_path":        s.opts.DBPath,
	})
}

func (s *Server) handleConfigSnapshot(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.db.Config.All()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	roles, err := s.db.Roles.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	aspects, err := s.db.ConsiderAspects.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"config":          cfg,
		"roles":           roles,
		"considerAspects": aspects,
	})
}

type sessionListItem struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CWD        string `json:"cwd"`
	Role       string `json:"role"`
	CreatedAt  int64  `json:"created_at"`
	LastActive int64  `json:"last_active"`
	Insight    string `json:"insight"`
}

func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.db.Sessions.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]sessionListItem, 0, len(sessions))
	for _, sess := range sessions {
		item := sessionListItem{
			ID:         sess.ID,
			Name:       sess.Name,
			CWD:        sess.CWD,
			Role:       sess.Role,
			CreatedAt:  sess.CreatedAt.Unix(),
			LastActive: sess.LastActive.Unix(),
		}
		if msg, err := s.db.Messages.LatestUserForSession(sess.ID); err == nil && msg != nil {
			c := msg.Content
			if len(c) > 80 {
				c = c[:80]
			}
			item.Insight = c
		}
		out = append(out, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleSessionsGet(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	sess, err := s.db.Sessions.Get(id)
	if err == sql.ErrNoRows || (err == nil && sess == nil) {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sess)
}

func (s *Server) handleSessionsMessages(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	limit := 50
	before := int64(0)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	if v := r.URL.Query().Get("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			before = n
		}
	}
	msgs, err := s.db.Messages.PageForSession(id, limit, before)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(msgs)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	sc, err := s.db.Sessions.Count()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mc, err := s.db.Memories.Count()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jc, err := s.db.Jobs.Count()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{
		"sessions": sc,
		"memories": mc,
		"jobs":     jc,
	})
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}
