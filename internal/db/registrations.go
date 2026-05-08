package db

import "database/sql"

type RegistrationQ struct{ db *sql.DB }

func (q *RegistrationQ) Get(registryURL string) (string, error) {
	var agentID string
	err := q.db.QueryRow(`SELECT agent_id FROM registrations WHERE registry_url = ?`, registryURL).Scan(&agentID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return agentID, err
}

func (q *RegistrationQ) Upsert(registryURL, agentID string) error {
	_, err := q.db.Exec(
		`INSERT INTO registrations(registry_url, agent_id, registered_at) VALUES (?, ?, unixepoch())
		 ON CONFLICT(registry_url) DO UPDATE SET agent_id = excluded.agent_id, registered_at = unixepoch()`,
		registryURL, agentID)
	return err
}
