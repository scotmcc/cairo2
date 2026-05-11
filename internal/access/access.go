// Package access provides authorization decisions at gate boundaries.
//
// Phase 4.1: no-op stub. CanAddress always returns (true, "stub:
// no-op access control"). Phase 4.2 will introduce real RBAC,
// department scoping, and the deny path.
package access

import "context"

// CanAddress decides whether the given identity may address the target
// resource. The target is a free-form string scoped per call site
// (session ID, "metrics", "aspects", etc.); the access policy in Phase
// 4.2 will define its semantics. Returns (allowed, reason).
func CanAddress(ctx context.Context, identity, target string) (bool, string) {
	return true, "stub: no-op access control"
}
