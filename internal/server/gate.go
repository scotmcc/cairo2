package server

import (
	"net/http"
	"time"

	"github.com/scotmcc/cairo2/internal/access"
	"github.com/scotmcc/cairo2/internal/audit"
	"github.com/scotmcc/cairo2/internal/authn"
)

// gate runs the ZT gate for a single handler call: Verify → CanAddress → Log.
// Returns the caller Identity and true when access is granted, false when denied.
// On denial, gate writes 403 and the caller must return immediately.
func (s *Server) gate(w http.ResponseWriter, r *http.Request, action, target string) (authn.Identity, bool) {
	id, _ := authn.VerifyWith(r, s.opts.Resolver)
	allowed, reason := access.CanAddress(r.Context(), id.User, target)
	decision := "granted"
	if !allowed {
		decision = "denied"
	}
	audit.Log(r.Context(), audit.Event{
		Timestamp: time.Now(),
		Actor:     id.User,
		Gate:      "access",
		Action:    action,
		Target:    target,
		Decision:  decision,
		Reason:    reason,
	})
	if !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
	}
	return id, allowed
}
