package sqliteopen_test

import (
	"strings"
	"testing"

	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// Test_SeedSkills_InitContainsOnboardingFlow asserts the seeded /init skill
// is the rich multi-turn onboarding prompt, not a stub. Regression guard for
// the v0.2.1 cleanup era — if a future cleanup gutted the skill content, the
// /init flow degrades into a single fake-completion turn (Selene replies
// "Got it, I've stored your name" without actually asking anything).
func Test_SeedSkills_InitContainsOnboardingFlow(t *testing.T) {
	database := testdb.OpenTestDB(t)

	skill, err := database.Skills.Get("init")
	if err != nil {
		t.Fatalf("Skills.Get(\"init\"): %v", err)
	}
	if skill == nil {
		t.Fatal("init skill missing from seeded DB")
	}

	// Anchors that prove the multi-turn onboarding prompt is intact.
	// Each phrase is load-bearing — pick stable wording that wouldn't
	// drift from minor edits but would disappear if the skill were
	// replaced by a stub.
	// Note: "init_complete" was intentionally removed from skill_init.txt
	// in commit fix(init): the harness now sets it deterministically after
	// the /init turn completes rather than relying on the model to do it.
	wantPhrases := []string{
		"Ask questions ONE AT A TIME",
		"Phase 1: Meet",
		"Phase 2: The Project",
		"Phase 3: How We Work",
		"non-negotiable rules",
		"Finish With",
	}
	for _, want := range wantPhrases {
		if !strings.Contains(skill.Content, want) {
			t.Errorf("init skill missing expected phrase %q\n--- content ---\n%s",
				want, skill.Content)
		}
	}

	// Length floor: anything under a kilobyte means the skill was gutted.
	if len(skill.Content) < 1500 {
		t.Errorf("init skill suspiciously short (%d bytes); expected the full onboarding prompt",
			len(skill.Content))
	}
}
