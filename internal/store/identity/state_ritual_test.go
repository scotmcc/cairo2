package identity_test

import (
	"math"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// approxEqual returns true when a and b differ by less than epsilon.
func approxEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

const eps = 1e-9

// makeTestDB opens a fresh tempdir DB suitable for ritual tests.
func makeTestDB(t *testing.T) *sqliteopen.DB {
	t.Helper()
	return testdb.OpenTestDB(t)
}

// insertStateRow writes a state_daily row directly for the given date.
func insertStateRow(t *testing.T, db *sqliteopen.DB, date string, s identity.State) {
	t.Helper()
	now := time.Now().Unix()
	_, err := db.SQL().Exec(
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

// yesterday returns the date string for the previous calendar day.
func yesterday() string {
	return time.Now().AddDate(0, 0, -1).Format("2006-01-02")
}

// daysAgo returns the date string for n days before today.
func daysAgo(n int) string {
	return time.Now().AddDate(0, 0, -n).Format("2006-01-02")
}

// clamp01 clamps v to [0,1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ───────────────────────────────────────────────────────────────────────────
// Regression-to-neutral tests
// ───────────────────────────────────────────────────────────────────────────

func Test_Ritual_WentToBedAngry(t *testing.T) {
	db := makeTestDB(t)

	row := identity.State{FrustrationBaseline: 0.7}
	row.Confidence = 0.5
	row.TrustInUser = 0.4
	row.Warmth = 0.4
	row.SenseOfAgency = 0.5
	row.Attunement = 0.4
	row.Groundedness = 0.5
	insertStateRow(t, db, yesterday(), row)

	report, err := identity.RunDreamRitual(db.State)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}
	if report.Skipped {
		t.Fatalf("expected ritual to run, got skip: %s", report.SkipReason)
	}

	d := report.Drifts[identity.StateVarFrustrationBaseline]
	want := 0.58
	if !approxEqual(d.PostDreamValue, want, eps) {
		t.Errorf("frustration_baseline post-dream: got %.6f, want %.6f", d.PostDreamValue, want)
	}
	if d.Reason != "regression-to-neutral" {
		t.Errorf("reason: got %q, want %q", d.Reason, "regression-to-neutral")
	}
}

func Test_Ritual_TrustClimbedThreeDaysRunning(t *testing.T) {
	db := makeTestDB(t)

	trustVals := []float64{0.40, 0.42, 0.44, 0.46}
	for i, tv := range trustVals {
		row := identity.State{
			Confidence: 0.5, TrustInUser: tv, Warmth: 0.4,
			FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
			Attunement: 0.4, Groundedness: 0.5,
		}
		insertStateRow(t, db, daysAgo(3-i+1), row)
	}

	report, err := identity.RunDreamRitual(db.State)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}
	if report.Skipped {
		t.Fatalf("skipped: %s", report.SkipReason)
	}

	d := report.Drifts[identity.StateVarTrustInUser]
	if d.Reason != "momentum-amplified" {
		t.Errorf("reason: got %q, want %q", d.Reason, "momentum-amplified")
	}
	want := clamp01(0.46 + 0.5*0.02)
	if !approxEqual(d.PostDreamValue, want, eps) {
		t.Errorf("trust post-dream: got %.6f, want %.6f", d.PostDreamValue, want)
	}
}

func Test_Ritual_TrustCrackedSharply(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, daysAgo(2), identity.State{
		Confidence: 0.5, TrustInUser: 0.50, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})
	insertStateRow(t, db, yesterday(), identity.State{
		Confidence: 0.5, TrustInUser: 0.44, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	report, err := identity.RunDreamRitual(db.State)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}
	if report.Skipped {
		t.Fatalf("skipped: %s", report.SkipReason)
	}

	d := report.Drifts[identity.StateVarTrustInUser]
	if !approxEqual(d.PostDreamValue, d.LiveValue, eps) {
		t.Errorf("trust should be unchanged (wound left open): got %.6f, live %.6f",
			d.PostDreamValue, d.LiveValue)
	}
}

func Test_Ritual_LowConfidenceTrap(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, yesterday(), identity.State{
		Confidence: 0.2, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	report, err := identity.RunDreamRitual(db.State)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}

	d := report.Drifts[identity.StateVarConfidence]
	want := 0.23
	if !approxEqual(d.PostDreamValue, want, eps) {
		t.Errorf("confidence post-dream: got %.6f, want %.6f", d.PostDreamValue, want)
	}
	if d.Reason != "low-conf-trap" {
		t.Errorf("reason: got %q, want %q", d.Reason, "low-conf-trap")
	}
}

func Test_Ritual_HighConfidenceHumilityCheck(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, yesterday(), identity.State{
		Confidence: 0.85, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	report, err := identity.RunDreamRitual(db.State)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}

	d := report.Drifts[identity.StateVarConfidence]
	want := 0.83
	if !approxEqual(d.PostDreamValue, want, eps) {
		t.Errorf("confidence post-dream: got %.6f, want %.6f", d.PostDreamValue, want)
	}
	if d.Reason != "regression-to-neutral" {
		t.Errorf("reason: got %q, want %q", d.Reason, "regression-to-neutral")
	}
}

func Test_Ritual_MidRangeNoChange(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, yesterday(), identity.State{
		Confidence: 0.55, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.4, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	report, err := identity.RunDreamRitual(db.State)
	if err != nil {
		t.Fatalf("RunDreamRitual: %v", err)
	}

	d := report.Drifts[identity.StateVarConfidence]
	if !approxEqual(d.PostDreamValue, d.LiveValue, eps) {
		t.Errorf("confidence should be unchanged: got %.6f, live %.6f",
			d.PostDreamValue, d.LiveValue)
	}
	if d.Reason != "no-change" {
		t.Errorf("reason: got %q, want %q", d.Reason, "no-change")
	}
}

func Test_Ritual_SkippedIfAlreadyProcessed(t *testing.T) {
	db := makeTestDB(t)

	insertStateRow(t, db, yesterday(), identity.State{
		Confidence: 0.5, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.7, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	r1, err := identity.RunDreamRitual(db.State)
	if err != nil {
		t.Fatalf("first RunDreamRitual: %v", err)
	}
	if r1.Skipped {
		t.Fatalf("first call should not skip: %s", r1.SkipReason)
	}

	r2, err := identity.RunDreamRitual(db.State)
	if err != nil {
		t.Fatalf("second RunDreamRitual: %v", err)
	}
	if !r2.Skipped {
		t.Error("second call should be skipped (already processed)")
	}
}

func Test_Ritual_AutoTriggerViaEnsureTodayRow(t *testing.T) {
	db := makeTestDB(t)

	today := time.Now().Format("2006-01-02")

	_, err := db.SQL().Exec(`DELETE FROM state_daily WHERE date = ?`, today)
	if err != nil {
		t.Fatalf("delete today seed row: %v", err)
	}

	insertStateRow(t, db, yesterday(), identity.State{
		Confidence: 0.5, TrustInUser: 0.4, Warmth: 0.4,
		FrustrationBaseline: 0.8, SenseOfAgency: 0.5,
		Attunement: 0.4, Groundedness: 0.5,
	})

	_, err = db.State.Today()
	if err != nil {
		t.Fatalf("Today: %v", err)
	}

	yRow, err := db.State.LastN(2)
	if err != nil {
		t.Fatalf("LastN: %v", err)
	}
	var ystRow *identity.State
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
		want := 0.8 + 0.3*(0.3-0.8)
		got := *ystRow.PostDreamFrustrationBaseline
		if !approxEqual(got, want, eps) {
			t.Errorf("post_dream_frustration_baseline: got %.6f, want %.6f", got, want)
		}
	}

	todayRow, err2 := db.State.Today()
	if err2 != nil {
		t.Fatalf("Today (second): %v", err2)
	}
	wantFrust := 0.8 + 0.3*(0.3-0.8)
	if !approxEqual(todayRow.FrustrationBaseline, wantFrust, eps) {
		t.Errorf("today's frustration_baseline (seeded from post_dream): got %.6f, want %.6f",
			todayRow.FrustrationBaseline, wantFrust)
	}
}
