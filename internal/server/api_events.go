package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/scotmcc/cairo2/internal/agent"
)

var sseAllowedTypes = map[agent.EventType]bool{
	agent.EventAgentStart:    true,
	agent.EventAgentEnd:      true,
	agent.EventTurnStart:     true,
	agent.EventTurnEnd:       true,
	agent.EventToolStart:     true,
	agent.EventToolEnd:       true,
	agent.EventError:         true,
	agent.EventStallDetected: true,
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := s.agent.Bus().Subscribe()
	defer unsub()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !sseAllowedTypes[ev.Type] {
				continue
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
