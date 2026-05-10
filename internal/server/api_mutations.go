package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var value string
	if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON value")
		return
	}
	if err := s.db.Config.Set(key, value); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"key": key, "value": value})
}

func (s *Server) handleSessionsPatch(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.db.Sessions.Rename(id, body.Name); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sess, err := s.db.Sessions.Get(id)
	if err == sql.ErrNoRows {
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

func (s *Server) handleSessionsDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	if _, err := s.db.Sessions.Get(id); err == sql.ErrNoRows {
		writeJSONError(w, http.StatusNotFound, "session not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.db.Sessions.Delete(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRolesPatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Field string `json:"field"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if _, err := s.db.Roles.Get(name); err == sql.ErrNoRows {
		writeJSONError(w, http.StatusNotFound, "role not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var err error
	switch body.Field {
	case "model":
		err = s.db.Roles.SetModel(name, body.Value)
	case "think":
		err = s.db.Roles.SetThink(name, body.Value)
	default:
		writeJSONError(w, http.StatusBadRequest, "field must be 'model' or 'think'")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleAspectPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Traits   string `json:"traits"`
		Enabled  bool   `json:"enabled"`
		Position int    `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.db.ConsiderAspects.Upsert(name, body.Traits, body.Enabled, body.Position); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	aspect, err := s.db.ConsiderAspects.Get(name)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(aspect)
}

func (s *Server) handleAspectPatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.db.ConsiderAspects.SetEnabled(name, body.Enabled); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleAspectDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.db.ConsiderAspects.Get(name); err == sql.ErrNoRows {
		writeJSONError(w, http.StatusNotFound, "aspect not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.db.ConsiderAspects.Delete(name); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
