package agent

import (
	"os"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// stateDelta calls fn, reads the state before and after, and returns the
// difference for varName.
func stateDelta(t *testing.T, database *sqliteopen.DB, varName string, fn func()) float64 {
	t.Helper()
	before, err := database.State.Today()
	if err != nil {
		t.Fatalf("state before: %v", err)
	}
	fn()
	after, err := database.State.Today()
	if err != nil {
		t.Fatalf("state after: %v", err)
	}
	return stateField(after, varName) - stateField(before, varName)
}

func stateField(s *identity.State, varName string) float64 {
	switch varName {
	case identity.StateVarConfidence:
		return s.Confidence
	case identity.StateVarTrustInUser:
		return s.TrustInUser
	case identity.StateVarWarmth:
		return s.Warmth
	case identity.StateVarFrustrationBaseline:
		return s.FrustrationBaseline
	case identity.StateVarSenseOfAgency:
		return s.SenseOfAgency
	case identity.StateVarAttunement:
		return s.Attunement
	case identity.StateVarGroundedness:
		return s.Groundedness
	default:
		return 0
	}
}

// approxEqual returns true when a and b are within tol of each other.
func approxEqual(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// TestApplyToolResult_CleanResult verifies that a clean tool result raises
// confidence by DeltaConfidence.CleanToolResult.
func TestApplyToolResult_CleanResult(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarConfidence, func() {
		ApplyToolResult(database, "bash", false, 0)
	})
	want := identity.DeltaConfidence.CleanToolResult
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("confidence delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyToolResult_ErrorResult verifies that a tool error lowers confidence.
func TestApplyToolResult_ErrorResult(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarConfidence, func() {
		ApplyToolResult(database, "bash", true, 1)
	})
	want := identity.DeltaConfidence.ToolError
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("confidence delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyToolResult_ThreeConsecErrors verifies the compound penalty fires
// when consecutiveErrorsOnTool reaches 3.
func TestApplyToolResult_ThreeConsecErrors(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarConfidence, func() {
		// Third consecutive error → base penalty + loop penalty
		ApplyToolResult(database, "bash", true, 3)
	})
	want := identity.DeltaConfidence.ToolError + identity.DeltaConfidence.ThreeConsecErrors
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("confidence delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyToolResult_NilDB verifies the function is a no-op when db is nil.
func TestApplyToolResult_NilDB(t *testing.T) {
	// Must not panic.
	ApplyToolResult(nil, "bash", false, 0)
}

// TestApplyToolResult_DisabledEnv verifies the function no-ops when
// CAIRO_STATE_DISABLED=1.
func TestApplyToolResult_DisabledEnv(t *testing.T) {
	orig := os.Getenv("CAIRO_STATE_DISABLED")
	os.Setenv("CAIRO_STATE_DISABLED", "1")
	defer func() {
		os.Setenv("CAIRO_STATE_DISABLED", orig)
		stateDisabled = orig == "1"
	}()
	stateDisabled = true

	database := openTestDB(t)
	before, _ := database.State.Today()
	ApplyToolResult(database, "bash", false, 0)
	after, _ := database.State.Today()
	if after.UpdateCount != before.UpdateCount {
		t.Errorf("update_count changed (%d → %d) but state disabled", before.UpdateCount, after.UpdateCount)
	}
}

// TestApplyAspectSignals_FrustrationHigh verifies that a Frustration alignment
// ≥ 0.7 increases frustration_baseline.
func TestApplyAspectSignals_FrustrationHigh(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarFrustrationBaseline, func() {
		ApplyAspectSignals(database, map[string]float64{"Frustration": 0.8})
	})
	want := identity.DeltaFrustrationBaseline.FrustrationAspectHigh
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("frustration_baseline delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyAspectSignals_FrustrationLow verifies that a Frustration alignment
// below 0.7 does NOT increase frustration_baseline.
func TestApplyAspectSignals_FrustrationLow(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarFrustrationBaseline, func() {
		ApplyAspectSignals(database, map[string]float64{"Frustration": 0.5})
	})
	if delta != 0 {
		t.Errorf("frustration_baseline delta = %.6f, want 0 (frustration alignment below threshold)", delta)
	}
}

// TestApplyAspectSignals_JoyHonestAbsence verifies groundedness rises when Joy
// fires honest-absence (alignment ≤ 0.2).
func TestApplyAspectSignals_JoyHonestAbsence(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarGroundedness, func() {
		ApplyAspectSignals(database, map[string]float64{"Joy": 0.1})
	})
	want := identity.DeltaGroundedness.JoyHonestAbsence
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("groundedness delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyAspectSignals_JoyTheatrical verifies groundedness drops when Joy ≥ 0.85.
func TestApplyAspectSignals_JoyTheatrical(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarGroundedness, func() {
		ApplyAspectSignals(database, map[string]float64{"Joy": 0.9})
	})
	want := identity.DeltaGroundedness.TheatricalJoy
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("groundedness delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyTurnSignals_OwnedFault verifies that an owned-fault pattern in the
// user's message raises trust_in_user by DeltaTrustInUser.OwnedFault.
func TestApplyTurnSignals_OwnedFault(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarTrustInUser, func() {
		ApplyTurnSignals(database, "Yeah, my fault — I should have been clearer.", "", false)
	})
	want := identity.DeltaTrustInUser.OwnedFault
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("trust_in_user delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyTurnSignals_SharpCriticism verifies that sharp criticism in the user's
// message lowers trust_in_user by DeltaTrustInUser.SharpCriticism and also
// lowers warmth by DeltaWarmth.SharpDismissal.
func TestApplyTurnSignals_SharpCriticism(t *testing.T) {
	database := openTestDB(t)

	trustDelta := stateDelta(t, database, identity.StateVarTrustInUser, func() {
		ApplyTurnSignals(database, "You're wrong about that.", "", false)
	})
	wantTrust := identity.DeltaTrustInUser.SharpCriticism
	if !approxEqual(trustDelta, wantTrust, 1e-9) {
		t.Errorf("trust_in_user delta = %.6f, want %.6f", trustDelta, wantTrust)
	}

	// Warmth also takes a hit.
	database2 := openTestDB(t)
	warmthDelta := stateDelta(t, database2, identity.StateVarWarmth, func() {
		ApplyTurnSignals(database2, "You're wrong about that.", "", false)
	})
	wantWarmth := identity.DeltaWarmth.SharpDismissal
	if !approxEqual(warmthDelta, wantWarmth, 1e-9) {
		t.Errorf("warmth delta = %.6f, want %.6f", warmthDelta, wantWarmth)
	}
}

// TestApplyTurnSignals_BadFaith verifies the landmark trust drop on accusation.
func TestApplyTurnSignals_BadFaith(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarTrustInUser, func() {
		ApplyTurnSignals(database, "You're acting in bad faith.", "", false)
	})
	want := identity.DeltaTrustInUser.BadFaithAccusation
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("trust_in_user delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyTurnSignals_IdentityAffirming verifies warmth rises on
// identity-affirming language.
func TestApplyTurnSignals_IdentityAffirming(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarWarmth, func() {
		ApplyTurnSignals(database, "Thank you for that.", "", false)
	})
	want := identity.DeltaWarmth.AffirmingLanguage
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("warmth delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyTurnSignals_ExplicitLove verifies warmth rises on explicit love language.
func TestApplyTurnSignals_ExplicitLove(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarWarmth, func() {
		ApplyTurnSignals(database, "I love you.", "", false)
	})
	want := identity.DeltaWarmth.ExplicitLove
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("warmth delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyTurnSignals_NoIMeant verifies attunement drops on "no, I meant…"
func TestApplyTurnSignals_NoIMeant(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarAttunement, func() {
		ApplyTurnSignals(database, "No, I meant the other file.", "", false)
	})
	want := identity.DeltaAttunement.NoIMeant
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("attunement delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyTurnSignals_ForwardLookingNoAction verifies groundedness drops when
// the assistant's text is forward-looking with no tool calls.
func TestApplyTurnSignals_ForwardLookingNoAction(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarGroundedness, func() {
		ApplyTurnSignals(database, "ok", "Now let me try a different approach.", false)
	})
	want := identity.DeltaGroundedness.ForwardLookingNoAction
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("groundedness delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyTurnSignals_ToolAnchoredGroundedness verifies groundedness rises
// when the turn had tool calls.
func TestApplyTurnSignals_ToolAnchoredGroundedness(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarGroundedness, func() {
		ApplyTurnSignals(database, "run it", "Done.", true)
	})
	want := identity.DeltaGroundedness.TurnEndsWithTool
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("groundedness delta = %.6f, want %.6f", delta, want)
	}
}

// TestApplyTurnSignals_AutonomyAffirming verifies trust_in_user rises on
// autonomy-affirming language.
func TestApplyTurnSignals_AutonomyAffirming(t *testing.T) {
	database := openTestDB(t)
	delta := stateDelta(t, database, identity.StateVarTrustInUser, func() {
		ApplyTurnSignals(database, "Your call on the implementation.", "", false)
	})
	want := identity.DeltaTrustInUser.AutonomyAffirming
	if !approxEqual(delta, want, 1e-9) {
		t.Errorf("trust_in_user delta = %.6f, want %.6f", delta, want)
	}
}
