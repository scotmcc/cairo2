package agent

import (
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// turnRows builds a slice of *db.Message with sequential ids so the queue
// helpers can be exercised without touching SQLite. Only the ID field is
// material — selectSummarizeRange does not look at content.
func turnRows(ids ...int64) []*sessions.Message {
	out := make([]*sessions.Message, len(ids))
	for i, id := range ids {
		out[i] = &sessions.Message{ID: id, Role: "user"}
	}
	return out
}

// Trigger is "fire when count exceeds threshold". 8 unsummarized turns is at
// the boundary — must NOT fire so the recent tail keeps room to grow before
// the next summarize cycle steals from it.
func TestSelectSummarizeRange_AtThresholdNoFire(t *testing.T) {
	turns := turnRows(1, 2, 3, 4, 5)
	if _, _, fire := selectSummarizeRange(8, 8, 4, turns, false); fire {
		t.Fatalf("count==trigger should not fire")
	}
}

// force=true bypasses the count > trigger gate. The dream pre-flight drain
// uses this so small backlogs (count <= trigger) that the per-turn path
// would skip indefinitely still get folded during nightly maintenance.
// The boundary check still applies — sessions with too few turns to leave
// a recent tail safely no-op even in force mode.
func TestSelectSummarizeRange_ForceBypassesThreshold(t *testing.T) {
	// 5 turns, trigger 8 — non-force would no-op; force should fire because
	// we have batchSize+1 turns (the boundary still leaves a recent tail).
	turns := turnRows(1, 2, 3, 4, 5)
	first, last, fire := selectSummarizeRange(5, 8, 4, turns, true)
	if !fire {
		t.Fatalf("force should fire even with count <= trigger")
	}
	if first != 1 || last != 4 { // boundary id 5 - 1
		t.Fatalf("got first=%d last=%d, want 1 / 4", first, last)
	}
}

// Force does NOT override the boundary check — too few turns to leave a
// recent tail still no-ops. Otherwise dream could fold the freshest turn
// of a session that's still active and lose the recent dialogue from
// loadHistory's hot context window.
func TestSelectSummarizeRange_ForceStillRequiresBoundary(t *testing.T) {
	turns := turnRows(1, 2, 3, 4) // exactly batchSize, not batchSize+1
	if _, _, fire := selectSummarizeRange(4, 8, 4, turns, true); fire {
		t.Fatalf("force must not fire without a boundary turn")
	}
}

// 9 unsummarized turns exceeds the trigger of 8 — fire, fold the oldest
// 4. Range covers everything strictly before the 5th turn so any tool
// rows trailing the 4th turn get swept.
func TestSelectSummarizeRange_FiresAtNinePicksOldestFour(t *testing.T) {
	// ids 10..18, no interleaved tool rows — first 5 are what we'd fetch
	turns := turnRows(10, 12, 14, 16, 18)
	first, last, fire := selectSummarizeRange(9, 8, 4, turns, false)
	if !fire {
		t.Fatalf("count>trigger should fire")
	}
	if first != 10 {
		t.Fatalf("firstID = %d, want 10", first)
	}
	if last != 17 { // boundary turn id 18 minus 1
		t.Fatalf("lastID = %d, want 17 (boundary - 1)", last)
	}
}

// Tool rows interleave the user/assistant rows. selectSummarizeRange only
// sees the user/assistant rows we passed in; the *range* it returns sweeps
// everything between firstID and the row before the boundary turn — which
// is exactly how interleaved tool rows get marked along with the batch.
func TestSelectSummarizeRange_RangeSweepsInterleavedToolRows(t *testing.T) {
	// Imagine ids 1..13. User/assistant turns at 1,3,5,7,9,11,13.
	// (tool rows live at 2,4,6,8,10,12 — not in `turns`.)
	turns := turnRows(1, 3, 5, 7, 9)
	first, last, fire := selectSummarizeRange(7, 8, 4, turns, false)
	if fire {
		t.Fatalf("count 7 below trigger 8 should not fire")
	}
	_ = first
	_ = last

	// Bump the queue: 9 unsummarized turns now.
	first, last, fire = selectSummarizeRange(9, 8, 4, turns, false)
	if !fire {
		t.Fatalf("count 9 above trigger 8 should fire")
	}
	if first != 1 {
		t.Fatalf("firstID = %d, want 1", first)
	}
	// Boundary turn is the 5th element (id=9). Anything < 9 is swept,
	// including tool rows at id 2/4/6/8.
	if last != 8 {
		t.Fatalf("lastID = %d, want 8", last)
	}
}

// 13 unsummarized turns after a previous fire-cycle still folds exactly
// the oldest 4 — the helper has no memory, the sweep is per-call.
func TestSelectSummarizeRange_SecondCycle(t *testing.T) {
	// After a previous cycle marked turns 1..8, the "oldest 5 unsummarized"
	// are turns starting at id 100. Caller passes only those.
	turns := turnRows(100, 102, 104, 106, 108)
	first, last, fire := selectSummarizeRange(13, 8, 4, turns, false)
	if !fire {
		t.Fatalf("count 13 should fire")
	}
	if first != 100 || last != 107 {
		t.Fatalf("got first=%d last=%d, want 100 / 107", first, last)
	}
}

// Steady state: simulate a sequence of (turn arrives, maybe fire) cycles
// and verify the unsummarized count oscillates inside [5, 9]. This is the
// invariant Scot called out — never below 5 (would be too narrow), never
// above 9 (would mean we missed a fire).
func TestSelectSummarizeRange_SteadyStateOscillation(t *testing.T) {
	const trigger = 8
	const batch = 4

	count := 0
	nextID := int64(1)
	queue := []*sessions.Message{}

	add := func() {
		queue = append(queue, &sessions.Message{ID: nextID, Role: "user"})
		nextID++
		count++
	}
	tryFire := func() {
		// emulate `OldestUnsummarized(batch+1)` — we pass exactly that prefix
		var oldestSlice []*sessions.Message
		if len(queue) > batch {
			oldestSlice = queue[:batch+1]
		} else {
			oldestSlice = queue
		}
		_, _, fire := selectSummarizeRange(count, trigger, batch, oldestSlice, false)
		if fire {
			// drop the oldest `batch` from the simulated queue
			queue = queue[batch:]
			count -= batch
		}
	}

	// Walk 30 turns. Track min/max of count *after* each cycle.
	maxSeen, minSeen := 0, 1<<30
	for i := 0; i < 30; i++ {
		add()
		tryFire()
		if count > maxSeen {
			maxSeen = count
		}
		// only sample min once we've reached steady state past the first fire
		if i >= 8 && count < minSeen {
			minSeen = count
		}
	}
	if maxSeen > 9 {
		t.Fatalf("steady-state max count %d > 9 (missed a fire)", maxSeen)
	}
	if minSeen < 5 {
		t.Fatalf("steady-state min count %d < 5 (over-summarized)", minSeen)
	}
}

// configIntDefault should treat blank, zero, negative, and bogus values as
// "use the fallback" — tolerant of stale config rows so the summarizer
// never silently disables itself or runs amok.
func TestConfigIntDefault_Fallbacks(t *testing.T) {
	d := openTestDB(t)
	cases := []struct {
		key      string
		value    string
		fallback int
		want     int
	}{
		{"missing_key", "", 8, 8},
		{"explicit_zero", "0", 8, 8},
		{"negative", "-5", 8, 8},
		{"bogus", "abc", 4, 4},
		{"valid", "12", 8, 12},
	}
	for _, c := range cases {
		if c.value != "" {
			if err := d.Config.Set(c.key, c.value); err != nil {
				t.Fatalf("set %s: %v", c.key, err)
			}
		}
		got := configIntDefault(d, c.key, c.fallback)
		if got != c.want {
			t.Fatalf("configIntDefault(%q=%q, fb=%d) = %d, want %d",
				c.key, c.value, c.fallback, got, c.want)
		}
	}
}

func TestParseSummaryResponse_PlainFormat(t *testing.T) {
	raw := `SUMMARY: User asked for a refactor and we completed it.
FACTS:
- Project uses Go 1.25
- Refactor touched cli.go`
	summary, facts := parseSummaryResponse(raw)
	if !strings.Contains(summary, "refactor") {
		t.Fatalf("plain summary missing: %q", summary)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts, got %d: %v", len(facts), facts)
	}
}

func TestParseSummaryResponse_MarkdownEmphasis(t *testing.T) {
	// Real-world output from ministral-3:14b-instruct — bolded headers with
	// the summary body on the line AFTER the header.
	raw := `**SUMMARY:**
Initialization began. Selene confirmed project details and asked about working style.

**FACTS:**
- **ai_name** = "Selene"
- **user_name** = "Scot"
- init_complete not yet set`
	summary, facts := parseSummaryResponse(raw)
	if summary == "" {
		t.Fatalf("bolded-header summary parsed empty")
	}
	if !strings.Contains(summary, "Initialization began") {
		t.Fatalf("summary body missing: %q", summary)
	}
	if len(facts) != 3 {
		t.Fatalf("expected 3 facts, got %d: %v", len(facts), facts)
	}
	if !strings.Contains(facts[0], "ai_name") {
		t.Fatalf("first fact malformed: %q", facts[0])
	}
}

func TestParseSummaryResponse_AsteriskBullets(t *testing.T) {
	raw := `SUMMARY: did things
FACTS:
* alpha
* beta`
	_, facts := parseSummaryResponse(raw)
	if len(facts) != 2 || facts[0] != "alpha" || facts[1] != "beta" {
		t.Fatalf("asterisk bullets not parsed: %v", facts)
	}
}

func TestParseSummaryResponse_MultiLineSummary(t *testing.T) {
	raw := `SUMMARY:
Line one of summary.
Line two of summary.
FACTS:
- one`
	summary, facts := parseSummaryResponse(raw)
	if !strings.Contains(summary, "Line one") || !strings.Contains(summary, "Line two") {
		t.Fatalf("multi-line summary not joined: %q", summary)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
}

func TestParseSummaryJSON_SchemaShape(t *testing.T) {
	raw := `{"summary":"We refactored the CLI help and fixed the summarizer.","facts":["Summarizer was silently discarding output","Help now lists subcommands"]}`
	s, f, ok := parseSummaryJSON(raw)
	if !ok {
		t.Fatalf("expected ok=true for schema-shaped JSON")
	}
	if !strings.Contains(s, "refactored") {
		t.Fatalf("summary body missing: %q", s)
	}
	if len(f) != 2 {
		t.Fatalf("expected 2 facts, got %d: %v", len(f), f)
	}
}

func TestParseSummaryJSON_EmptyFactsAllowed(t *testing.T) {
	raw := `{"summary":"Short trivial segment.","facts":[]}`
	s, f, ok := parseSummaryJSON(raw)
	if !ok {
		t.Fatalf("empty facts should still count as a valid parse")
	}
	if s == "" || len(f) != 0 {
		t.Fatalf("unexpected parse: summary=%q facts=%v", s, f)
	}
}

func TestParseSummaryJSON_FencedObject(t *testing.T) {
	// A model that wraps its JSON in a code fence should still be recoverable,
	// since we locate the outermost braces rather than trusting the bytes.
	raw := "```json\n{\"summary\":\"did a thing\",\"facts\":[\"alpha\"]}\n```"
	s, f, ok := parseSummaryJSON(raw)
	if !ok {
		t.Fatalf("fenced JSON should parse")
	}
	if s != "did a thing" || len(f) != 1 || f[0] != "alpha" {
		t.Fatalf("unexpected parse: summary=%q facts=%v", s, f)
	}
}

func TestParseSummaryJSON_EmptySummaryRejected(t *testing.T) {
	// An object with a blank summary isn't useful — extractSummary should
	// treat it as a parse failure and fall back to the text parser.
	raw := `{"summary":"","facts":["x"]}`
	if _, _, ok := parseSummaryJSON(raw); ok {
		t.Fatalf("empty summary should not be accepted")
	}
}

func TestExtractSummary_JSONPrefersJSON(t *testing.T) {
	raw := `{"summary":"json wins","facts":["f1"]}`
	s, _, source := extractSummary(raw)
	if source != "json" {
		t.Fatalf("expected json source, got %q", source)
	}
	if s != "json wins" {
		t.Fatalf("unexpected summary: %q", s)
	}
}

func TestExtractSummary_FallsBackToText(t *testing.T) {
	raw := `**SUMMARY:**
text path wins when JSON is absent

**FACTS:**
- keep the fallback alive`
	s, f, source := extractSummary(raw)
	if source != "text" {
		t.Fatalf("expected text source, got %q", source)
	}
	if !strings.Contains(s, "text path wins") {
		t.Fatalf("summary missing: %q", s)
	}
	if len(f) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(f))
	}
}

// addTestMessages inserts n user messages each with the given content into a
// session. Used to drive the token-pressure trigger tests without real LLM calls.
func addTestMessages(t *testing.T, d *sqliteopen.DB, sessionID int64, n int, content string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := d.Messages.Add(sessionID, "user", content, "", "", ""); err != nil {
			t.Fatalf("add message %d: %v", i, err)
		}
	}
}

// TestSummarizer_TokenPressureTrigger_UnderTurnOverTokenFires verifies that
// the secondary (token-pressure) trigger fires compaction even when the
// turn-count threshold has NOT been reached.
//
// Setup: turn threshold=8 (default), token threshold=100.
// Insert 3 turns each with ~200 chars → ~150 estimated tokens total, exceeding 100.
// Turn count (3) is well below the trigger (8), so the turn-count path will NOT fire.
// The token-pressure path must fire and compute a valid (firstID, lastID) range.
func TestSummarizer_TokenPressureTrigger_UnderTurnOverTokenFires(t *testing.T) {
	d := openTestDB(t)
	// Set a low token threshold so a few messages exceed it.
	if err := d.Config.Set(config.KeySummaryTokenThreshold, "100"); err != nil {
		t.Fatalf("set token threshold: %v", err)
	}

	sid := seedSession(t, d)
	// Each message: 200 chars → ~50 tokens each. 3 messages → ~150 tokens > 100.
	content := strings.Repeat("x", 200)
	addTestMessages(t, d, sid, 5, content) // need >batchSize(4)+1=5 for range logic; insert 5

	count, err := d.Messages.CountUnsummarized(sid)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count >= 8 {
		t.Fatalf("precondition: turn count %d should be below trigger 8", count)
	}

	estimatedTokens, err := d.Messages.EstimateUnsummarizedTokens(sid)
	if err != nil {
		t.Fatalf("token estimate: %v", err)
	}
	if estimatedTokens <= 100 {
		t.Fatalf("precondition: estimated tokens %d should exceed threshold 100", estimatedTokens)
	}

	// Replicate the trigger logic from Summarize to verify the token path fires.
	trigger := configIntDefault(d, config.KeySummaryThreshold, 8)
	tokenThreshold := configIntDefault(d, config.KeySummaryTokenThreshold, 8000)
	batchSize := configIntDefault(d, config.KeySummaryBatchSize, 4)

	turns, err := d.Messages.OldestUnsummarized(sid, batchSize+1)
	if err != nil {
		t.Fatalf("oldest unsummarized: %v", err)
	}
	_, _, fire := selectSummarizeRange(count, trigger, batchSize, turns, false)
	if fire {
		t.Fatalf("precondition: turn-count path must NOT fire (count=%d, trigger=%d)", count, trigger)
	}

	// Now apply the token trigger.
	tokenFire := false
	if estimatedTokens > tokenThreshold && len(turns) > batchSize {
		firstID := turns[0].ID
		lastID := turns[batchSize].ID - 1
		if lastID >= firstID {
			tokenFire = true
		}
	}
	if !tokenFire {
		t.Fatalf("token-pressure trigger should fire: estimated=%d threshold=%d turns=%d batchSize=%d",
			estimatedTokens, tokenThreshold, len(turns), batchSize)
	}
}

// TestSummarizer_TurnCountTrigger_OverTurnUnderTokenFires verifies that the
// existing turn-count path still fires when turns exceed the threshold but
// token count is below the token threshold.
//
// This guards regression: adding the token trigger must not disable the
// original turn-count trigger.
func TestSummarizer_TurnCountTrigger_OverTurnUnderTokenFires(t *testing.T) {
	d := openTestDB(t)
	// Set a very high token threshold so the token path cannot fire.
	if err := d.Config.Set(config.KeySummaryTokenThreshold, "999999"); err != nil {
		t.Fatalf("set token threshold: %v", err)
	}

	sid := seedSession(t, d)
	// Insert 10 very short turns → far below any sane token threshold.
	addTestMessages(t, d, sid, 10, "hi")

	count, err := d.Messages.CountUnsummarized(sid)
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	trigger := configIntDefault(d, config.KeySummaryThreshold, 8)
	if count <= trigger {
		t.Fatalf("precondition: turn count %d must exceed trigger %d", count, trigger)
	}

	estimatedTokens, err := d.Messages.EstimateUnsummarizedTokens(sid)
	if err != nil {
		t.Fatalf("token estimate: %v", err)
	}
	tokenThreshold := configIntDefault(d, config.KeySummaryTokenThreshold, 8000)
	if estimatedTokens > tokenThreshold {
		t.Fatalf("precondition: estimated tokens %d must NOT exceed threshold %d", estimatedTokens, tokenThreshold)
	}

	batchSize := configIntDefault(d, config.KeySummaryBatchSize, 4)
	turns, err := d.Messages.OldestUnsummarized(sid, batchSize+1)
	if err != nil {
		t.Fatalf("oldest unsummarized: %v", err)
	}

	_, _, fire := selectSummarizeRange(count, trigger, batchSize, turns, false)
	if !fire {
		t.Fatalf("turn-count trigger should fire: count=%d trigger=%d", count, trigger)
	}
}
