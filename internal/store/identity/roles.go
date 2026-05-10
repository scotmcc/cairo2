package identity

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

type Role struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Model         string    `json:"model"`
	BasePromptKey string    `json:"base_prompt_key"`
	Tools         string    `json:"tools"`    // JSON array of tool names
	Think         string    `json:"think"`    // "" inherit | "true" | "false"
	Consider      bool      `json:"consider"` // false disables consider for this role; default true
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type RoleQ struct{ db *sql.DB }

func NewRoleQ(db *sql.DB) *RoleQ { return &RoleQ{db: db} }

func (q *RoleQ) Get(name string) (*Role, error) {
	row := q.db.QueryRow(
		`SELECT id, name, description, model, base_prompt_key, tools, think, consider, created_at, updated_at
		 FROM roles WHERE name = ?`, name)
	return scanRole(row)
}

func (q *RoleQ) List() ([]*Role, error) {
	rows, err := q.db.Query(
		`SELECT id, name, description, model, base_prompt_key, tools, think, consider, created_at, updated_at
		 FROM roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Role
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AllowedTools returns the parsed tool whitelist for a role.
// Empty (nil) means unrestricted — the role may call any built-in tool.
// A non-empty slice is a whitelist intersected against registered tools.
// If the role doesn't exist or the tools column is empty/malformed, returns nil.
func (q *RoleQ) AllowedTools(roleName string) ([]string, error) {
	if roleName == "" {
		return nil, nil
	}
	var raw string
	err := q.db.QueryRow(`SELECT COALESCE(tools,'') FROM roles WHERE name = ?`, roleName).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		log.Printf("warn: role %q has malformed tools JSON: %v — treating as unrestricted", roleName, err)
		return nil, nil
	}
	return names, nil
}

// ModelFor returns the model configured for the given role, falling back to
// the global config model if the role has no model set.
func (q *RoleQ) ModelFor(roleName string) (string, error) {
	if roleName == "" {
		return "", nil
	}
	var model string
	err := q.db.QueryRow(`SELECT model FROM roles WHERE name = ?`, roleName).Scan(&model)
	if err != nil || model == "" {
		return "", nil // caller falls back to global config
	}
	return model, nil
}

// ThinkFor returns the per-role think override for the given role.
// hasOverride is true only when the role's think column is non-empty.
// override will be "true" or "false" in that case. When hasOverride is false,
// the caller should fall back to the global think config.
func (q *RoleQ) ThinkFor(roleName string) (override string, hasOverride bool, err error) {
	if roleName == "" {
		return "", false, nil
	}
	var think string
	err = q.db.QueryRow(`SELECT think FROM roles WHERE name = ?`, roleName).Scan(&think)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if think == "" {
		return "", false, nil
	}
	return think, true, nil
}

func (q *RoleQ) Upsert(name, description, model, basePromptKey, tools string) error {
	_, err := q.db.Exec(`
		INSERT INTO roles(name, description, model, base_prompt_key, tools, updated_at)
		VALUES(?, ?, ?, ?, ?, unixepoch())
		ON CONFLICT(name) DO UPDATE SET
			description     = CASE WHEN excluded.description    != '' THEN excluded.description    ELSE description    END,
			model           = CASE WHEN excluded.model          != '' THEN excluded.model          ELSE model          END,
			base_prompt_key = CASE WHEN excluded.base_prompt_key != '' THEN excluded.base_prompt_key ELSE base_prompt_key END,
			tools           = CASE WHEN excluded.tools          != '' THEN excluded.tools          ELSE tools          END,
			updated_at      = unixepoch()`,
		name, description, model, basePromptKey, tools)
	return err
}

// SetModel updates just the model for an existing role. Flips source='user'
// so seedRoles' UPSERT-with-source-guard doesn't overwrite the choice on
// next Open.
func (q *RoleQ) SetModel(name, model string) error {
	_, err := q.db.Exec(`UPDATE roles SET model = ?, source = 'user', updated_at = unixepoch() WHERE name = ?`, model, name)
	return err
}

// SetThink updates the per-role think override.
// Allowed values: "" (inherit global), "true", "false".
// Flips source='user' so seedRoles doesn't overwrite the choice.
func (q *RoleQ) SetThink(name, value string) error {
	switch value {
	case "", "true", "false":
		// ok
	default:
		return fmt.Errorf("think must be \"\", \"true\", or \"false\" — got %q", value)
	}
	_, err := q.db.Exec(`UPDATE roles SET think = ?, source = 'user', updated_at = unixepoch() WHERE name = ?`, value, name)
	return err
}

func scanRole(row scanner) (*Role, error) {
	var r Role
	var createdAt, updatedAt int64
	err := row.Scan(&r.ID, &r.Name, &r.Description, &r.Model, &r.BasePromptKey, &r.Tools, &r.Think, &r.Consider, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = time.Unix(createdAt, 0)
	r.UpdatedAt = time.Unix(updatedAt, 0)
	return &r, nil
}
