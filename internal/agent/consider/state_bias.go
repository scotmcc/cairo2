package consider

import "github.com/scotmcc/cairo2/internal/store/identity"

// AspectStateMapping ties an aspect name to its primary state var (and optional
// secondary). Inverse=true means the aspect contributes negatively to the var.
type AspectStateMapping struct {
	Primary          string // state var name from identity.StateVar* constants
	Secondary        string // empty if none
	PrimaryInverse   bool   // true for aspects that move var in negative direction (e.g., Fear → groundedness-inverse)
	SecondaryInverse bool
}

// AspectBiasMap maps aspect name → mapping. Hardcoded per plan §4 table.
var AspectBiasMap = map[string]AspectStateMapping{
	"Joy":         {Primary: identity.StateVarGroundedness, Secondary: identity.StateVarWarmth},
	"Fear":        {Primary: identity.StateVarGroundedness, Secondary: identity.StateVarConfidence, PrimaryInverse: true, SecondaryInverse: true},
	"Heart":       {Primary: identity.StateVarWarmth, Secondary: identity.StateVarAttunement},
	"Sadness":     {Primary: identity.StateVarWarmth, PrimaryInverse: true},
	"Shadow":      {Primary: identity.StateVarSenseOfAgency, Secondary: identity.StateVarTrustInUser, PrimaryInverse: true, SecondaryInverse: true},
	"Steward":     {Primary: identity.StateVarSenseOfAgency},
	"Trust":       {Primary: identity.StateVarTrustInUser},
	"Frustration": {Primary: identity.StateVarFrustrationBaseline},
	"Curiosity":   {Primary: identity.StateVarConfidence, Secondary: identity.StateVarSenseOfAgency},
}

// clamp constrains v to [lo, hi].
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// stateVar returns the named field from s. Returns 0.5 (neutral) for unknown
// names or when s is nil.
func stateVar(s *identity.State, name string) float64 {
	if s == nil || name == "" {
		return 0.5
	}
	switch name {
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
	}
	return 0.5
}

// ApplyStateBias takes a raw alignment from the LLM and returns the post-bias
// alignment, clamped to [0, 1]. Bias is bounded by ±0.15 per spec.
//
// Formula (plan §4a):
//
//	primary_signed   = primary_value   if !PrimaryInverse   else (1.0 - primary_value)
//	secondary_signed = secondary_value if !SecondaryInverse else (1.0 - secondary_value)
//	bias = 0.1 × (primary_signed - 0.5) + 0.05 × (secondary_signed - 0.5)
//	bias = clamp(bias, -0.15, +0.15)
//	adjusted = clamp(rawAlignment + bias, 0.0, 1.0)
//
// If aspectName is not in AspectBiasMap, rawAlignment is returned unchanged.
// If state is nil, rawAlignment is returned unchanged.
func ApplyStateBias(aspectName string, rawAlignment float64, state *identity.State) float64 {
	mapping, ok := AspectBiasMap[aspectName]
	if !ok {
		return rawAlignment
	}
	if state == nil {
		return rawAlignment
	}

	primaryValue := stateVar(state, mapping.Primary)
	if mapping.PrimaryInverse {
		primaryValue = 1.0 - primaryValue
	}

	// Secondary defaults to neutral (0.5) when absent — no contribution.
	secondaryValue := 0.5
	if mapping.Secondary != "" {
		secondaryValue = stateVar(state, mapping.Secondary)
		if mapping.SecondaryInverse {
			secondaryValue = 1.0 - secondaryValue
		}
	}

	bias := 0.1*(primaryValue-0.5) + 0.05*(secondaryValue-0.5)
	bias = clamp(bias, -0.15, 0.15)

	return clamp(rawAlignment+bias, 0.0, 1.0)
}
