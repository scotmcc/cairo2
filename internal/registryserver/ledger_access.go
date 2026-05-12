package registryserver

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/scotmcc/cairo2/internal/access"
)

// validRoles is the set of allowed department member roles.
var validRoles = map[string]bool{
	"admin": true, "developer": true, "dept-lead": true, "analyst": true,
}

// validAgentTypes is the set of allowed agent assignment types.
var validAgentTypes = map[string]bool{
	"personal": true, "departmental": true, "enterprise": true,
}

// Department is a named group of users that may access departmental agents.
type Department struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// Member is a user with a role in a department.
type Member struct {
	DeptID string `json:"dept_id"`
	User   string `json:"user"`
	Role   string `json:"role"`
}

// Assignment records the type and optional department for an agent.
type Assignment struct {
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	DeptID    string `json:"dept_id,omitempty"`
}

// ErrDeptNotFound is returned when a department lookup finds no row.
var ErrDeptNotFound = errors.New("department not found")

// ErrDeptConflict is returned when a department name is already taken.
var ErrDeptConflict = errors.New("department name already exists")

func randHex16() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateDepartment inserts a new department with a generated ID.
func (l *Ledger) CreateDepartment(ctx context.Context, id, name string) error {
	if id == "" {
		var err error
		id, err = randHex16()
		if err != nil {
			return fmt.Errorf("create department: generate id: %w", err)
		}
	}
	if name == "" {
		return errors.New("department name required")
	}
	now := time.Now().Unix()
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO departments (id, name, created_at) VALUES (?, ?, ?)`,
		id, name, now,
	)
	if err != nil && isUniqueErr(err) {
		return ErrDeptConflict
	}
	return err
}

// ListDepartments returns all departments ordered by name.
func (l *Ledger) ListDepartments(ctx context.Context) ([]Department, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT id, name, created_at FROM departments ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var depts []Department
	for rows.Next() {
		var d Department
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt); err != nil {
			return nil, err
		}
		depts = append(depts, d)
	}
	return depts, rows.Err()
}

// GetDepartment looks up a department by id or name.
func (l *Ledger) GetDepartment(ctx context.Context, idOrName string) (*Department, error) {
	var d Department
	err := l.db.QueryRowContext(ctx,
		`SELECT id, name, created_at FROM departments WHERE id = ? OR name = ? LIMIT 1`,
		idOrName, idOrName,
	).Scan(&d.ID, &d.Name, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrDeptNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// AddMember adds or replaces a user's membership in a department.
func (l *Ledger) AddMember(ctx context.Context, deptID, user, role string) error {
	if !validRoles[role] {
		return fmt.Errorf("invalid role %q (must be admin|developer|dept-lead|analyst)", role)
	}
	dept, err := l.GetDepartment(ctx, deptID)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = l.db.ExecContext(ctx,
		`INSERT INTO department_members (dept_id, user, role, added_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(dept_id, user) DO UPDATE SET role=excluded.role, added_at=excluded.added_at`,
		dept.ID, user, role, now,
	)
	return err
}

// RemoveMember removes a user from a department.
func (l *Ledger) RemoveMember(ctx context.Context, deptID, user string) error {
	dept, err := l.GetDepartment(ctx, deptID)
	if err != nil {
		return err
	}
	_, err = l.db.ExecContext(ctx,
		`DELETE FROM department_members WHERE dept_id = ? AND user = ?`,
		dept.ID, user,
	)
	return err
}

// ListMembers returns all members of a department.
func (l *Ledger) ListMembers(ctx context.Context, deptID string) ([]Member, error) {
	dept, err := l.GetDepartment(ctx, deptID)
	if err != nil {
		return nil, err
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT dept_id, user, role FROM department_members WHERE dept_id = ? ORDER BY user`,
		dept.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.DeptID, &m.User, &m.Role); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// DeptsForUser returns the department IDs the user is a member of.
func (l *Ledger) DeptsForUser(ctx context.Context, user string) ([]string, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT dept_id FROM department_members WHERE user = ?`, user,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var depts []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		depts = append(depts, id)
	}
	return depts, rows.Err()
}

// AssignAgent sets the type and optional dept for an agent.
func (l *Ledger) AssignAgent(ctx context.Context, agentID, agentType, deptID string) error {
	if !validAgentTypes[agentType] {
		return fmt.Errorf("invalid agent_type %q (must be personal|departmental|enterprise)", agentType)
	}
	if agentType == "departmental" && deptID == "" {
		return errors.New("departmental agent requires dept_id")
	}
	if agentType != "departmental" && deptID != "" {
		return errors.New("dept_id must be empty for personal/enterprise agents")
	}
	var resolvedDeptID *string
	if deptID != "" {
		dept, err := l.GetDepartment(ctx, deptID)
		if err != nil {
			return err
		}
		resolvedDeptID = &dept.ID
	}
	now := time.Now().Unix()
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO agent_assignments (agent_id, agent_type, dept_id, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(agent_id) DO UPDATE SET agent_type=excluded.agent_type, dept_id=excluded.dept_id, updated_at=excluded.updated_at`,
		agentID, agentType, resolvedDeptID, now,
	)
	return err
}

// GetAssignment returns the assignment for agentID, or nil if not assigned.
func (l *Ledger) GetAssignment(ctx context.Context, agentID string) (*Assignment, error) {
	var a Assignment
	var deptID sql.NullString
	err := l.db.QueryRowContext(ctx,
		`SELECT agent_id, agent_type, dept_id FROM agent_assignments WHERE agent_id = ?`,
		agentID,
	).Scan(&a.AgentID, &a.AgentType, &deptID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if deptID.Valid {
		a.DeptID = deptID.String
	}
	return &a, nil
}

// AddSuperAdmin inserts user into super_admins (idempotent via INSERT OR IGNORE).
func (l *Ledger) AddSuperAdmin(ctx context.Context, user string) error {
	if user == "" {
		return errors.New("user required")
	}
	now := time.Now().Unix()
	_, err := l.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO super_admins (user, added_at) VALUES (?, ?)`,
		user, now,
	)
	return err
}

// RemoveSuperAdmin removes user from super_admins.
func (l *Ledger) RemoveSuperAdmin(ctx context.Context, user string) error {
	_, err := l.db.ExecContext(ctx,
		`DELETE FROM super_admins WHERE user = ?`, user,
	)
	return err
}

// IsSuperAdmin returns true if user is in super_admins.
func (l *Ledger) IsSuperAdmin(ctx context.Context, user string) (bool, error) {
	var count int
	err := l.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM super_admins WHERE user = ?`, user,
	).Scan(&count)
	return count > 0, err
}

// ListSuperAdmins returns all super-admin usernames.
func (l *Ledger) ListSuperAdmins(ctx context.Context) ([]string, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT user FROM super_admins ORDER BY user`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CountSuperAdmins returns the number of super-admins.
func (l *Ledger) CountSuperAdmins(ctx context.Context) (int, error) {
	var n int
	err := l.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM super_admins`,
	).Scan(&n)
	return n, err
}

// isUniqueErr returns true if err is a SQLite UNIQUE constraint violation.
func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "UNIQUE constraint failed")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// accessAdapter adapts *Ledger to the access.Ledger interface.
// It lives here so the access package does not import registryserver.
type accessAdapter struct {
	l *Ledger
}

func (a *accessAdapter) IsSuperAdmin(ctx context.Context, user string) (bool, error) {
	return a.l.IsSuperAdmin(ctx, user)
}

func (a *accessAdapter) DeptsForUser(ctx context.Context, user string) ([]string, error) {
	return a.l.DeptsForUser(ctx, user)
}

func (a *accessAdapter) GetAssignment(ctx context.Context, agentID string) (*access.Assignment, error) {
	asgn, err := a.l.GetAssignment(ctx, agentID)
	if err != nil || asgn == nil {
		return nil, err
	}
	return &access.Assignment{
		AgentID:   asgn.AgentID,
		AgentType: asgn.AgentType,
		DeptID:    asgn.DeptID,
	}, nil
}

func (a *accessAdapter) GetAgentOwner(ctx context.Context, agentID string) (string, bool, error) {
	return a.l.GetAgentOwner(ctx, agentID)
}

func (a *accessAdapter) ListAll(ctx context.Context) ([]access.Agent, error) {
	agents, err := a.l.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]access.Agent, len(agents))
	for i, ag := range agents {
		result[i] = access.Agent{AgentID: ag.AgentID, Owner: ag.Owner}
	}
	return result, nil
}
