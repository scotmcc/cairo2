package registryserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/access"
)

// NewAdmin returns the admin mux pre-wired with operator-scoped endpoints.
func NewAdmin(ledger *Ledger, startedAt time.Time) http.Handler {
	decider := access.New(ledger.AsAccessAdapter())
	mux := http.NewServeMux()

	// Existing routes — updated to use decider.
	mux.HandleFunc("GET /agents", handleAdminAgents(ledger, decider))
	mux.HandleFunc("GET /agents/{id}", handleAdminAgent(ledger))
	mux.HandleFunc("GET /healthz", handleAdminHealthz(ledger, startedAt))
	mux.HandleFunc("POST /agents/{id}/revoke", handleAdminRevoke(ledger))
	mux.HandleFunc("POST /broadcast", handleAdminBroadcast(ledger))

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
		id, ok := gateWith(decider, w, r, "agent.list", "agents")
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

func handleAdminAgent(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := gate(w, r, "agent.get", id); !ok {
			return
		}
		operator := operatorFromHeader(r)
		if operator == "" {
			http.NotFound(w, r)
			return
		}
		agent, err := ledger.GetByOwner(r.Context(), id, operator)
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

func handleAdminRevoke(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := gate(w, r, "agent.revoke", id); !ok {
			return
		}
		operator := operatorFromHeader(r)
		if operator == "" {
			http.Error(w, `{"error":"operator required"}`, http.StatusBadRequest)
			return
		}
		if err := ledger.Revoke(r.Context(), id, operator); err != nil {
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

func handleAdminBroadcast(ledger *Ledger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gate(w, r, "agent.broadcast", "broadcast"); !ok {
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
		if _, ok := gateWith(decider, w, r, "department.list", "departments"); !ok {
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
		if _, ok := gateWith(decider, w, r, "department.create", "departments"); !ok {
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
		if _, ok := gateWith(decider, w, r, "department.member.list", "departments"); !ok {
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
		if _, ok := gateWith(decider, w, r, "department.member.add", "departments"); !ok {
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
		if _, ok := gateWith(decider, w, r, "department.member.remove", "departments"); !ok {
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
		if _, ok := gateWith(decider, w, r, "agent.assign", "departments"); !ok {
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
		if _, ok := gateWith(decider, w, r, "super-admin.list", "super-admins"); !ok {
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
		if _, ok := gateWith(decider, w, r, "super-admin.add", "super-admins"); !ok {
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
		if _, ok := gateWith(decider, w, r, "super-admin.remove", "super-admins"); !ok {
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
