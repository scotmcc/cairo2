package db

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// RitualReport summarizes what the state ritual did this run.
type RitualReport struct {
	DateProcessed  string              // YYYY-MM-DD
	Skipped        bool                // true if no unprocessed prior row
	SkipReason     string              // populated when Skipped=true
	Drifts         map[string]VarDrift // per-var drift snapshot (named vars from state_const.go)
	LandmarkEvents []string            // e.g., ["frustration_baseline regressed by 0.12 (was 0.7)"]
}

// VarDrift captures live → post-dream for a single var.
type VarDrift struct {
	LiveValue      float64
	PostDreamValue float64
	Delta1d        float64 // live - yesterday_live
	Delta7dAvg     float64 // smoothed 7-day delta
	Reason         string  // "regression-to-neutral", "momentum-amplified", "low-conf-trap", "no-change"
}

// Summary renders the report as a human-readable paragraph for the dream
// agent's LLM prompt. Returns a brief skip notice when the ritual was skipped.
func (r *RitualReport) Summary() string {
	if r.Skipped {
		return fmt.Sprintf("State ritual: skipped (%s).", r.SkipReason)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("State ritual ran for %s.\n", r.DateProcessed))

	for _, name := range StateVarNames {
		d, ok := r.Drifts[name]
		if !ok {
			continue
		}
		delta := d.PostDreamValue - d.LiveValue
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		sb.WriteString(fmt.Sprintf("  %s: %.3f → %.3f (%s%.3f, %s)\n",
			name, d.LiveValue, d.PostDreamValue, sign, delta, d.Reason))
	}

	if len(r.LandmarkEvents) > 0 {
		sb.WriteString("Landmark events:\n")
		for _, ev := range r.LandmarkEvents {
			sb.WriteString("  * " + ev + "\n")
		}
	}

	sb.WriteString("These shifts reflect overnight integration — regression where heat accumulated, ")
	sb.WriteString("amplification where relationship momentum was sustained. ")
	sb.WriteString("Consider encoding meaningful drift arcs as memories if they warrant narrative.")
	return sb.String()
}

// RunDreamRitual reads state_daily, applies the ritual transforms, writes
// post_dream_*, stamps dream_processed_at, and returns the report.
// Idempotent: skips if the most recent unprocessed prior row already has
// dream_processed_at set.
func RunDreamRitual(db *DB) (*RitualReport, error) {
	// Pull all rows; we need the most-recent unprocessed prior and up to 7
	// days of history for momentum context. Fetch 8 to have room.
	rows, err := db.State.LastN(8)
	if err != nil {
		return nil, fmt.Errorf("ritual: LastN: %w", err)
	}

	// Identify the target row: most recent row with dream_processed_at IS NULL.
	// If today's row is in the set, skip it — we never process today's live row
	// (it's still accumulating). Look for the first row that is prior to today.
	today := nowDate()
	var target *State
	var history []*State // rows older than target, in descending date order

	for i, row := range rows {
		if row.Date >= today {
			// Skip today's row (or any future row, which shouldn't exist).
			continue
		}
		if row.DreamProcessedAt != nil {
			// Already processed — nothing left to do.
			return &RitualReport{
				DateProcessed: row.Date,
				Skipped:       true,
				SkipReason:    fmt.Sprintf("most recent prior row (%s) already processed", row.Date),
			}, nil
		}
		// This is the target.
		target = row
		history = rows[i+1:] // everything older
		break
	}

	if target == nil {
		return &RitualReport{
			Skipped:    true,
			SkipReason: "no unprocessed prior row found",
		}, nil
	}

	// Build 7-day history slice (values older than target, in ascending order
	// so index 0 is immediately prior to target).
	// history is descending, so reverse it for the momentum helper.
	window := make([]*State, len(history))
	for i, s := range history {
		window[len(history)-1-i] = s
	}

	// Compute post-dream values for each variable.
	type varSpec struct {
		name    string
		live    float64
		yesterd *float64 // live value from the row immediately before target
	}

	var yesterdayRow *State
	if len(history) > 0 {
		yesterdayRow = history[0] // most-recent prior to target (descending order)
	}

	specs := []varSpec{
		{StateVarConfidence, target.Confidence, fieldPtr(yesterdayRow, StateVarConfidence)},
		{StateVarTrustInUser, target.TrustInUser, fieldPtr(yesterdayRow, StateVarTrustInUser)},
		{StateVarWarmth, target.Warmth, fieldPtr(yesterdayRow, StateVarWarmth)},
		{StateVarFrustrationBaseline, target.FrustrationBaseline, fieldPtr(yesterdayRow, StateVarFrustrationBaseline)},
		{StateVarSenseOfAgency, target.SenseOfAgency, fieldPtr(yesterdayRow, StateVarSenseOfAgency)},
		{StateVarAttunement, target.Attunement, fieldPtr(yesterdayRow, StateVarAttunement)},
		{StateVarGroundedness, target.Groundedness, fieldPtr(yesterdayRow, StateVarGroundedness)},
	}

	drifts := make(map[string]VarDrift, len(specs))
	post := PostDreamValues{}
	var landmarks []string

	for _, sp := range specs {
		d1 := 0.0
		if sp.yesterd != nil {
			d1 = sp.live - *sp.yesterd
		}
		d7 := delta7dAvg(sp.name, target, window)

		postVal, reason := applyTransform(sp.name, sp.live, d1, d7, window)

		drifts[sp.name] = VarDrift{
			LiveValue:      sp.live,
			PostDreamValue: postVal,
			Delta1d:        d1,
			Delta7dAvg:     d7,
			Reason:         reason,
		}

		// Flag landmark: any shift >= 0.05
		shift := math.Abs(postVal - sp.live)
		if shift >= 0.05 {
			landmarks = append(landmarks, fmt.Sprintf(
				"%s %s by %.2f (was %.3f)",
				sp.name,
				landmarkVerb(postVal-sp.live),
				shift,
				sp.live,
			))
		}

		// Assign to PostDreamValues struct.
		setPostDream(&post, sp.name, postVal)
	}

	if err := db.State.WritePostDream(target.Date, post); err != nil {
		return nil, fmt.Errorf("ritual: WritePostDream: %w", err)
	}

	return &RitualReport{
		DateProcessed:  target.Date,
		Skipped:        false,
		Drifts:         drifts,
		LandmarkEvents: landmarks,
	}, nil
}

// applyTransform applies the appropriate ritual transform for a state variable
// and returns (postDreamValue, reason).
func applyTransform(name string, live, delta1d, delta7dAvg float64, window []*State) (float64, string) {
	switch name {
	case StateVarFrustrationBaseline:
		// Regression-to-neutral toward 0.3.
		post := live + 0.3*(0.3-live)
		return clamp01(post), "regression-to-neutral"

	case StateVarGroundedness:
		// Regression-to-neutral only when below 0.5 — high groundedness is a feature.
		if live < 0.5 {
			post := live + 0.3*(0.5-live)
			return clamp01(post), "regression-to-neutral"
		}
		return live, "no-change"

	case StateVarTrustInUser, StateVarWarmth, StateVarAttunement:
		// Momentum amplification.
		return momentumAmplify(name, live, delta1d, delta7dAvg, window)

	case StateVarConfidence, StateVarSenseOfAgency:
		// Self-var trap mitigation.
		return selfVarTransform(live)

	default:
		return live, "no-change"
	}
}

// momentumAmplify applies the momentum-amplification formula for relationship vars.
func momentumAmplify(name string, live, delta1d, delta7dAvg float64, window []*State) (float64, string) {
	// Need at least one prior day for any momentum logic.
	if len(window) < 1 {
		return live, "no-change"
	}

	hasNegDay := hasNegativeDayInWindow(name, window)

	if delta7dAvg > 0 && delta1d > 0 && !hasNegDay {
		bump := 0.5 * delta1d
		return clamp01(live + bump), "momentum-amplified"
	}
	if delta1d < -0.05 {
		// Leave the wound.
		return live, "no-change"
	}
	return live, "no-change"
}

// selfVarTransform applies the low-confidence trap mitigation formula.
func selfVarTransform(live float64) (float64, string) {
	if live < 0.3 {
		post := live + 0.1*(0.5-live)
		return clamp01(post), "low-conf-trap"
	}
	if live > 0.8 {
		return clamp01(live - 0.02), "regression-to-neutral"
	}
	return live, "no-change"
}

// delta7dAvg computes (live - row_7_days_ago_live) / 7 using the available window.
// window is in ascending date order (oldest first). Returns 0 if window is empty.
func delta7dAvg(name string, target *State, window []*State) float64 {
	if len(window) == 0 {
		return 0
	}
	oldest := window[0]
	days := float64(len(window)) // number of days in window
	if days < 1 {
		return 0
	}
	targetLive := fieldVal(target, name)
	oldestLive := fieldVal(oldest, name)
	return (targetLive - oldestLive) / days
}

// hasNegativeDayInWindow returns true if any day in the 7-day window shows a
// negative delta for the given variable (consecutive day-over-day comparison).
// window is in ascending order (oldest first).
func hasNegativeDayInWindow(name string, window []*State) bool {
	// We need at least 2 rows to compute day-over-day.
	if len(window) < 2 {
		return false
	}
	for i := 1; i < len(window); i++ {
		prev := fieldVal(window[i-1], name)
		curr := fieldVal(window[i], name)
		if curr-prev < 0 {
			return true
		}
	}
	return false
}

// fieldVal returns the live value for a named state variable from a State row.
func fieldVal(s *State, name string) float64 {
	if s == nil {
		return 0
	}
	switch name {
	case StateVarConfidence:
		return s.Confidence
	case StateVarTrustInUser:
		return s.TrustInUser
	case StateVarWarmth:
		return s.Warmth
	case StateVarFrustrationBaseline:
		return s.FrustrationBaseline
	case StateVarSenseOfAgency:
		return s.SenseOfAgency
	case StateVarAttunement:
		return s.Attunement
	case StateVarGroundedness:
		return s.Groundedness
	default:
		return 0
	}
}

// fieldPtr returns a pointer to the live value for a named variable, or nil
// if the state row is nil.
func fieldPtr(s *State, name string) *float64 {
	if s == nil {
		return nil
	}
	v := fieldVal(s, name)
	return &v
}

// setPostDream assigns a computed post-dream value to the appropriate field.
func setPostDream(p *PostDreamValues, name string, val float64) {
	switch name {
	case StateVarConfidence:
		p.Confidence = val
	case StateVarTrustInUser:
		p.TrustInUser = val
	case StateVarWarmth:
		p.Warmth = val
	case StateVarFrustrationBaseline:
		p.FrustrationBaseline = val
	case StateVarSenseOfAgency:
		p.SenseOfAgency = val
	case StateVarAttunement:
		p.Attunement = val
	case StateVarGroundedness:
		p.Groundedness = val
	}
}

// clamp01 clamps v to [0.0, 1.0].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// landmarkVerb returns "regressed" or "climbed" based on sign.
func landmarkVerb(delta float64) string {
	if delta < 0 {
		return "regressed"
	}
	return "climbed"
}

// nowDate returns the current local date as "YYYY-MM-DD".
func nowDate() string {
	return time.Now().Format("2006-01-02")
}
