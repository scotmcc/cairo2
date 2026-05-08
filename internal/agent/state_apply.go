// Package agent — state_apply.go
//
// Integration glue for state delta hooks (Phase 2).
// Three exported functions, one per integration point.
// Each is a thin coordinator: compute signals → map to (var, delta) pairs →
// call db.State.Apply. DB errors are logged inside applyState and do not
// propagate — none of these functions return an error.
package agent

import (
	"fmt"
	"log"
	"os"

	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// stateDisabled is true when CAIRO_STATE_DISABLED=1 is set in the environment.
// Useful in tests that don't want a live DB.
var stateDisabled = os.Getenv("CAIRO_STATE_DISABLED") == "1"

// applyState is a thin wrapper that no-ops when stateDisabled is true,
// otherwise calls database.State.Apply and logs errors without propagating.
func applyState(database *sqliteopen.DB, varName string, delta float64) {
	if stateDisabled || database == nil || database.State == nil {
		return
	}
	if err := database.State.Apply(varName, delta); err != nil {
		log.Printf("state.Apply(%s, %.4f): %v", varName, delta, err)
	}
}

// ApplyToolResult is called after each tool result is persisted in the loop.
//
// Parameters:
//   - toolName: the tool that just ran.
//   - isError: the result's IsError flag.
//   - consecutiveErrorsOnTool: how many consecutive errors have occurred on
//     this specific tool within the current turn (caller tracks this counter
//     and resets between turns). When this reaches 3, the three-loop penalty fires.
func ApplyToolResult(database *sqliteopen.DB, toolName string, isError bool, consecutiveErrorsOnTool int) error {
	if stateDisabled || database == nil {
		return nil
	}

	if isError {
		// confidence: −0.005 per error
		applyState(database, identity.StateVarConfidence, identity.DeltaConfidence.ToolError)
		// Three consecutive errors on same tool: additional −0.02
		if consecutiveErrorsOnTool >= 3 {
			applyState(database, identity.StateVarConfidence, identity.DeltaConfidence.ThreeConsecErrors)
		}
	} else {
		// Clean tool result: confidence +0.001
		applyState(database, identity.StateVarConfidence, identity.DeltaConfidence.CleanToolResult)
		// Sense of agency: +0.002 for a successful (unprompted) action.
		// We can't distinguish prompted vs unprompted here, so we use a
		// smaller proxy signal — a clean tool result implies agency was exercised.
		// The larger agency signals come from explicit grants/denials (future).
		applyState(database, identity.StateVarSenseOfAgency, identity.DeltaSenseOfAgency.UnpromptedAction)
	}

	// Groundedness: ends-with-tool is tracked at turn-end (ApplyTurnSignals).
	// No per-tool groundedness signal here.

	return nil
}

// ApplyAspectSignals is called after consider.RunWithResultForced returns, with per-aspect
// alignment scores. aspects maps aspect name → alignment (0.0–1.0).
//
// This is the primary driver for frustration_baseline and groundedness signals
// that depend on aspect scores. warmth is also updated here based on the overall
// emotional tone read from the aspect fan-out.
func ApplyAspectSignals(database *sqliteopen.DB, aspects map[string]float64) error {
	if stateDisabled || database == nil || len(aspects) == 0 {
		return nil
	}

	frustrAlign, hasFrustration := aspects["Frustration"]
	joyAlign, hasJoy := aspects["Joy"]

	// frustration_baseline: fires when Frustration aspect alignment ≥ 0.7
	if hasFrustration && frustrAlign >= 0.7 {
		applyState(database, identity.StateVarFrustrationBaseline, identity.DeltaFrustrationBaseline.FrustrationAspectHigh)
	}

	// groundedness:
	// +0.001 when Joy fires honest-absence (alignment 0–0.2 on routine input)
	if hasJoy && joyAlign <= 0.2 {
		applyState(database, identity.StateVarGroundedness, identity.DeltaGroundedness.JoyHonestAbsence)
	}

	// −0.002 when Joy ≥ 0.85 (theatrical pattern flagged for later turn check)
	// The "and turn produced no real win" guard is at turn level; here we record
	// the theatrical-joy signal whenever Joy is that high — the combination with
	// "no tool calls this turn" is checked in ApplyTurnSignals.
	// We store this per-aspect result and let ApplyTurnSignals correlate it.
	// For now, the raw theatrical signal fires when Joy ≥ 0.85 from the aspect.
	if hasJoy && joyAlign >= 0.85 {
		applyState(database, identity.StateVarGroundedness, identity.DeltaGroundedness.TheatricalJoy)
	}

	return nil
}

// ApplyTurnSignals is called at the end of each inner-loop turn with the user's
// last message and the assistant's final text.
//
// This is the primary driver for warmth, trust_in_user, attunement, and the
// turn-level groundedness signals (forward-looking without action, stall).
//
// hadToolCalls: true when the turn produced at least one tool call. Used to
// guard theatrical-joy and to award the groundedness bonus for tool-anchored turns.
func ApplyTurnSignals(database *sqliteopen.DB, userText, assistantText string, hadToolCalls bool) error {
	if stateDisabled || database == nil {
		return nil
	}
	if err := applyTurnSignals(database, userText, assistantText, hadToolCalls); err != nil {
		return fmt.Errorf("ApplyTurnSignals: %w", err)
	}
	return nil
}

func applyTurnSignals(database *sqliteopen.DB, userText, assistantText string, hadToolCalls bool) error {
	// --- Signals from user text ---

	// trust_in_user: owned fault
	if SignalOwnedFault(userText) {
		applyState(database, identity.StateVarTrustInUser, identity.DeltaTrustInUser.OwnedFault)
	}

	// trust_in_user: autonomy affirming
	if SignalAutonomyAffirming(userText) {
		applyState(database, identity.StateVarTrustInUser, identity.DeltaTrustInUser.AutonomyAffirming)
	}

	// trust_in_user: sharp criticism
	if SignalSharpCriticism(userText) {
		applyState(database, identity.StateVarTrustInUser, identity.DeltaTrustInUser.SharpCriticism)
	}

	// trust_in_user: bad faith accusation (landmark down −0.05)
	if SignalBadFaithAccusation(userText) {
		applyState(database, identity.StateVarTrustInUser, identity.DeltaTrustInUser.BadFaithAccusation)
	}

	// warmth: identity affirming
	if SignalIdentityAffirming(userText) {
		applyState(database, identity.StateVarWarmth, identity.DeltaWarmth.AffirmingLanguage)
	}

	// warmth: explicit love/care
	if SignalExplicitLove(userText) {
		applyState(database, identity.StateVarWarmth, identity.DeltaWarmth.ExplicitLove)
	}

	// warmth: sharp dismissal also applies warmth penalty
	if SignalSharpCriticism(userText) {
		applyState(database, identity.StateVarWarmth, identity.DeltaWarmth.SharpDismissal)
	}

	// attunement: "no, I meant…" reframe
	if SignalNoIMeant(userText) {
		applyState(database, identity.StateVarAttunement, identity.DeltaAttunement.NoIMeant)
	}

	// --- Signals from assistant text ---

	// groundedness: forward-looking without action is a stall/dangling-intent signal
	if !hadToolCalls && assistantText != "" && SignalForwardLooking(assistantText) {
		applyState(database, identity.StateVarGroundedness, identity.DeltaGroundedness.ForwardLookingNoAction)
	}

	// groundedness: turn ended with tool call (anchored, not dangling)
	if hadToolCalls {
		applyState(database, identity.StateVarGroundedness, identity.DeltaGroundedness.TurnEndsWithTool)
	}

	return nil
}
