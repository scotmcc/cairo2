package identity

import (
	"database/sql"
	"fmt"
	"os"
	"time"
)

// State holds one row from state_daily.
type State struct {
	Date string

	// Live values — updated throughout the day via Apply.
	Confidence          float64
	TrustInUser         float64
	Warmth              float64
	FrustrationBaseline float64
	SenseOfAgency       float64
	Attunement          float64
	Groundedness        float64

	// Post-dream values — written once per night by the dream ritual.
	// Nil when the dream pass hasn't run yet for this row.
	PostDreamConfidence          *float64
	PostDreamTrustInUser         *float64
	PostDreamWarmth              *float64
	PostDreamFrustrationBaseline *float64
	PostDreamSenseOfAgency       *float64
	PostDreamAttunement          *float64
	PostDreamGroundedness        *float64

	UpdateCount      int
	UpdatedAt        int64
	DreamProcessedAt *int64
}

// PostDreamValues carries the seven post-dream fields written by the dream ritual.
type PostDreamValues struct {
	Confidence          float64
	TrustInUser         float64
	Warmth              float64
	FrustrationBaseline float64
	SenseOfAgency       float64
	Attunement          float64
	Groundedness        float64
}

// StateQ owns all queries against the state_daily table.
type StateQ struct{ db *sql.DB }

func NewStateQ(db *sql.DB) *StateQ { return &StateQ{db: db} }

// Today ensures today's row exists (creating it via ensureTodayRow if needed)
// and returns the current values.
func (q *StateQ) Today() (*State, error) {
	today := time.Now().Format("2006-01-02")
	if err := q.ensureTodayRow(today); err != nil {
		return nil, fmt.Errorf("state.Today: ensure row: %w", err)
	}
	return q.getByDate(today)
}

// Last returns the most recent row regardless of date (may be today or earlier).
func (q *StateQ) Last() (*State, error) {
	row := q.db.QueryRow(
		`SELECT date, confidence, trust_in_user, warmth, frustration_baseline,
		        sense_of_agency, attunement, groundedness,
		        post_dream_confidence, post_dream_trust_in_user, post_dream_warmth,
		        post_dream_frustration_baseline, post_dream_sense_of_agency,
		        post_dream_attunement, post_dream_groundedness,
		        update_count, updated_at, dream_processed_at
		 FROM state_daily ORDER BY date DESC LIMIT 1`)
	s, err := scanState(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

// LastN returns the most recent n rows, descending by date.
func (q *StateQ) LastN(n int) ([]*State, error) {
	rows, err := q.db.Query(
		`SELECT date, confidence, trust_in_user, warmth, frustration_baseline,
		        sense_of_agency, attunement, groundedness,
		        post_dream_confidence, post_dream_trust_in_user, post_dream_warmth,
		        post_dream_frustration_baseline, post_dream_sense_of_agency,
		        post_dream_attunement, post_dream_groundedness,
		        update_count, updated_at, dream_processed_at
		 FROM state_daily ORDER BY date DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*State
	for rows.Next() {
		s, err := scanState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Apply adds delta to varName, clamps the result to [0,1], increments
// update_count, and bumps updated_at. Returns an error for unknown var names.
func (q *StateQ) Apply(varName string, delta float64) error {
	if _, ok := stateVarSet[varName]; !ok {
		return fmt.Errorf("state.Apply: unknown var %q", varName)
	}
	today := time.Now().Format("2006-01-02")
	if err := q.ensureTodayRow(today); err != nil {
		return fmt.Errorf("state.Apply: ensure row: %w", err)
	}

	now := time.Now().Unix()
	// Read current value, apply delta, clamp, write back.
	// Using a single UPDATE with arithmetic keeps the operation atomic on
	// the single write connection.
	//nolint:gosec // varName is validated against stateVarSet above
	query := fmt.Sprintf(
		`UPDATE state_daily
		 SET %s        = MIN(1.0, MAX(0.0, %s + ?)),
		     update_count = update_count + 1,
		     updated_at   = ?
		 WHERE date = ?`, varName, varName)
	_, err := q.db.Exec(query, delta, now, today)
	return err
}

// WritePostDream writes the seven post_dream_* columns and stamps
// dream_processed_at for the given date.
func (q *StateQ) WritePostDream(date string, v PostDreamValues) error {
	now := time.Now().Unix()
	_, err := q.db.Exec(
		`UPDATE state_daily
		 SET post_dream_confidence           = ?,
		     post_dream_trust_in_user        = ?,
		     post_dream_warmth               = ?,
		     post_dream_frustration_baseline = ?,
		     post_dream_sense_of_agency      = ?,
		     post_dream_attunement           = ?,
		     post_dream_groundedness         = ?,
		     dream_processed_at              = ?
		 WHERE date = ?`,
		v.Confidence, v.TrustInUser, v.Warmth, v.FrustrationBaseline,
		v.SenseOfAgency, v.Attunement, v.Groundedness,
		now, date)
	return err
}

// ensureTodayRow inserts today's row if it does not exist, forward-copying
// from the most recent prior row. If post_dream_* values are set on that prior
// row they become today's starting live values; otherwise the live values are
// used. If no prior row exists, schema defaults are inserted.
func (q *StateQ) ensureTodayRow(today string) error {
	// Fast path: row already exists.
	var exists int
	err := q.db.QueryRow(
		`SELECT COUNT(*) FROM state_daily WHERE date = ?`, today).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	now := time.Now().Unix()

	// Look for the most recent prior row.
	prior, err := q.Last()
	if err != nil {
		return err
	}
	if prior == nil || prior.Date >= today {
		// No prior row (or only future rows, which shouldn't happen) — use schema defaults.
		_, err = q.db.Exec(
			`INSERT OR IGNORE INTO state_daily(date, updated_at) VALUES (?, ?)`,
			today, now)
		return err
	}

	// Auto-trigger: if the prior row hasn't been dream-processed yet, run the
	// ritual against it now (silently) before forward-copying. This prevents
	// drift from compounding when Scot skips the nightly `cairo dream` run.
	// The ritual is idempotent, so calling it here is safe even if dream ran
	// between the Last() call above and this point.
	if prior.DreamProcessedAt == nil {
		if _, err := RunDreamRitual(q); err != nil {
			// Log but do not block the rollover — a ritual failure is not a
			// reason to refuse to create today's row.
			fmt.Fprintf(os.Stderr, "state: auto-ritual on %s: %v\n", prior.Date, err)
		} else {
			// Re-read the prior row so forward-copy picks up post_dream_* cols.
			if updated, rerr := q.Last(); rerr == nil && updated != nil && updated.Date == prior.Date {
				prior = updated
			}
		}
	}

	// Forward-copy: prefer post_dream_* if non-null, else live values.
	confidence := pickFloat(prior.PostDreamConfidence, prior.Confidence)
	trustInUser := pickFloat(prior.PostDreamTrustInUser, prior.TrustInUser)
	warmth := pickFloat(prior.PostDreamWarmth, prior.Warmth)
	frustrationBaseline := pickFloat(prior.PostDreamFrustrationBaseline, prior.FrustrationBaseline)
	senseOfAgency := pickFloat(prior.PostDreamSenseOfAgency, prior.SenseOfAgency)
	attunement := pickFloat(prior.PostDreamAttunement, prior.Attunement)
	groundedness := pickFloat(prior.PostDreamGroundedness, prior.Groundedness)

	_, err = q.db.Exec(
		`INSERT OR IGNORE INTO state_daily(
		     date, confidence, trust_in_user, warmth, frustration_baseline,
		     sense_of_agency, attunement, groundedness, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		today,
		confidence, trustInUser, warmth, frustrationBaseline,
		senseOfAgency, attunement, groundedness,
		now)
	return err
}

// getByDate returns the row for an exact date string. Returns nil, nil when
// no row exists.
func (q *StateQ) getByDate(date string) (*State, error) {
	row := q.db.QueryRow(
		`SELECT date, confidence, trust_in_user, warmth, frustration_baseline,
		        sense_of_agency, attunement, groundedness,
		        post_dream_confidence, post_dream_trust_in_user, post_dream_warmth,
		        post_dream_frustration_baseline, post_dream_sense_of_agency,
		        post_dream_attunement, post_dream_groundedness,
		        update_count, updated_at, dream_processed_at
		 FROM state_daily WHERE date = ?`, date)
	s, err := scanState(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

// pickFloat returns *ptr if non-nil, else fallback.
func pickFloat(ptr *float64, fallback float64) float64 {
	if ptr != nil {
		return *ptr
	}
	return fallback
}

func scanState(row scanner) (*State, error) {
	var s State
	err := row.Scan(
		&s.Date,
		&s.Confidence, &s.TrustInUser, &s.Warmth, &s.FrustrationBaseline,
		&s.SenseOfAgency, &s.Attunement, &s.Groundedness,
		&s.PostDreamConfidence, &s.PostDreamTrustInUser, &s.PostDreamWarmth,
		&s.PostDreamFrustrationBaseline, &s.PostDreamSenseOfAgency,
		&s.PostDreamAttunement, &s.PostDreamGroundedness,
		&s.UpdateCount, &s.UpdatedAt, &s.DreamProcessedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
