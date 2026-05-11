package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// JSON-RPC 2.0 error codes.
const (
	rpcErrParse    = -32700
	rpcErrInvalid  = -32600
	rpcErrNotFound = -32601
	rpcErrParams   = -32602
	rpcErrInternal = -32603
)

// rpcRequest is an incoming JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      any             `json:"id"`
}

// rpcError is the JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcResponse is the JSON-RPC 2.0 response envelope.
type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

// streamEntry holds a pending stream's token channel.
type streamEntry struct {
	tokens <-chan string
	errCh  <-chan error
}

// streamRegistry maps stream IDs to their pending SSE channels.
type streamRegistry struct {
	mu      sync.Mutex
	entries map[string]*streamEntry
}

func newStreamRegistry() *streamRegistry {
	return &streamRegistry{entries: make(map[string]*streamEntry)}
}

func (r *streamRegistry) add(id string, entry *streamEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[id] = entry
}

func (r *streamRegistry) pop(id string) (*streamEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if ok {
		delete(r.entries, id)
	}
	return e, ok
}

// safeSlashCommands is the allowlist of slash commands that are safe to invoke
// over the API. Exit/quit and TUI-only commands are excluded.
var safeSlashCommands = map[string]bool{
	"/init":     true,
	"/help":     true,
	"/session":  true,
	"/sessions": true,
	"/jobs":     true,
	"/memories": true,
	"/tools":    true,
	"/skills":   true,
}

// handleRPC handles POST /rpc — the JSON-RPC 2.0 dispatcher.
func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r, "rpc.call", "rpc"); !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, rpcErrParse, "parse error")
		return
	}

	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, rpcErrInvalid, "jsonrpc must be \"2.0\"")
		return
	}

	switch req.Method {
	case "cairo.send":
		s.rpcSend(w, r, &req)
	case "cairo.send.stream":
		s.rpcSendStream(w, r, &req)
	case "cairo.status":
		s.rpcStatus(w, r, &req)
	case "cairo.slash":
		s.rpcSlash(w, r, &req)
	default:
		writeRPCError(w, req.ID, rpcErrNotFound, fmt.Sprintf("unknown method: %s", req.Method))
	}
}

// sendParams are the params for cairo.send and cairo.send.stream.
type sendParams struct {
	Message string      `json:"message"`
	Context chatContext `json:"context"`
}

// rpcSend implements cairo.send — blocking send, returns full response.
func (s *Server) rpcSend(w http.ResponseWriter, r *http.Request, req *rpcRequest) {
	var p sendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, rpcErrParams, "invalid params")
		return
	}
	if p.Message == "" {
		writeRPCError(w, req.ID, rpcErrParams, "message is required")
		return
	}

	text := p.Message + formatContext([]chatContext{p.Context})

	resp, turnID, err := s.bridge.Send(r.Context(), text)
	if err != nil {
		writeRPCError(w, req.ID, rpcErrInternal, err.Error())
		return
	}

	var sessionID int64
	if sess := s.agent.Session(); sess != nil {
		sessionID = sess.ID
	}

	writeRPCResult(w, req.ID, map[string]any{
		"response":   resp,
		"session_id": sessionID,
		"turn_id":    turnID,
	})
}

// rpcSendStream implements cairo.send.stream — returns a stream_id immediately;
// the caller polls GET /rpc/stream/{id} for SSE token chunks.
func (s *Server) rpcSendStream(w http.ResponseWriter, r *http.Request, req *rpcRequest) {
	var p sendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, rpcErrParams, "invalid params")
		return
	}
	if p.Message == "" {
		writeRPCError(w, req.ID, rpcErrParams, "message is required")
		return
	}

	text := p.Message + formatContext([]chatContext{p.Context})

	streamID := newStreamID()
	tokensCh := make(chan string, 64)
	errCh := make(chan error, 1)

	entry := &streamEntry{tokens: tokensCh, errCh: errCh}
	s.streams.add(streamID, entry)

	// Fire the bridge send in the background; the stream consumer drains tokens.
	go func() {
		_, _, err := s.bridge.SendStream(r.Context(), text, tokensCh)
		errCh <- err
	}()

	writeRPCResult(w, req.ID, map[string]string{"stream_id": streamID})
}

// handleRPCStream handles GET /rpc/stream/{id} — SSE consumer for a pending stream.
func (s *Server) handleRPCStream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r, "rpc.stream", "rpc"); !ok {
		return
	}
	// Extract {id} from the URL path manually (Go 1.22 pattern matching).
	id := strings.TrimPrefix(r.URL.Path, "/rpc/stream/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	entry, ok := s.streams.pop(id)
	if !ok {
		http.Error(w, "stream not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for tok := range entry.tokens {
		data, _ := json.Marshal(map[string]string{"token": tok})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	<-entry.errCh
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// rpcStatus implements cairo.status — returns server and session state.
func (s *Server) rpcStatus(w http.ResponseWriter, r *http.Request, req *rpcRequest) {
	var sessionID int64
	var sessionName string
	if sess := s.agent.Session(); sess != nil {
		sessionID = sess.ID
		sessionName = sess.Name
	}

	writeRPCResult(w, req.ID, map[string]any{
		"session_id":   sessionID,
		"session_name": sessionName,
		"model":        s.agent.Model(),
		"auth":         s.opts.Auth,
		"busy":         s.agent.IsStreaming(),
	})
}

// slashParams are the params for cairo.slash.
type slashParams struct {
	Command string `json:"command"`
}

// rpcSlash implements cairo.slash — invokes a safe slash command.
func (s *Server) rpcSlash(w http.ResponseWriter, r *http.Request, req *rpcRequest) {
	var p slashParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, rpcErrParams, "invalid params")
		return
	}

	cmd := strings.Fields(p.Command)
	if len(cmd) == 0 {
		writeRPCError(w, req.ID, rpcErrParams, "command is required")
		return
	}

	name := cmd[0]
	if !safeSlashCommands[name] {
		writeRPCError(w, req.ID, rpcErrNotFound, "unknown or unsafe slash command")
		return
	}

	result, err := s.execSlash(name)
	if err != nil {
		writeRPCError(w, req.ID, rpcErrInternal, err.Error())
		return
	}

	writeRPCResult(w, req.ID, map[string]string{"output": result})
}

// execSlash executes a safe slash command against the DB and returns its output.
func (s *Server) execSlash(cmd string) (string, error) {
	switch cmd {
	case "/help":
		return "Available commands: /init /help /session /sessions /jobs /memories /tools /skills", nil

	case "/session":
		sess := s.agent.Session()
		if sess == nil {
			return "no active session", nil
		}
		name := sess.Name
		if name == "" {
			name = "(unnamed)"
		}
		return fmt.Sprintf("session %d: %s\nrole: %s\ncwd:  %s\n", sess.ID, name, sess.Role, sess.CWD), nil

	case "/sessions":
		sessions, err := s.db.Sessions.List()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		activeSess := s.agent.Session()
		var activeID int64
		if activeSess != nil {
			activeID = activeSess.ID
		}
		for _, sess := range sessions {
			marker := "  "
			if sess.ID == activeID {
				marker = "* "
			}
			name := sess.Name
			if name == "" {
				name = "(unnamed)"
			}
			fmt.Fprintf(&b, "%s[%d] %s — %s — %s\n", marker, sess.ID, name, sess.Role, sess.LastActive.Format("2006-01-02 15:04"))
		}
		if b.Len() == 0 {
			b.WriteString("no sessions\n")
		}
		return b.String(), nil

	case "/jobs":
		jobs, err := s.db.Jobs.List()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, j := range jobs {
			fmt.Fprintf(&b, "[%d] [%s] %s\n", j.ID, j.Status, j.Title)
		}
		if b.Len() == 0 {
			b.WriteString("no jobs\n")
		}
		return b.String(), nil

	case "/memories":
		memories, err := s.db.Memories.AllContent()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, m := range memories {
			if m.PinnedAt != nil {
				fmt.Fprintf(&b, "[P] [%d] %s\n", m.ID, m.Content)
			} else {
				fmt.Fprintf(&b, "[%d] %s\n", m.ID, m.Content)
			}
		}
		if b.Len() == 0 {
			b.WriteString("no memories\n")
		}
		return b.String(), nil

	case "/tools":
		customTools, err := s.db.Tools.List()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, t := range customTools {
			status := "on"
			if !t.IsEnabled {
				status = "off"
			}
			fmt.Fprintf(&b, "[%s] %s — %s\n", status, t.Name, t.Description)
		}
		if b.Len() == 0 {
			b.WriteString("no custom tools\n")
		}
		return b.String(), nil

	case "/skills":
		skills, err := s.db.Skills.List()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, sk := range skills {
			fmt.Fprintf(&b, "%s — %s\n", sk.Name, sk.Description)
		}
		if b.Len() == 0 {
			b.WriteString("no skills\n")
		}
		return b.String(), nil

	case "/init":
		// /init triggers a Prompt; handled via agent, not DB query.
		// For the RPC surface, return a message directing the caller to use cairo.send.
		return "use cairo.send with your init prompt to run /init over the API", nil

	default:
		return "", fmt.Errorf("unknown command: %s", cmd)
	}
}

// writeRPCResult writes a successful JSON-RPC 2.0 response.
func writeRPCResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

// writeRPCError writes an error JSON-RPC 2.0 response.
func writeRPCError(w http.ResponseWriter, id any, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	})
}

// newStreamID generates a random stream ID with an "s_" prefix.
func newStreamID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "s_" + hex.EncodeToString(b)
}
