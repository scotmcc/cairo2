// Package access provides authorization decisions at gate boundaries.
//
// Phase 4.2: real RBAC implementation. The Decider struct provides CanAddress
// and ListVisible backed by a ledger. The package-level CanAddress is kept as
// a documented no-op for callers that have not yet been migrated to Decider
// (specifically internal/server/gate.go, the cairo agent side). The agent-side
// gates remain no-ops until a future phase when agent and registry collaborate
// on session-scoped access decisions.
package access

import "context"

// Ledger is the minimal interface the Decider needs from the registry DB.
// Defined here so the access package does not import registryserver (which
// would create an import cycle). The registryserver package provides an
// adapter that satisfies this interface.
type Ledger interface {
	IsSuperAdmin(ctx context.Context, user string) (bool, error)
	DeptsForUser(ctx context.Context, user string) ([]string, error)
	GetAssignment(ctx context.Context, agentID string) (*Assignment, error)
	GetAgentOwner(ctx context.Context, agentID string) (string, bool, error)
	ListAll(ctx context.Context) ([]Agent, error)
}

// Assignment describes how an agent is classified.
type Assignment struct {
	AgentID   string
	AgentType string // "personal" | "departmental" | "enterprise"
	DeptID    string
}

// Agent is the minimal agent info needed for visibility checks.
type Agent struct {
	AgentID string
	Owner   string
}

// Decider makes access control decisions backed by a Ledger.
type Decider struct {
	l Ledger
}

// New creates a Decider using the provided Ledger.
func New(l Ledger) *Decider {
	return &Decider{l: l}
}

// IsSuperAdmin returns true if identity is a super-admin (errors treated as false).
func (d *Decider) IsSuperAdmin(ctx context.Context, identity string) bool {
	ok, _ := d.l.IsSuperAdmin(ctx, identity)
	return ok
}

// CanAddress decides whether identity may address target.
//
// Aggregate targets ("agents", "broadcast", "departments", "super-admins",
// "config", "metrics"): some are super-admin-only, "agents" is open (filtering
// happens downstream in ListVisible).
//
// All other targets are treated as agent_id lookups with type-based rules:
//   - enterprise: any authenticated identity
//   - departmental: identity must be a member of the agent's department
//   - personal (or no assignment): identity must be the agent's owner
//
// Super-admin always wins.
func (d *Decider) CanAddress(ctx context.Context, identity, target string) (bool, string) {
	superAdmin, _ := d.l.IsSuperAdmin(ctx, identity)
	if superAdmin {
		return true, "super-admin"
	}

	switch target {
	case "agents":
		return true, "open-catalog"
	case "broadcast", "departments", "super-admins", "config", "metrics":
		return false, "super-admin required for " + target
	}

	// Treat as agent_id.
	asgn, err := d.l.GetAssignment(ctx, target)
	if err != nil {
		return false, "ledger error"
	}

	agentType := "personal"
	var deptID string
	if asgn != nil {
		agentType = asgn.AgentType
		deptID = asgn.DeptID
	}

	switch agentType {
	case "enterprise":
		return true, "enterprise-agent"
	case "departmental":
		depts, err := d.l.DeptsForUser(ctx, identity)
		if err != nil {
			return false, "ledger error"
		}
		for _, d := range depts {
			if d == deptID {
				return true, "dept-member"
			}
		}
		return false, "not a member of dept " + deptID
	default: // personal
		owner, found, err := d.l.GetAgentOwner(ctx, target)
		if err != nil || !found {
			return false, "agent not found"
		}
		if owner == identity {
			return true, "owner"
		}
		return false, "not owner"
	}
}

// ListVisible returns the agent IDs that identity is allowed to address.
// Super-admin sees all; others see enterprise agents + departmental agents
// in their departments + personal agents they own.
func (d *Decider) ListVisible(ctx context.Context, identity string) ([]string, error) {
	superAdmin, _ := d.l.IsSuperAdmin(ctx, identity)

	agents, err := d.l.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	var depts []string
	if !superAdmin {
		depts, err = d.l.DeptsForUser(ctx, identity)
		if err != nil {
			return nil, err
		}
	}

	deptSet := make(map[string]bool, len(depts))
	for _, d := range depts {
		deptSet[d] = true
	}

	var ids []string
	for _, ag := range agents {
		if superAdmin {
			ids = append(ids, ag.AgentID)
			continue
		}
		asgn, err := d.l.GetAssignment(ctx, ag.AgentID)
		if err != nil {
			continue
		}
		agentType := "personal"
		deptID := ""
		if asgn != nil {
			agentType = asgn.AgentType
			deptID = asgn.DeptID
		}
		switch agentType {
		case "enterprise":
			ids = append(ids, ag.AgentID)
		case "departmental":
			if deptSet[deptID] {
				ids = append(ids, ag.AgentID)
			}
		default: // personal
			if ag.Owner == identity {
				ids = append(ids, ag.AgentID)
			}
		}
	}
	return ids, nil
}

// CanAddress is the package-level no-op stub kept for callers (internal/server/gate.go)
// that have not been migrated to Decider. The cairo agent side has no ledger access
// in Phase 4.2; its gate remains open until a later phase.
func CanAddress(ctx context.Context, identity, target string) (bool, string) {
	return true, "stub: no-op access control"
}
