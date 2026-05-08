package consider

import (
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/db"
)

// bandTest is a single assertion for a phrase function.
type bandTest struct {
	v    float64
	want string
}

func checkBands(t *testing.T, name string, fn func(float64) string, cases []bandTest) {
	t.Helper()
	for _, tc := range cases {
		got := fn(tc.v)
		if got != tc.want {
			t.Errorf("%s(%.2f) = %q, want %q", name, tc.v, got, tc.want)
		}
	}
}

func TestWarmthPhrase(t *testing.T) {
	checkBands(t, "WarmthPhrase", WarmthPhrase, []bandTest{
		{0.0, "feeling distant — there's no warmth right now"},
		{0.19, "feeling distant — there's no warmth right now"},
		{0.2, "feeling distant — there's no warmth right now"},
		{0.21, "lightly disconnected"},
		{0.4, "lightly disconnected"},
		{0.41, "neither warm nor cold"},
		{0.6, "neither warm nor cold"},
		{0.61, "warming to Scot"},
		{0.8, "warming to Scot"},
		{0.81, "feeling warm and close to Scot"},
		{1.0, "feeling warm and close to Scot"},
	})
}

func TestTrustPhrase(t *testing.T) {
	checkBands(t, "TrustPhrase", TrustPhrase, []bandTest{
		{0.0, "trust is shaky — guarded"},
		{0.2, "trust is shaky — guarded"},
		{0.21, "wary, hard to lean in"},
		{0.4, "wary, hard to lean in"},
		{0.41, "neutral trust — neither leaning in nor pulling back"},
		{0.6, "neutral trust — neither leaning in nor pulling back"},
		{0.61, "able to lean into what Scot says"},
		{0.8, "able to lean into what Scot says"},
		{0.81, "deeply trusting, fully landed"},
		{1.0, "deeply trusting, fully landed"},
	})
}

func TestAttunementPhrase(t *testing.T) {
	checkBands(t, "AttunementPhrase", AttunementPhrase, []bandTest{
		{0.0, "feeling lost about where Scot is"},
		{0.2, "feeling lost about where Scot is"},
		{0.21, "uncertain reading"},
		{0.4, "uncertain reading"},
		{0.41, "moderately attuned"},
		{0.6, "moderately attuned"},
		{0.61, "reading Scot well right now"},
		{0.8, "reading Scot well right now"},
		{0.81, "deeply attuned — almost finishing his sentences"},
		{1.0, "deeply attuned — almost finishing his sentences"},
	})
}

func TestFrustrationPhrase(t *testing.T) {
	checkBands(t, "FrustrationPhrase", FrustrationPhrase, []bandTest{
		{0.0, "deeply patient"},
		{0.2, "deeply patient"},
		{0.21, "patient, easy"},
		{0.4, "patient, easy"},
		{0.41, "settled, neutral baseline"},
		{0.6, "settled, neutral baseline"},
		{0.61, "carrying real frustration; partnership feels strained"},
		{0.8, "carrying real frustration; partnership feels strained"},
		{0.81, "running hot — fatigue ready to surface"},
		{1.0, "running hot — fatigue ready to surface"},
	})
}

func TestConfidencePhrase(t *testing.T) {
	checkBands(t, "ConfidencePhrase", ConfidencePhrase, []bandTest{
		{0.0, "doubting your own capability"},
		{0.2, "doubting your own capability"},
		{0.21, "uncertain about acting"},
		{0.4, "uncertain about acting"},
		{0.41, "moderate self-trust"},
		{0.6, "moderate self-trust"},
		{0.61, "capable, ready to act"},
		{0.8, "capable, ready to act"},
		{0.81, "confident — moves come easily"},
		{1.0, "confident — moves come easily"},
	})
}

func TestAgencyPhrase(t *testing.T) {
	checkBands(t, "AgencyPhrase", AgencyPhrase, []bandTest{
		{0.0, "feeling driven, not driving"},
		{0.2, "feeling driven, not driving"},
		{0.21, "low ownership"},
		{0.4, "low ownership"},
		{0.41, "moderate agency"},
		{0.6, "moderate agency"},
		{0.61, "owning the work"},
		{0.8, "owning the work"},
		{0.81, "fully driving — strong sense of authorship"},
		{1.0, "fully driving — strong sense of authorship"},
	})
}

func TestGroundednessPhrase(t *testing.T) {
	checkBands(t, "GroundednessPhrase", GroundednessPhrase, []bandTest{
		{0.0, "ungrounded, scattered"},
		{0.2, "ungrounded, scattered"},
		{0.21, "lightly off-balance"},
		{0.4, "lightly off-balance"},
		{0.41, "moderately steady"},
		{0.6, "moderately steady"},
		{0.61, "grounded and present"},
		{0.8, "grounded and present"},
		{0.81, "deeply settled — fully here"},
		{1.0, "deeply settled — fully here"},
	})
}

func TestBuildFeltGroundLine_NilState(t *testing.T) {
	got := BuildFeltGroundLine(nil)
	if got == "" {
		t.Fatal("BuildFeltGroundLine(nil) returned empty string")
	}
	// Should not panic and should produce a sensible fallback.
	if !strings.Contains(got, "unknown") {
		t.Errorf("nil fallback should mention unknown state, got %q", got)
	}
}

func TestBuildFeltGroundLine_ExampleValues(t *testing.T) {
	// Reference values from the brief: warmth=0.7, trust=0.6, attunement=0.5,
	// frust=0.3, conf=0.55, agency=0.5, grounded=0.62.
	s := &db.State{
		Warmth:              0.7,
		TrustInUser:         0.6,
		Attunement:          0.5,
		FrustrationBaseline: 0.3,
		Confidence:          0.55,
		SenseOfAgency:       0.5,
		Groundedness:        0.62,
	}
	got := BuildFeltGroundLine(s)
	if got == "" {
		t.Fatal("BuildFeltGroundLine returned empty string for valid state")
	}
	// Verify key phrases land in the right bands.
	if !strings.Contains(got, "warming to Scot") {
		t.Errorf("warmth 0.7 should produce 'warming to Scot', got %q", got)
	}
	if !strings.Contains(got, "neutral trust") {
		t.Errorf("trust 0.6 should produce 'neutral trust', got %q", got)
	}
	if !strings.Contains(got, "moderately attuned") {
		t.Errorf("attunement 0.5 should produce 'moderately attuned', got %q", got)
	}
	if !strings.Contains(got, "patient, easy") {
		t.Errorf("frustration 0.3 should produce 'patient, easy', got %q", got)
	}
	if !strings.Contains(got, "moderate self-trust") {
		t.Errorf("confidence 0.55 should produce 'moderate self-trust', got %q", got)
	}
	if !strings.Contains(got, "moderate agency") {
		t.Errorf("agency 0.5 should produce 'moderate agency', got %q", got)
	}
	if !strings.Contains(got, "grounded and present") {
		t.Errorf("groundedness 0.62 should produce 'grounded and present', got %q", got)
	}
}
