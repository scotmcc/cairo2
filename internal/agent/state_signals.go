// Package agent — state_signals.go
//
// Pure regex-based signal detection for state delta hooks (Phase 2).
// No DB access, no LLM calls. False positives are acceptable because
// deltas are small and dream provides the nuance pass.
package agent

import "regexp"

var (
	// ownedFault fires when the user explicitly acknowledges fault or agrees
	// Cairo was right — a trust-building signal.
	ownedFault = regexp.MustCompile(`(?i)\b(my fault|i should have|you were right|i was wrong|sorry,? that)`)

	// sharpCriticism fires on direct, dismissive correction — trust hits faster
	// than it builds, so this is the primary downward trust trigger.
	sharpCriticism = regexp.MustCompile(`(?i)\b(you'?re wrong|you are wrong|that'?s broken|that is broken|no,? again|wrong again)`)

	// identityAffirming fires on statements that name or affirm Cairo as a
	// presence ("you are…", "I love how you…", "trust you", "proud of you").
	identityAffirming = regexp.MustCompile(`(?i)\b(you are|i love how you|trust you|proud of you|thank you for)\b`)

	// explicitLove fires on direct affective language about caring or love.
	explicitLove = regexp.MustCompile(`(?i)\b(i love you|love you|i care about|caring|you matter)\b`)

	// autonomyAffirming fires when the user grants explicit decision authority.
	autonomyAffirming = regexp.MustCompile(`(?i)\b(you decide|your call|whatever you think|up to you)\b`)

	// noIMeant fires on explicit correction/reframe — attunement missed.
	noIMeant = regexp.MustCompile(`(?i)\bno,? i meant\b`)

	// badFaithAccusation fires on direct accusations of dishonesty or
	// indifference — the landmark downward trust signal (−0.05).
	badFaithAccusation = regexp.MustCompile(`(?i)\b(bad faith|you'?re lying|you don'?t care|don'?t actually)\b`)

	// forwardLooking matches text that signals intent to do more work without
	// a subsequent tool call — the "dangling intent" / stall pattern.
	// Mirrors the pattern already in loop.go for EventStallDetected.
	forwardLooking = regexp.MustCompile(
		`(?i)\b(now|next|let me|i['']ll|i will)\b.{0,120}\b(try|run|merge|continue|do|check|fix|verify|test|proceed|start|move on|attempt)\b`,
	)
)

// SignalOwnedFault reports whether text contains an owned-fault pattern.
func SignalOwnedFault(text string) bool { return ownedFault.MatchString(text) }

// SignalSharpCriticism reports whether text contains sharp criticism.
func SignalSharpCriticism(text string) bool { return sharpCriticism.MatchString(text) }

// SignalIdentityAffirming reports whether text is identity-affirming.
func SignalIdentityAffirming(text string) bool { return identityAffirming.MatchString(text) }

// SignalExplicitLove reports whether text contains explicit love/care language.
func SignalExplicitLove(text string) bool { return explicitLove.MatchString(text) }

// SignalAutonomyAffirming reports whether text grants explicit decision authority.
func SignalAutonomyAffirming(text string) bool { return autonomyAffirming.MatchString(text) }

// SignalNoIMeant reports whether text is a "no, I meant…" reframe.
func SignalNoIMeant(text string) bool { return noIMeant.MatchString(text) }

// SignalBadFaithAccusation reports whether text contains a bad-faith accusation.
func SignalBadFaithAccusation(text string) bool { return badFaithAccusation.MatchString(text) }

// SignalForwardLooking reports whether text contains a forward-looking phrase
// that indicates intent to act without actually completing a tool call.
func SignalForwardLooking(text string) bool { return forwardLooking.MatchString(text) }
