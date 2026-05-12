package registryserver

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
//
// This package-level gate uses the no-op access.CanAddress (for handlers wired
// before the Decider was available — kept for backward compat). New handlers
// should use gateWith instead.
func gate(w http.ResponseWriter, r *http.Request, action, target string) (authn.Identity, bool) {
	return gateWith(nil, nil, w, r, action, target)
}

// gateWith is the real gate: uses decider when non-nil, falls back to no-op stub.
func gateWith(d *access.Decider, resolver authn.Resolver, w http.ResponseWriter, r *http.Request, action, target string) (authn.Identity, bool) {
	id, _ := authn.VerifyWith(r, resolver)
	var allowed bool
	var reason string
	if d != nil {
		allowed, reason = d.CanAddress(r.Context(), id.User, target)
	} else {
		allowed, reason = access.CanAddress(r.Context(), id.User, target)
	}
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
