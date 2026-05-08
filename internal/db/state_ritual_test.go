package db

import (
	"math"
	"testing"
	"time"
)

// approxEqual returns true when a and b differ by less than epsilon.
func approxEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

const eps = 1e-9

// makeTestDB opens a fresh in-memory DB suitable for ritual tests.
func makeTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenAt(":memory:")
	if err != nil {
		t.Fatalf("OpenAt :memory:: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// insertStateRow writes a state_daily row directly for the given date.
// post_dream_* and dream_processed_at are left NULL (unprocessed).
func insertStateRow(t *testing.T, db *DB, date string, s State) {
	t.Helper()
	now := time.Now().Unix()
	_, err := db.sql.Exec(
		`INSERT OR REPLACE INTO state_daily(
			date, confidence, trust_in_user, warmth, frustration_baseline,
			sense_of_agency, attunement, groundedness, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		date,
		s.Confidence, s.TrustInUser, s.Warmth, s.FrustrationBaseline,
		s.SenseOfAgency, s.Attunement, s.Groundedness,
		now,
	)
	if err != nil {
		t.Fatalf("insertStateRow %s: %v", date, err)
	}
}

// markProcessed stamps dream_processed_at on the given date row.
func markProcessed(t *testing.T, db *DB, date string) {
	t.Helper()
	_, err := db.sql.Exec(
		`UPDATE state_daily SET dream_processed_at = ? WHERE date = ?`,
		time.Now().Unix(), date,
	)
	if err != nil {
		t.Fatalf("markProcessed %s: %v", date, err)
	}
}

// yesterday returns the date string for the previous calendar day.
func yesterday() string {
	return time.Now().AddDate(0, 0, -1).Format("2006-01-02")
}

// daysAgo returns the date string for n days before today.
func daysAgo(n int) string {
	return time.Now().AddDate(0, 0, -n).Format("2006-01-02")
}

// ───────────────────────────────────────────────────────────────────────────
// Regression-to-neutral tests
// ───────────────────────────────────────────────────────────────────────────

// Test_Ritual_WentToBedAngry verifies that frustration 0.7 regresses to 0.58.
// Formula: 0.7 + 0.3*(0.3 - 0.7) = 0.58
func Test_Ritual_WentToBedAngry(t *testing.T) {
	db := makeTestDB(t)

	row := State{FrustrationBaseline: 0.7}
	row.Confidence = 0.5
	row.TrustInUser = 0.4
	row.Warmth = 0.4
	row.SenseOfAgency = 0.5
	row.Attunement = 0.4
	row.Groundedness = 0.5
	insertStateRow(t, db, yesterday(), row)

	report, err := RunDreamRitual(db)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}
	if report.Skipped {
		t.Fatalf("expected ritual to run, got skip: %s", report.SkipReason)
	}

	d := report.Drifts[StateVarFrustrationBaseline]
	want := 0.58
	if !approxEqual(d.PostDreamValue, want, eps) {
		t.Errorf("frustration_baseline post-dream: got %.6f, want %.6f", d.PostDreamValue, want)
	}
	if d.Reason != "regression-to-neutral" {
		t.Errorf("reason: got %q, want %q", d.Reason, "regression-to-neutral")
	}
}

// ───────────────────────────────────────────────────────────────────────────
// Momentum amplification tests
// ───────────────────────────────────────────────────────────────────────────

// Test_Ritual_TrustClimbedThreeDaysRunning verifies momentum amplification when
// delta_1d > 0, delta_7d_avg > 0, and no negative day exists.
func Test_Ritual_TrustClimbedThreeDaysRunning(t *testing.T) {
	db := makeTestDB(t)

	// Seed 4 days: trust rising each day.
	trustVals := []float64{0.40, 0.42, 0.44, 0.46}
	for i, tv := range trustVals {
		row := State{
			Confidence: 0.5, TrustInUser: tv, Warmth: 0.4,
			FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
			Attunement: 0.4, Groundedness: 0.5,
		}
		// i=0 is the oldest (3 days ago), i=3 is yesterday.
		insertStateRow(t, db, daysAgo(3-i+1), row)
	}

	report, err := RunDreamRitual(db)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}
	if report.Skipped {
		t.Fatalf("skipped: %s", report.SkipReason)
	}

	d := report.Drifts[StateVarTrustInUser]
	if d.Reason != "momentum-amplified" {
		t.Errorf("reason: got %q, want %q", d.Reason, "momentum-amplified")
	}
	// bump = 0.5 * delta_1d; delta_1d = 0.46 - 0.44 = 0.02 → bump = 0.01
	want := clamp01(0.46 + 0.5*0.02)
	if !approxEqual(d.PostDreamValue, want, eps) {
		t.Errorf("trust post-dream: got %.6f, want %.6f", d.PostDreamValue, want)
	}
}

// Test_Ritual_TrustCrackedSharply verifies that delta_1d = -0.06 leaves the
// wound: post_dream stays at live value (no cushion).
func Test_Ritual_TrustCrackedSharply(t *testing.T) {
	db := makeTestDB(t)

	// Yesterday's trust was 0.50; today's (target) is 0.44 (drop of 0.06).
	insertStateRow(t, db, daysAgo(2), State{
		Confidence: 0.5, TrustInUser: 0.50, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})
	insertStateRow(t, db, yesterday(), State{
		Confidence: 0.5, TrustInUser: 0.44, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	report, err := RunDreamRitual(db)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}
	if report.Skipped {
		t.Fatalf("skipped: %s", report.SkipReason)
	}

	d := report.Drifts[StateVarTrustInUser]
	if !approxEqual(d.PostDreamValue, d.LiveValue, eps) {
		t.Errorf("trust should be unchanged (wound left open): got %.6f, live %.6f",
			d.PostDreamValue, d.LiveValue)
	}
}

// ───────────────────────────────────────────────────────────────────────────
// Self-var trap mitigation tests
// ───────────────────────────────────────────────────────────────────────────

// Test_Ritual_LowConfidenceTrap verifies confidence 0.2 → 0.23.
// Formula: 0.2 + 0.1*(0.5 - 0.2) = 0.23
func Test_Ritual_LowConfidenceTrap(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, yesterday(), State{
		Confidence: 0.2, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	report, err := RunDreamRitual(db)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}

	d := report.Drifts[StateVarConfidence]
	want := 0.23
	if !approxEqual(d.PostDreamValue, want, eps) {
		t.Errorf("confidence post-dream: got %.6f, want %.6f", d.PostDreamValue, want)
	}
	if d.Reason != "low-conf-trap" {
		t.Errorf("reason: got %q, want %q", d.Reason, "low-conf-trap")
	}
}

// Test_Ritual_HighConfidenceHumilityCheck verifies confidence 0.85 → 0.83.
func Test_Ritual_HighConfidenceHumilityCheck(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, yesterday(), State{
		Confidence: 0.85, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	report, err := RunDreamRitual(db)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}

	d := report.Drifts[StateVarConfidence]
	want := 0.83
	if !approxEqual(d.PostDreamValue, want, eps) {
		t.Errorf("confidence post-dream: got %.6f, want %.6f", d.PostDreamValue, want)
	}
	if d.Reason != "regression-to-neutral" {
		t.Errorf("reason: got %q, want %q", d.Reason, "regression-to-neutral")
	}
}

// Test_Ritual_MidRangeNoChange verifies confidence 0.55 stays at 0.55.
func Test_Ritual_MidRangeNoChange(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, yesterday(), State{
		Confidence: 0.55, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	report, err := RunDreamRitual(db)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}

	d := report.Drifts[StateVarConfidence]
	if !approxEqual(d.PostDreamValue, d.LiveValue, eps) {
		t.Errorf("confidence should be unchanged: got %.6f, live %.6f",
			d.PostDreamValue, d.LiveValue)
	}
	if d.Reason != "no-change" {
		t.Errorf("reason: got %q, want %q", d.Reason, "no-change")
	}
}

// ───────────────────────────────────────────────────────────────────────────
// Idempotency test
// ───────────────────────────────────────────────────────────────────────────

// Test_Ritual_SkippedIfAlreadyProcessed verifies that calling RunDreamRitual
// twice on the same date returns Skipped=true on the second call.
func Test_Ritual_SkippedIfAlreadyProcessed(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, yesterday(), State{
		Confidence: 0.5, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.7, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	// First run — should succeed.
	r1, err := RunDreamRitual(db)
	if err != nil {
		t.Fatalf("first RunDreamRitual: %v", err)
	}
	if r1.Skipped {
		t.Fatalf("first call should not skip: %s", r1.SkipReason)
	}

	// Second run — should be idempotent (skipped).
	r2, err := RunDreamRitual(db)
	if err != nil {
		t.Fatalf("second RunDreamRitual: %v", err)
	}
	if !r2.Skipped {
		t.Error("second call should be skipped (already processed)")
	}
}

// ───────────────────────────────────────────────────────────────────────────
// Auto-trigger test
// ───────────────────────────────────────────────────────────────────────────

// Test_Ritual_AutoTriggerViaEnsureTodayRow verifies that ensureTodayRow
// runs the ritual against yesterday's unprocessed row before forward-copying it.
// We simulate a "new day" by deleting the seed-inserted today row, then
// replacing yesterday's row with high frustration, then calling Today() which
// re-enters ensureTodayRow and hits the auto-trigger branch.
func Test_Ritual_AutoTriggerViaEnsureTodayRow(t *testing.T) {
	db := makeTestDB(t)

	today := time.Now().Format("2006-01-02")

	// Delete the seed row for today so ensureTodayRow will re-create it.
	_, err := db.sql.Exec(`DELETE FROM state_daily WHERE date = ?`, today)
	if err != nil {
		t.Fatalf("delete today seed row: %v", err)
	}

	// Insert yesterday's row with high frustration and no dream_processed_at.
	insertStateRow(t, db, yesterday(), State{
		Confidence: 0.5, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.8, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	// Trigger ensureTodayRow by calling Today() — no today row exists, so it
	// will auto-trigger the ritual before forward-copying yesterday's values.
	_, err = db.State.Today()
	if err != nil {
		t.Fatalf("Today: %v", err)
	}

	// Now check: yesterday's row should have dream_processed_at set.
	yRow, err := db.State.LastN(2)
	if err != nil {
		t.Fatalf("LastN: %v", err)
	}
	var ystRow *State
	for _, r := range yRow {
		if r.Date == yesterday() {
			ystRow = r
			break
		}
	}
	if ystRow == nil {
		t.Fatal("yesterday's row not found")
	}
	if ystRow.DreamProcessedAt == nil {
		t.Error("auto-trigger should have set dream_processed_at on yesterday's row")
	}
	if ystRow.PostDreamFrustrationBaseline == nil {
		t.Error("auto-trigger should have written post_dream_frustration_baseline")
	} else {
		// frustration 0.8 → 0.8 + 0.3*(0.3-0.8) = 0.65
		want := 0.8 + 0.3*(0.3-0.8)
		got := *ystRow.PostDreamFrustrationBaseline
		if !approxEqual(got, want, eps) {
			t.Errorf("post_dream_frustration_baseline: got %.6f, want %.6f", got, want)
		}
	}

	// Also verify today's row was seeded from post_dream_frustration_baseline.
	todayRow, err2 := db.State.Today()
	if err2 != nil {
		t.Fatalf("Today (second): %v", err2)
	}
	// frustration_baseline in today's row should reflect the post-dream value.
	wantFrust := 0.8 + 0.3*(0.3-0.8)
	if !approxEqual(todayRow.FrustrationBaseline, wantFrust, eps) {
		t.Errorf("today's frustration_baseline (seeded from post_dream): got %.6f, want %.6f",
			todayRow.FrustrationBaseline, wantFrust)
	}
}
