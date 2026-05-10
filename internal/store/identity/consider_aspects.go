package identity

import "database/sql"

// ConsiderAspect is one entry in the consider_aspects table.
type ConsiderAspect struct {
	Name     string
	Traits   string
	Enabled  bool
	Position int
}

// ConsiderAspectQ provides query methods for the consider_aspects table.
type ConsiderAspectQ struct{ db *sql.DB }

func NewConsiderAspectQ(db *sql.DB) *ConsiderAspectQ { return &ConsiderAspectQ{db: db} }

// List returns all aspects ordered by position, then name.
func (q *ConsiderAspectQ) List() ([]*ConsiderAspect, error) {
	rows, err := q.db.Query(
		`SELECT name, traits, enabled, position FROM consider_aspects ORDER BY position ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConsiderAspects(rows)
}

// ListEnabled returns only enabled aspects ordered by position, then name.
func (q *ConsiderAspectQ) ListEnabled() ([]*ConsiderAspect, error) {
	rows, err := q.db.Query(
		`SELECT name, traits, enabled, position FROM consider_aspects WHERE enabled = 1 ORDER BY position ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConsiderAspects(rows)
}

// Add inserts a new aspect. Returns an error if the name already exists.
// New rows are stamped source='user' so seedConsiderAspects' UPSERT doesn't
// touch them on next Open.
func (q *ConsiderAspectQ) Add(name, traits string) error {
	_, err := q.db.Exec(
		`INSERT INTO consider_aspects(name, traits, source) VALUES(?, ?, 'user')`, name, traits)
	return err
}

// Update replaces the traits for an existing aspect. Flips source='user' so
// seedConsiderAspects' UPSERT-with-source-guard preserves the edit on next Open.
func (q *ConsiderAspectQ) Update(name, traits string) error {
	_, err := q.db.Exec(
		`UPDATE consider_aspects SET traits = ?, source = 'user' WHERE name = ?`, traits, name)
	return err
}

// SetEnabled enables or disables an aspect by name.
func (q *ConsiderAspectQ) SetEnabled(name string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := q.db.Exec(
		`UPDATE consider_aspects SET enabled = ? WHERE name = ?`, v, name)
	return err
}

// Upsert inserts or replaces an aspect by name, stamping source='user' so
// seedConsiderAspects does not overwrite user edits on next Open.
func (q *ConsiderAspectQ) Upsert(name, traits string, enabled bool, position int) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := q.db.Exec(
		`INSERT INTO consider_aspects(name, traits, enabled, position, source)
		 VALUES(?, ?, ?, ?, 'user')
		 ON CONFLICT(name) DO UPDATE SET
		   traits=excluded.traits, enabled=excluded.enabled,
		   position=excluded.position, source='user'`,
		name, traits, v, position)
	return err
}

// Get returns a single aspect by name. Returns sql.ErrNoRows when not found.
func (q *ConsiderAspectQ) Get(name string) (*ConsiderAspect, error) {
	var a ConsiderAspect
	var enabled int
	err := q.db.QueryRow(
		`SELECT name, traits, enabled, position FROM consider_aspects WHERE name = ?`, name,
	).Scan(&a.Name, &a.Traits, &enabled, &a.Position)
	if err != nil {
		return nil, err
	}
	a.Enabled = enabled != 0
	return &a, nil
}

// Delete removes an aspect by name.
func (q *ConsiderAspectQ) Delete(name string) error {
	_, err := q.db.Exec(`DELETE FROM consider_aspects WHERE name = ?`, name)
	return err
}

func scanConsiderAspects(rows *sql.Rows) ([]*ConsiderAspect, error) {
	var out []*ConsiderAspect
	for rows.Next() {
		var a ConsiderAspect
		var enabled int
		if err := rows.Scan(&a.Name, &a.Traits, &enabled, &a.Position); err != nil {
			return nil, err
		}
		a.Enabled = enabled != 0
		out = append(out, &a)
	}
	return out, rows.Err()
}
