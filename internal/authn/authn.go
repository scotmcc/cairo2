// Package authn provides identity verification at HTTP gate boundaries.
//
// Phase 4.1: no-op stub. Verify extracts X-Operator-Identity header
// (matching cmd/cairo-ctl convention) and returns it as Identity.User.
// Phase 4.4 will replace this with real tsnet peer-cert identity extraction.
package authn

import "net/http"

// Identity is the verified caller of an HTTP request.
type Identity struct {
	User   string // identity string (email, username, "local")
	Source string // where the identity came from: "header", "tsnet", "local"
}

// Verify extracts identity from the request. In the Phase 4.1 stub,
// this reads X-Operator-Identity and falls back to "local" when absent.
// Never returns an error in the stub. Phase 4.4 will introduce real
// verification and may return errors.
func Verify(r *http.Request) (Identity, error) {
	if v := r.Header.Get("X-Operator-Identity"); v != "" {
		return Identity{User: v, Source: "header"}, nil
	}
	return Identity{User: "local", Source: "local"}, nil
}
