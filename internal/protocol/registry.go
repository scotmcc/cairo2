package protocol

import "encoding/json"

// RegisterRequest is sent by a cairo agent to /register.
// Copy-pasted in cairo and cairo-registry — see README sync ritual.
// AgentID is optional; when provided and (agent_id, owner) matches an existing row,
// that row is updated in place and the same agent_id is returned (stable identity
// across hostname changes). When absent or unmatched, falls through to legacy
// (owner, hostname, tailnet_node) lookup-or-insert.
type RegisterRequest struct {
	AgentID     string `json:"agent_id,omitempty"`
	Hostname    string `json:"hostname"`
	Version     string `json:"version"`
	TailnetNode string `json:"tailnet_node"`
}

// RegisterResponse is returned by /register.
type RegisterResponse struct {
	AgentID      string `json:"agent_id"`
	RegisteredAt int64  `json:"registered_at"`
}

// Frame is a WS frame on the agent stream. Phase 1B uses ping/pong only.
type Frame struct {
	Type string          `json:"type"`           // "ping" | "pong"
	Body json.RawMessage `json:"body,omitempty"` // empty in Phase 1B
}
