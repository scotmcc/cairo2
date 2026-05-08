package db

import (
	"testing"
	"time"
)

// TestStateToday_LazyRollover verifies that with no prior row, Today() creates
// today's row at schema defaults.
func TestStateToday_LazyRollover(t *testing.T) {
	database := openTest(t)

	s, err := database.State.Today()
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if s == nil {
		t.Fatal("Today: got nil state, want row")
	}
	today := time.Now().Format("2006-01-02")
	if s.Date != today {
		t.Errorf("Date: want %q, got %q", today, s.Date)
	}
	// Schema defaults per plan §2.
	checkFloat(t, "confidence", s.Confidence, 0.5)
	checkFloat(t, "trust_in_user", s.TrustInUser, 0.4)
	checkFloat(t, "warmth", s.Warmth, 0.4)
	checkFloat(t, "frustration_baseline", s.FrustrationBaseline, 0.4)
	checkFloat(t, "sense_of_agency", s.SenseOfAgency, 0.5)
	checkFloat(t, "attunement", s.Attunement, 0.4)
	checkFloat(t, "groundedness", s.Groundedness, 0.5)
}

// TestStateToday_ForwardCopyFromPostDream verifies that when a prior row has
// post_dream_* values set, Today() uses those as starting live values rather
// than the prior row's live values. The seed inserts today's row at schema
// defaults; we delete it, insert a synthetic "yesterday" with distinct values,
// then call ensureTodayRow to trigger the forward-copy path.
func TestStateToday_ForwardCopyFromPostDream(t *testing.T) {
	database := openTest(t)

	today := time.Now().Format("2006-01-02")
	// Remove today's row seeded at startup so we can exercise the rollover path.
	if _, err := database.sql.Exec(`DELETE FROM state_daily WHERE date = ?`, today); err != nil {
		t.Fatalf("delete today: %v", err)
	}

	// Insert a prior row with live values (0.3) and distinct post_dream_* (0.7).
	// Mark dream_processed_at as already done so the Phase 4 auto-trigger does
	// not overwrite the post_dream_* values we're explicitly testing here.
	yesterday := "2000-01-01"
	now := time.Now().Unix()
	_, err := database.sql.Exec(
		`INSERT INTO state_daily(
		     date, confidence, trust_in_user, warmth, frustration_baseline,
		     sense_of_agency, attunement, groundedness,
		     post_dream_confidence, post_dream_trust_in_user, post_dream_warmth,
		     post_dream_frustration_baseline, post_dream_sense_of_agency,
		     post_dream_attunement, post_dream_groundedness,
		     updated_at, dream_processed_at)
		 VALUES (?, 0.3, 0.3, 0.3, 0.3, 0.3, 0.3, 0.3,
		         0.7, 0.7, 0.7, 0.7, 0.7, 0.7, 0.7,
		         ?, ?)`,
		yesterday, now, now)
	if err != nil {
		t.Fatalf("insert prior row: %v", err)
	}

	// ensureTodayRow should forward-copy post_dream_* values.
	if err := database.State.ensureTodayRow(today); err != nil {
		t.Fatalf("ensureTodayRow: %v", err)
	}

	s, err := database.State.Today()
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if s == nil {
		t.Fatal("Today: got nil state, want row")
	}
	// All live values should be the post_dream_* values from yesterday (0.7).
	checkFloat(t, "confidence (from post_dream)", s.Confidence, 0.7)
	checkFloat(t, "trust_in_user (from post_dream)", s.TrustInUser, 0.7)
	checkFloat(t, "warmth (from post_dream)", s.Warmth, 0.7)
	checkFloat(t, "frustration_baseline (from post_dream)", s.FrustrationBaseline, 0.7)
	checkFloat(t, "sense_of_agency (from post_dream)", s.SenseOfAgency, 0.7)
	checkFloat(t, "attunement (from post_dream)", s.Attunement, 0.7)
	checkFloat(t, "groundedness (from post_dream)", s.Groundedness, 0.7)
}

// TestStateToday_ForwardCopyFallbackToLive verifies that when a prior row has
// NULL post_dream_* values, Today() uses the live values as starting point.
func TestStateToday_ForwardCopyFallbackToLive(t *testing.T) {
	database := openTest(t)

	today := time.Now().Format("2006-01-02")
	// Remove today's seeded row to exercise the rollover path.
	if _, err := database.sql.Exec(`DELETE FROM state_daily WHERE date = ?`, today); err != nil {
		t.Fatalf("delete today: %v", err)
	}

	// Insert a prior row with live values (0.6) and no post_dream_* (NULL).
	// Mark dream_processed_at as already done so the Phase 4 auto-trigger does
	// not write post_dream_* before forward-copy — this test verifies the live-
	// value fallback path, which only fires when post_dream_* is NULL.
	yesterday := "2000-01-01"
	now := time.Now().Unix()
	_, err := database.sql.Exec(
		`INSERT INTO state_daily(date, confidence, trust_in_user, warmth,
		     frustration_baseline, sense_of_agency, attunement, groundedness,
		     updated_at, dream_processed_at)
		 VALUES (?, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, 0.6, ?, ?)`,
		yesterday, now, now)
	if err != nil {
		t.Fatalf("insert prior row: %v", err)
	}

	if err := database.State.ensureTodayRow(today); err != nil {
		t.Fatalf("ensureTodayRow: %v", err)
	}

	s, err := database.State.Today()
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if s == nil {
		t.Fatal("Today: got nil state, want row")
	}
	// All live values should be copied from prior row's live values (0.6).
	checkFloat(t, "confidence (live fallback)", s.Confidence, 0.6)
	checkFloat(t, "trust_in_user (live fallback)", s.TrustInUser, 0.6)
	checkFloat(t, "warmth (live fallback)", s.Warmth, 0.6)
	checkFloat(t, "frustration_baseline (live fallback)", s.FrustrationBaseline, 0.6)
	checkFloat(t, "sense_of_agency (live fallback)", s.SenseOfAgency, 0.6)
	checkFloat(t, "attunement (live fallback)", s.Attunement, 0.6)
	checkFloat(t, "groundedness (live fallback)", s.Groundedness, 0.6)
}

// TestStateApply_ClampHigh verifies that Apply clamps to 1.0 when delta
// would push the value above the ceiling.
func TestStateApply_ClampHigh(t *testing.T) {
	database := openTest(t)

	if _, err := database.State.Today(); err != nil {
		t.Fatalf("Today: %v", err)
	}
	if err := database.State.Apply(StateVarConfidence, 5.0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	s, err := database.State.Today()
	if err != nil {
		t.Fatalf("Today after Apply: %v", err)
	}
	checkFloat(t, "confidence clamped high", s.Confidence, 1.0)
}

// TestStateApply_ClampLow verifies that Apply clamps to 0.0 when delta
// would push the value below the floor.
func TestStateApply_ClampLow(t *testing.T) {
	database := openTest(t)

	if _, err := database.State.Today(); err != nil {
		t.Fatalf("Today: %v", err)
	}
	if err := database.State.Apply(StateVarConfidence, -5.0); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	s, err := database.State.Today()
	if err != nil {
		t.Fatalf("Today after Apply: %v", err)
	}
	checkFloat(t, "confidence clamped low", s.Confidence, 0.0)
}

// TestStateApply_UpdateCount verifies that update_count increments on each Apply.
func TestStateApply_UpdateCount(t *testing.T) {
	database := openTest(t)

	if _, err := database.State.Today(); err != nil {
		t.Fatalf("Today: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := database.State.Apply(StateVarWarmth, 0.01); err != nil {
			t.Fatalf("Apply[%d]: %v", i, err)
		}
	}
	s, err := database.State.Today()
	if err != nil {
		t.Fatalf("Today after Apply: %v", err)
	}
	if s.UpdateCount != 3 {
		t.Errorf("update_count: want 3, got %d", s.UpdateCount)
	}
}

// TestStateApply_UnknownVar verifies that Apply returns an error for unknown
// variable names.
func TestStateApply_UnknownVar(t *testing.T) {
	database := openTest(t)

	if _, err := database.State.Today(); err != nil {
		t.Fatalf("Today: %v", err)
	}
	err := database.State.Apply("nonexistent_var", 0.1)
	if err == nil {
		t.Error("Apply with unknown var: want error, got nil")
	}
}

// TestStateWritePostDream verifies that WritePostDream writes all seven
// post_dream_* columns and stamps dream_processed_at.
func TestStateWritePostDream(t *testing.T) {
	database := openTest(t)

	s, err := database.State.Today()
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if s == nil {
		t.Fatal("Today: got nil")
	}

	v := PostDreamValues{
		Confidence:          0.55,
		TrustInUser:         0.45,
		Warmth:              0.65,
		FrustrationBaseline: 0.30,
		SenseOfAgency:       0.50,
		Attunement:          0.48,
		Groundedness:        0.52,
	}
	if err := database.State.WritePostDream(s.Date, v); err != nil {
		t.Fatalf("WritePostDream: %v", err)
	}

	updated, err := database.State.Today()
	if err != nil {
		t.Fatalf("Today after WritePostDream: %v", err)
	}
	if updated.DreamProcessedAt == nil {
		t.Error("dream_processed_at: want non-nil after WritePostDream, got nil")
	}
	checkFloatPtr(t, "post_dream_confidence", updated.PostDreamConfidence, 0.55)
	checkFloatPtr(t, "post_dream_trust_in_user", updated.PostDreamTrustInUser, 0.45)
	checkFloatPtr(t, "post_dream_warmth", updated.PostDreamWarmth, 0.65)
	checkFloatPtr(t, "post_dream_frustration_baseline", updated.PostDreamFrustrationBaseline, 0.30)
	checkFloatPtr(t, "post_dream_sense_of_agency", updated.PostDreamSenseOfAgency, 0.50)
	checkFloatPtr(t, "post_dream_attunement", updated.PostDreamAttunement, 0.48)
	checkFloatPtr(t, "post_dream_groundedness", updated.PostDreamGroundedness, 0.52)
}

// --- helpers ---

const floatEps = 1e-9

func checkFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if diff := got - want; diff < -floatEps || diff > floatEps {
		t.Errorf("%s: want %v, got %v", name, want, got)
	}
}

func checkFloatPtr(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: want %v, got nil", name, want)
		return
	}
	checkFloat(t, name, *got, want)
}
