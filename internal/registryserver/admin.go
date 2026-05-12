package registryserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/access"
	"github.com/scotmcc/cairo2/internal/audit"
)

// NewAdmin returns the admin mux pre-wired with operator-scoped endpoints.
// auditReader may be nil (falls back to no audit query capability).
func NewAdmin(ledger *Ledger, startedAt time.Time, auditReader audit.Reader) http.Handler {
	decider := access.New(ledger.AsAccessAdapter())
	mux := http.NewServeMux()

	// Existing routes — updated to use decider.
	mux.HandleFunc("GET /agents", handleAdminAgents(ledger, decider))
	mux.HandleFunc("GET /agents/{id}", handleAdminAgent(ledger, decider))
	mux.HandleFunc("GET /healthz", handleAdminHealthz(ledger, startedAt))
	mux.HandleFunc("POST /agents/{id}/revoke", handleAdminRevoke(ledger, decider))
	mux.HandleFunc("POST /broadcast", handleAdminBroadcast(ledger, decider))

	// Audit log — super-admin only.
	mux.HandleFunc("GET /audit", handleAdminAudit(decider, auditReader))

	// Department management.
	mux.HandleFunc("GET /departments", handleDeptList(ledger, decider))
	mux.HandleFunc("POST /departments", handleDeptCreate(ledger, decider))
	mux.HandleFunc("GET /departments/{dept_id}/members", handleDeptMemberList(ledger, decider))
	mux.HandleFunc("POST /departments/{dept_id}/members", handleDeptMemberAdd(ledger, decider))
	mux.HandleFunc("DELETE /departments/{dept_id}/members/{user}", handleDeptMemberRemove(ledger, decider))

	// Agent assignment.
	mux.HandleFunc("POST /agents/{id}/assign", handleAgentAssign(ledger, decider))

	// Super-admin management.
	mux.HandleFunc("GET /super-admins", handleSuperAdminList(ledger, decider))
	mux.HandleFunc("POST /super-admins", handleSuperAdminAdd(ledger, decider))
	mux.HandleFunc("DELETE /super-admins/{user}", handleSuperAdminRemove(ledger, decider))

	return mux
}

func operatorFromHeader(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Operator-Identity"))
}

func handleAdminAgents(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := gateWith(decider, nil, w, r, "agent.list", "agents")
		if !ok {
			return
		}
		visibleIDs, err := decider.ListVisible(r.Context(), id.User)
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		idSet := make(map[string]bool, len(visibleIDs))
		for _, v := range visibleIDs {
			idSet[v] = true
		}
		all, err := ledger.List(r.Context())
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		agents := []Agent{}
		for _, a := range all {
			if idSet[a.AgentID] {
				agents = append(agents, a)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agents)
	}
}

func handleAdminAgent(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.PathValue("id")
		if _, ok := gateWith(decider, nil, w, r, "agent.get", agentID); !ok {
			return
		}
		operator := operatorFromHeader(r)
		if operator == "" {
			http.NotFound(w, r)
			return
		}
		agent, err := ledger.GetByOwner(r.Context(), agentID, operator)
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		if agent == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agent)
	}
}

func handleAdminRevoke(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.PathValue("id")
		if _, ok := gateWith(decider, nil, w, r, "agent.revoke", agentID); !ok {
			return
		}
		operator := operatorFromHeader(r)
		if operator == "" {
			http.Error(w, `{"error":"operator required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.Revoke(r.Context(), agentID, operator); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"revoked"}`))
	}
}

func handleAdminBroadcast(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gateWith(decider, nil, w, r, "broadcast", "broadcast"); !ok {
			return
		}
		operator := operatorFromHeader(r)
		if operator == "" {
			http.Error(w, `{"error":"operator required"}`, http.StatusBadRequest)
			return
		}
		var body struct {
			Command string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Command == "" {
			http.Error(w, `{"error":"command required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.InsertCommand(r.Context(), operator, body.Command); err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}
}

func handleAdminHealthz(ledger *Ledger, startedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := healthzBody(r.Context(), ledger, startedAt)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// --- Department handlers ---

func handleDeptList(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gateWith(decider, nil, w, r, "department.list", "departments"); !ok {
			return
		}
		depts, err := ledger.ListDepartments(r.Context())
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		if depts == nil {
			depts = []Department{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(depts)
	}
}

func handleDeptCreate(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gateWith(decider, nil, w, r, "department.create", "departments"); !ok {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.CreateDepartment(r.Context(), "", body.Name); err != nil {
			if errors.Is(err, ErrDeptConflict) {
				http.Error(w, `{"error":"department already exists"}`, http.StatusConflict)
				return
			}
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		dept, err := ledger.GetDepartment(r.Context(), body.Name)
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(dept)
	}
}

func handleDeptMemberList(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deptID := r.PathValue("dept_id")
		if _, ok := gateWith(decider, nil, w, r, "department.member.list", "departments"); !ok {
			return
		}
		members, err := ledger.ListMembers(r.Context(), deptID)
		if err != nil {
			if errors.Is(err, ErrDeptNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		if members == nil {
			members = []Member{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(members)
	}
}

func handleDeptMemberAdd(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deptID := r.PathValue("dept_id")
		if _, ok := gateWith(decider, nil, w, r, "department.member.add", "departments"); !ok {
			return
		}
		var body struct {
			User string `json:"user"`
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.User == "" || body.Role == "" {
			http.Error(w, `{"error":"user and role required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.AddMember(r.Context(), deptID, body.User, body.Role); err != nil {
			if errors.Is(err, ErrDeptNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"added"}`))
	}
}

func handleDeptMemberRemove(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deptID := r.PathValue("dept_id")
		user := r.PathValue("user")
		if _, ok := gateWith(decider, nil, w, r, "department.member.remove", "departments"); !ok {
			return
		}
		if err := ledger.RemoveMember(r.Context(), deptID, user); err != nil {
			if errors.Is(err, ErrDeptNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"removed"}`))
	}
}

// --- Agent assignment handler ---

func handleAgentAssign(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.PathValue("id")
		if _, ok := gateWith(decider, nil, w, r, "agent.assign", "departments"); !ok {
			return
		}
		var body struct {
			AgentType string `json:"agent_type"`
			DeptID    string `json:"dept_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AgentType == "" {
			http.Error(w, `{"error":"agent_type required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.AssignAgent(r.Context(), agentID, body.AgentType, body.DeptID); err != nil {
			if errors.Is(err, ErrDeptNotFound) {
				http.Error(w, `{"error":"department not found"}`, http.StatusNotFound)
				return
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"assigned"}`))
	}
}

// --- Super-admin handlers ---

func handleSuperAdminList(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gateWith(decider, nil, w, r, "super-admin.list", "super-admins"); !ok {
			return
		}
		users, err := ledger.ListSuperAdmins(r.Context())
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		if users == nil {
			users = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(users)
	}
}

func handleSuperAdminAdd(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gateWith(decider, nil, w, r, "super-admin.add", "super-admins"); !ok {
			return
		}
		var body struct {
			User string `json:"user"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.User == "" {
			http.Error(w, `{"error":"user required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.AddSuperAdmin(r.Context(), body.User); err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"added"}`))
	}
}

func handleSuperAdminRemove(ledger *Ledger, decider *access.Decider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := r.PathValue("user")
		if _, ok := gateWith(decider, nil, w, r, "super-admin.remove", "super-admins"); !ok {
			return
		}
		if err := ledger.RemoveSuperAdmin(r.Context(), user); err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"removed"}`))
	}
}

// --- Audit log handler ---

func handleAdminAudit(decider *access.Decider, reader audit.Reader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gateWith(decider, nil, w, r, "audit.list", "audit"); !ok {
			return
		}
		if reader == nil {
			http.Error(w, `{"error":"audit reader not configured"}`, http.StatusInternalServerError)
			return
		}

		f := audit.QueryFilter{}
		q := r.URL.Query()
		f.Actor = q.Get("actor")
		f.Gate = q.Get("gate")
		f.Action = q.Get("action")

		if s := q.Get("since"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				f.Since = t
			} else if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				f.Since = time.Unix(n, 0)
			}
		}
		if s := q.Get("until"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				f.Until = t
			} else if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				f.Until = time.Unix(n, 0)
			}
		}
		if s := q.Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				f.Limit = n
			}
		}

		events, err := reader.Query(r.Context(), f)
		if err != nil {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		if events == nil {
			events = []audit.Event{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(events)
	}
}
