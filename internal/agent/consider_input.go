package agent

// ConsiderInput is the single canonical entry point for running a consider
// step against a user message — regardless of trigger (TUI auto-fire, CLI
// `/c ` prefix, API `/c ` prefix, or a model-invoked `consider_input` tool).
//
// Responsibilities:
//   1. Run consider.RunWithResultForced (which writes consider_activations rows
//      tagged with triggerSource and applies the per-role consider gate).
//   2. Build the inner-voice text (summary plus the named-aspect block).
//   3. UPDATE the user message row's inner_voice column when userMsgID > 0.
//   4. Link consider_activations rows to that message id.
//   5. Apply per-aspect signals to the state table.
//
// Lives in package `agent` (not in `agent/consider`) because it depends on
// formatNamedAspects and ApplyAspectSignals — both of which live here. Putting
// it inside the consider subpackage would create an agent → consider → agent
// import cycle.
//
// The per-role consider gate (roles.consider=0 for tool-heavy roles) is
// preserved by RunWithResultForced. It is independent of triggerSource: even
// when the model on a coder-role session invokes the tool explicitly, the
// gate stays a no-op signal that the role doesn't run consider.
//
// Bus events (EventStepStart / EventStepEnd) are NOT published here — that is
// the caller's responsibility, since only the auto-fire path participates in
// the agent loop's progress UI. Callers that want UI progress should bracket
// the call with their own publish pair.

import (
	"context"
	"log"

	"github.com/scotmcc/cairo2/internal/agent/consider"
	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// ConsiderInput runs the consider step and persists every side effect a
// caller might want: activation rows, message inner_voice, state deltas. It
// never returns persistence or state-apply errors — those are logged and the
// returned ConsiderResult still carries the in-memory data. The first return
// error covers only the consider call itself (model failure, config missing).
//
// Pass userMsgID = 0 when the user message has not yet been persisted; the
// inner_voice update and activation link will be skipped, and the caller is
// responsible for attaching the result later.
func ConsiderInput(
	ctx context.Context,
	database *sqliteopen.DB,
	llmClient *llm.Client,
	pub consider.EventPublisher,
	sessionID int64,
	roleName string,
	userMsgID int64,
	text string,
	triggerSource string,
) (consider.ConsiderResult, string, error) {
	if triggerSource == "" {
		triggerSource = "tui"
	}

	result, err := consider.RunWithResultForced(ctx, database, llmClient, pub, sessionID, roleName, text, triggerSource)
	if err != nil {
		return result, "", err
	}

	innerVoice := result.Summary
	if named := formatNamedAspects(result.Activations); named != "" {
		if innerVoice != "" {
			innerVoice += "\n\n" + named
		} else {
			innerVoice = named
		}
	}

	if userMsgID > 0 && innerVoice != "" {
		if uerr := database.Messages.UpdateInnerVoice(userMsgID, innerVoice); uerr != nil {
			log.Printf("consider: update inner_voice on message %d: %v", userMsgID, uerr)
		}
	}
	if userMsgID > 0 && len(result.ActivationIDs) > 0 {
		if lerr := database.ConsiderActivations.LinkToMessage(result.ActivationIDs, userMsgID); lerr != nil {
			log.Printf("consider: link activations to message %d: %v", userMsgID, lerr)
		}
	}
	if len(result.Aspects) > 0 {
		if aerr := ApplyAspectSignals(database, result.Aspects); aerr != nil {
			log.Printf("state aspect signals: %v", aerr)
		}
	}

	return result, innerVoice, nil
}
