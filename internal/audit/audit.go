// Package audit provides the audit-log gate.
//
// Phase 4.1: no-op stub. Log accepts events and discards them.
// Phase 4.3 will replace this with an append-only SQLite-backed log.
package audit

import (
	"context"
	"time"
)

// Event is a single audit record. Fields are set at the call site;
// the audit package does not enrich them in the stub. Phase 4.3 may
// add server-side enrichment (request ID, source IP, etc.).
type Event struct {
	Timestamp time.Time
	Actor     string            // identity making the request
	Gate      string            // "user" | "access" | "address" | "data"
	Action    string            // verb: "list", "send", "register", etc.
	Target    string            // resource: agent ID, session ID, etc.
	Decision  string            // "granted" | "denied"
	Reason    string            // free-form, optional
	Metadata  map[string]string // extensible
}

// Log records an audit event. The Phase 4.1 stub discards events.
// Never returns an error.
func Log(ctx context.Context, e Event) {
	_ = ctx
	_ = e
}
