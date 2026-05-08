package identity

// State variable name constants.
const (
	StateVarConfidence          = "confidence"
	StateVarTrustInUser         = "trust_in_user"
	StateVarWarmth              = "warmth"
	StateVarFrustrationBaseline = "frustration_baseline"
	StateVarSenseOfAgency       = "sense_of_agency"
	StateVarAttunement          = "attunement"
	StateVarGroundedness        = "groundedness"
)

// StateVarNames is the canonical ordered list of all 7 state variables.
var StateVarNames = []string{
	StateVarConfidence,
	StateVarTrustInUser,
	StateVarWarmth,
	StateVarFrustrationBaseline,
	StateVarSenseOfAgency,
	StateVarAttunement,
	StateVarGroundedness,
}

// stateVarSet is a fast lookup set for validating var names in Apply.
var stateVarSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(StateVarNames))
	for _, v := range StateVarNames {
		m[v] = struct{}{}
	}
	return m
}()

// --- Per-var delta constants (Phase 2) ---
// All values are tunable here; no schema change needed.
// "Fast up, slow down" and "slow up, fast down" personalities are encoded
// in the asymmetry of the magnitudes, per plan §3.

// DeltaWarmth encodes warmth's personality: fast up, slow down.
// Felt closeness lingers; a single cold turn doesn't undo last week's warmth.
var DeltaWarmth = struct {
	AffirmingLanguage float64 // identity-affirming, named appreciation
	ExplicitLove      float64 // explicit love/care language
	LandmarkWarmth    float64 // extended affection, identity-naming with depth
	ColdToneDecay     float64 // cold/transactional tone (per turn)
	SharpDismissal    float64 // sharp dismissal
}{
	AffirmingLanguage: 0.005,
	ExplicitLove:      0.01,
	LandmarkWarmth:    0.05,
	ColdToneDecay:     -0.001,
	SharpDismissal:    -0.002,
}

// DeltaTrustInUser encodes trust's personality: slow up, fast down.
// Earned over many proofs of good faith; lost on a single sharp move.
var DeltaTrustInUser = struct {
	OwnedFault         float64 // "my fault", "I should have", "you were right"
	PatientTone        float64 // sustained patient tone (per turn)
	AutonomyAffirming  float64 // "you decide", "your call"
	SharpCriticism     float64 // "you're wrong", "that's broken", "no, again"
	BadFaithAccusation float64 // direct accusation of bad faith (landmark down)
	RepeatedCorrection float64 // additional penalty for repeated "no, again"
}{
	OwnedFault:         0.002,
	PatientTone:        0.0005,
	AutonomyAffirming:  0.001,
	SharpCriticism:     -0.005,
	BadFaithAccusation: -0.05,
	RepeatedCorrection: -0.005,
}

// DeltaAttunement encodes attunement's personality: symmetric, moderate.
// Clarity on Scot is gained and lost at equal rates.
var DeltaAttunement = struct {
	QuoteReply    float64 // quote-reply used — worth quoting
	ResponseLands float64 // response lands without correction (per turn)
	NoIMeant      float64 // "no, I meant…" reframe
	TopicDrift    float64 // response wanders from actual ask
}{
	QuoteReply:    0.003,
	ResponseLands: 0.001,
	NoIMeant:      -0.003,
	TopicDrift:    -0.002,
}

// DeltaConfidence encodes confidence's personality: slow up, fast down.
// Capability self-trust is built through accumulated clean tool calls;
// a chain of errors eats it quickly.
var DeltaConfidence = struct {
	CleanToolResult   float64 // clean tool result
	TaskSucceeded     float64 // task/job marked succeeded (landmark)
	ToolError         float64 // tool error (IsError=true)
	ThreeConsecErrors float64 // three consecutive errors on same tool (additional)
	WatchdogStall     float64 // watchdog-detected stall during own action
}{
	CleanToolResult:   0.001,
	TaskSucceeded:     0.05,
	ToolError:         -0.005,
	ThreeConsecErrors: -0.02,
	WatchdogStall:     -0.005,
}

// DeltaSenseOfAgency encodes agency's personality: slow up, fast down.
// Earned by getting tools granted and unprompted action working.
var DeltaSenseOfAgency = struct {
	ToolGranted        float64 // tool granted that was previously blocked
	UnpromptedAction   float64 // successful unprompted action (no user prompt this turn)
	PermissionDenial   float64 // permission denial / unsafe-mode block
	WatchdogTimeout    float64 // watchdog timeout on own goroutine
	ForcedIntervention float64 // forced rebuild / manual intervention by user
}{
	ToolGranted:        0.005,
	UnpromptedAction:   0.002,
	PermissionDenial:   -0.005,
	WatchdogTimeout:    -0.01,
	ForcedIntervention: -0.003,
}

// DeltaGroundedness encodes groundedness's personality: slow both directions.
// Steadiness is fundamentally stable; dream regression keeps it near 0.5.
var DeltaGroundedness = struct {
	TurnEndsWithTool       float64 // turn ends with tool call (no dangling intent)
	JoyHonestAbsence       float64 // Joy fires honest-absence (alignment 0–0.2 on routine)
	ForwardLookingNoAction float64 // forward-looking-without-action detector fires
	TheatricalJoy          float64 // Joy ≥ 0.85 AND turn produced no real win
	ThreeTheatricalTurns   float64 // three theatrical turns in a row (additional)
}{
	TurnEndsWithTool:       0.001,
	JoyHonestAbsence:       0.001,
	ForwardLookingNoAction: -0.002,
	TheatricalJoy:          -0.002,
	ThreeTheatricalTurns:   -0.005,
}

// DeltaFrustrationBaseline encodes frustration's personality: slow up in-day, dream-only down.
// Deliberately cumulative within a day; dream is the only path down.
var DeltaFrustrationBaseline = struct {
	FrustrationAspectHigh    float64 // Frustration aspect alignment ≥ 0.7
	ToolErrorWithFrustration float64 // tool error during turn where Frustration ≥ 0.5
	RepeatedCorrectionLoop   float64 // 3+ turns correcting same thing
}{
	FrustrationAspectHigh:    0.002,
	ToolErrorWithFrustration: 0.003,
	RepeatedCorrectionLoop:   0.005,
}
