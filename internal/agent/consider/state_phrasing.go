package consider

import "github.com/scotmcc/cairo2/internal/db"

// phraseBand maps an upper-inclusive threshold to a qualitative phrase.
// Bands are checked in order; the first where v <= HighInclusive wins.
type phraseBand struct {
	HighInclusive float64
	Phrase        string
}

func pickPhrase(v float64, bands []phraseBand) string {
	for _, b := range bands {
		if v <= b.HighInclusive {
			return b.Phrase
		}
	}
	return bands[len(bands)-1].Phrase
}

var warmthBands = []phraseBand{
	{0.2, "feeling distant — there's no warmth right now"},
	{0.4, "lightly disconnected"},
	{0.6, "neither warm nor cold"},
	{0.8, "warming to Scot"},
	{1.0, "feeling warm and close to Scot"},
}

var trustBands = []phraseBand{
	{0.2, "trust is shaky — guarded"},
	{0.4, "wary, hard to lean in"},
	{0.6, "neutral trust — neither leaning in nor pulling back"},
	{0.8, "able to lean into what Scot says"},
	{1.0, "deeply trusting, fully landed"},
}

var attunementBands = []phraseBand{
	{0.2, "feeling lost about where Scot is"},
	{0.4, "uncertain reading"},
	{0.6, "moderately attuned"},
	{0.8, "reading Scot well right now"},
	{1.0, "deeply attuned — almost finishing his sentences"},
}

var frustrationBands = []phraseBand{
	{0.2, "deeply patient"},
	{0.4, "patient, easy"},
	{0.6, "settled, neutral baseline"},
	{0.8, "carrying real frustration; partnership feels strained"},
	{1.0, "running hot — fatigue ready to surface"},
}

var confidenceBands = []phraseBand{
	{0.2, "doubting your own capability"},
	{0.4, "uncertain about acting"},
	{0.6, "moderate self-trust"},
	{0.8, "capable, ready to act"},
	{1.0, "confident — moves come easily"},
}

var agencyBands = []phraseBand{
	{0.2, "feeling driven, not driving"},
	{0.4, "low ownership"},
	{0.6, "moderate agency"},
	{0.8, "owning the work"},
	{1.0, "fully driving — strong sense of authorship"},
}

var groundednessBands = []phraseBand{
	{0.2, "ungrounded, scattered"},
	{0.4, "lightly off-balance"},
	{0.6, "moderately steady"},
	{0.8, "grounded and present"},
	{1.0, "deeply settled — fully here"},
}

// WarmthPhrase returns the qualitative phrase for a warmth value.
func WarmthPhrase(v float64) string { return pickPhrase(v, warmthBands) }

// TrustPhrase returns the qualitative phrase for a trust_in_user value.
func TrustPhrase(v float64) string { return pickPhrase(v, trustBands) }

// AttunementPhrase returns the qualitative phrase for an attunement value.
func AttunementPhrase(v float64) string { return pickPhrase(v, attunementBands) }

// FrustrationPhrase returns the qualitative phrase for a frustration_baseline value.
func FrustrationPhrase(v float64) string { return pickPhrase(v, frustrationBands) }

// ConfidencePhrase returns the qualitative phrase for a confidence value.
func ConfidencePhrase(v float64) string { return pickPhrase(v, confidenceBands) }

// AgencyPhrase returns the qualitative phrase for a sense_of_agency value.
func AgencyPhrase(v float64) string { return pickPhrase(v, agencyBands) }

// GroundednessPhrase returns the qualitative phrase for a groundedness value.
func GroundednessPhrase(v float64) string { return pickPhrase(v, groundednessBands) }

// BuildFeltGroundLine composes a single felt-experience sentence from the
// current state. Returns a sensible fallback when state is nil.
//
// Relational vars (warmth, trust, attunement) are grouped together; self vars
// (confidence, agency, groundedness) follow; frustration baseline stands alone
// as a throughline note rather than a relational or self quality.
func BuildFeltGroundLine(s *db.State) string {
	if s == nil {
		return "Your felt ground is unknown — no state available."
	}
	return "You're " + WarmthPhrase(s.Warmth) +
		"; " + TrustPhrase(s.TrustInUser) +
		"; " + AttunementPhrase(s.Attunement) +
		". Frustration baseline: " + FrustrationPhrase(s.FrustrationBaseline) +
		". Self: " + ConfidencePhrase(s.Confidence) +
		", " + AgencyPhrase(s.SenseOfAgency) +
		", " + GroundednessPhrase(s.Groundedness) + "."
}
