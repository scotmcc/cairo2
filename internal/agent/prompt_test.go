package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/providers"
)

// openTestDB is a local copy of the db package's test helper, since tests in
// different packages can't share *_test.go helpers. It returns a fully seeded
// DB backed by a tempdir file.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenAt(path)
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// seedSession creates a session and returns its id. Convenient for tests
// that need a valid session.ID for summaries or prompt building.
func seedSession(t *testing.T, d *db.DB) int64 {
	t.Helper()
	s, err := d.Sessions.Create("test", "/tmp", "thinking_partner")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return s.ID
}

func TestBuildSystemPrompt_IncludesBaseAndRole(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)

	msg, err := BuildSystemPrompt(context.Background(), d, sid, "thinking_partner", "/tmp", nil, time.Time{}, providers.Default(), nil)
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	// The seeded base prompt introduces the agent by name (templated from
	// ai_name, default "Selene") and the seeded thinking_partner role prompt
	// describes its focus. Both should appear.
	if !strings.Contains(msg.Content, "You are Selene") {
		t.Errorf("base prompt not injected (or template didn't fire); content = %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "thinking partner") {
		t.Errorf("role addendum for thinking_partner not injected")
	}
	if !strings.Contains(msg.Content, "/tmp") {
		t.Errorf("cwd stamp not injected")
	}
}

func TestBuildSystemPrompt_TemplateSubstitution(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)

	// Override ai_name and verify the assembled prompt uses the new value
	// instead of the default Selene.
	if err := d.Config.Set("ai_name", "Nyx"); err != nil {
		t.Fatalf("set ai_name: %v", err)
	}

	msg, err := BuildSystemPrompt(context.Background(), d, sid, "thinking_partner", "/tmp", nil, time.Time{}, providers.Default(), nil)
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	if !strings.Contains(msg.Content, "You are Nyx") {
		t.Errorf("ai_name override didn't propagate through template; content starts: %s", truncForErr(msg.Content, 160))
	}
	if strings.Contains(msg.Content, "{{ai_name}}") {
		t.Errorf("raw template tag leaked through: %s", msg.Content)
	}
}

func TestApplyTemplates_EmptyKeyBecomesEmpty(t *testing.T) {
	got := applyTemplates("Hello, {{user_name}}!", map[string]string{"user_name": ""})
	if got != "Hello, !" {
		t.Errorf("empty key should substitute to empty string; got %q", got)
	}
}

func TestApplyTemplates_UnknownKeyBecomesEmpty(t *testing.T) {
	got := applyTemplates("x={{unset}} y", map[string]string{})
	if got != "x= y" {
		t.Errorf("unknown key should drop; got %q", got)
	}
}

func TestApplyTemplates_IgnoresMalformed(t *testing.T) {
	// Double braces with no simple identifier inside shouldn't match.
	in := "keep {{ this }} and {{123bad}} alone"
	got := applyTemplates(in, map[string]string{"this": "X"})
	if got != in {
		t.Errorf("malformed patterns should pass through; got %q", got)
	}
}

// TestApplyTemplates_Exported verifies the exported ApplyTemplates function
// matches the behaviour of the package-private applyTemplates. This is the
// entry point for callers outside the agent package (tui, cli) that need to
// substitute {{key}} placeholders in skill content before sending it to the model.
func TestApplyTemplates_Exported(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		vars   map[string]string
		expect string
	}{
		{
			name:   "substitutes ai_name",
			input:  "Hi — I'm {{ai_name}}. What should I call you?",
			vars:   map[string]string{"ai_name": "Selene"},
			expect: "Hi — I'm Selene. What should I call you?",
		},
		{
			name:   "substitutes user_name",
			input:  "You are {{user_name}}'s thinking partner.",
			vars:   map[string]string{"user_name": "Scot"},
			expect: "You are Scot's thinking partner.",
		},
		{
			name:   "empty value replaces placeholder with nothing",
			input:  "Hello, {{user_name}}!",
			vars:   map[string]string{"user_name": ""},
			expect: "Hello, !",
		},
		{
			name:   "missing key replaces placeholder with nothing",
			input:  "Hi {{unknown}}",
			vars:   map[string]string{},
			expect: "Hi ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyTemplates(tc.input, tc.vars)
			if got != tc.expect {
				t.Errorf("ApplyTemplates(%q) = %q; want %q", tc.input, got, tc.expect)
			}
		})
	}
}

// TestInitSkill_NoLiteralPlaceholders verifies that after ApplyTemplates is
// applied to the seeded init skill, the known {{ai_name}} and {{user_name}}
// placeholders are resolved — i.e. the model receives actual values, not
// literal brace syntax. Regression guard for: init skill sends {{ai_name}}
// verbatim causing small models to echo it literally instead of using the
// configured name.
func TestInitSkill_NoLiteralPlaceholders(t *testing.T) {
	d := openTestDB(t)

	skill, err := d.Skills.Get("init")
	if err != nil || skill == nil {
		t.Fatal("init skill missing from seeded DB")
	}

	vars, err := d.Config.All()
	if err != nil {
		t.Fatalf("Config.All: %v", err)
	}

	result := ApplyTemplates(skill.Content, vars)

	// After substitution, no {{...}} placeholders should remain for the two
	// identity keys that the init skill uses. (Other placeholders from custom
	// user content can be ignored; these two are the ones the skill relies on.)
	if strings.Contains(result, "{{ai_name}}") {
		t.Errorf("{{ai_name}} not substituted in init skill after ApplyTemplates")
	}
	if strings.Contains(result, "{{user_name}}") {
		t.Errorf("{{user_name}} not substituted in init skill after ApplyTemplates")
	}

	// The seeded default is "Selene" — verify it appears.
	if !strings.Contains(result, "Selene") {
		t.Errorf("expected 'Selene' in substituted init skill; got (first 200 chars): %s", truncForErr(result, 200))
	}
}

// TestInitSkill_UsesValidConfigAction verifies that the seeded init skill
// does not call config(action="list") — the config tool only accepts
// action="all" for listing all keys. Using "list" causes an error that can
// confuse the model during Phase 0 of the init flow.
func TestInitSkill_UsesValidConfigAction(t *testing.T) {
	d := openTestDB(t)

	skill, err := d.Skills.Get("init")
	if err != nil || skill == nil {
		t.Fatal("init skill missing from seeded DB")
	}

	if strings.Contains(skill.Content, `config(action="list")`) {
		t.Errorf(`init skill uses config(action="list") which is invalid — use config(action="all") instead`)
	}
}

func truncForErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func TestBuildSystemPrompt_MemoryOverflowHint(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)

	// memory_limit default is 15 — insert 17 memories and expect "2 more"
	// overflow text.
	for i := 0; i < 17; i++ {
		if _, err := d.Memories.Add("mem"+itoa(i), "[]", "", nil); err != nil {
			t.Fatalf("add memory %d: %v", i, err)
		}
	}

	msg, err := BuildSystemPrompt(context.Background(), d, sid, "thinking_partner", "/tmp", nil, time.Time{}, providers.Default(), nil)
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	if !strings.Contains(msg.Content, "2 more memories") {
		t.Errorf("overflow hint missing; expected '2 more memories' in:\n%s", msg.Content)
	}
}

func TestBuildSystemPrompt_SummariesSectionCapsAtContextLimit(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)

	// summary_context default is 4. Insert 6 summaries, expect only the
	// latest 4 in the prompt.
	for i := 0; i < 6; i++ {
		if _, err := d.Summaries.Add(sid, int64(i), int64(i), "summary-"+itoa(i), "", nil); err != nil {
			t.Fatalf("add summary %d: %v", i, err)
		}
	}

	msg, err := BuildSystemPrompt(context.Background(), d, sid, "thinking_partner", "/tmp", nil, time.Time{}, providers.Default(), nil)
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	if !strings.Contains(msg.Content, "Conversation context") {
		t.Errorf("summaries section header missing")
	}
	// The two oldest (summary-0, summary-1) should have been dropped by the
	// LatestForSession cap.
	if strings.Contains(msg.Content, "summary-0") || strings.Contains(msg.Content, "summary-1") {
		t.Errorf("oldest summaries should have been dropped by summary_context cap")
	}
	// The latest (summary-5) must be present.
	if !strings.Contains(msg.Content, "summary-5") {
		t.Errorf("most recent summary missing")
	}
}

// itoa avoids pulling strconv in the test file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
