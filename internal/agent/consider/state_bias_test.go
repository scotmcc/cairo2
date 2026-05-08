package consider

import (
	"testing"

	"github.com/scotmcc/cairo2/internal/store/identity"
)

// makeState builds a *identity.State with all vars set to the given value.
func makeState(v float64) *identity.State {
	return &identity.State{
		Confidence:          v,
		TrustInUser:         v,
		Warmth:              v,
		FrustrationBaseline: v,
		SenseOfAgency:       v,
		Attunement:          v,
		Groundedness:        v,
	}
}

// makeStateWith builds a *identity.State with individual overrides for named vars.
func makeStateWith(defaults float64, overrides map[string]float64) *identity.State {
	s := makeState(defaults)
	for k, v := range overrides {
		switch k {
		case identity.StateVarConfidence:
			s.Confidence = v
		case identity.StateVarTrustInUser:
			s.TrustInUser = v
		case identity.StateVarWarmth:
			s.Warmth = v
		case identity.StateVarFrustrationBaseline:
			s.FrustrationBaseline = v
		case identity.StateVarSenseOfAgency:
			s.SenseOfAgency = v
		case identity.StateVarAttunement:
			s.Attunement = v
		case identity.StateVarGroundedness:
			s.Groundedness = v
		}
	}
	return s
}

func approxEqual(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestApplyStateBias(t *testing.T) {
	neutralState := makeState(0.5) // all vars at 0.5 → zero bias

	tests := []struct {
		name       string
		aspect     string
		raw        float64
		state      *identity.State
		wantApprox float64
		wantExact  bool // when true, result must equal wantApprox exactly
	}{
		{
			name:       "unknown aspect — no change",
			aspect:     "Nonexistent",
			raw:        0.6,
			state:      neutralState,
			wantApprox: 0.6,
			wantExact:  true,
		},
		{
			name:       "nil state — no change",
			aspect:     "Joy",
			raw:        0.5,
			state:      nil,
			wantApprox: 0.5,
			wantExact:  true,
		},
		{
			name:       "all vars neutral — zero bias, raw returned unchanged",
			aspect:     "Joy",
			raw:        0.6,
			state:      neutralState,
			wantApprox: 0.6,
			wantExact:  true,
		},
		{
			// Joy: primary=groundedness (0.8), secondary=warmth (0.5 neutral)
			// primary_signed = 0.8; secondary_signed = 0.5 (neutral)
			// bias = 0.1*(0.8-0.5) + 0.05*(0.5-0.5) = 0.03 + 0 = 0.03
			// adjusted = 0.7 + 0.03 = 0.73
			name:   "Joy: high primary groundedness → positive bias",
			aspect: "Joy",
			raw:    0.7,
			state: makeStateWith(0.5, map[string]float64{
				identity.StateVarGroundedness: 0.8,
			}),
			wantApprox: 0.73,
		},
		{
			// Joy: primary=groundedness (0.2), secondary=warmth (0.5 neutral)
			// bias = 0.1*(0.2-0.5) = -0.03
			// adjusted = 0.7 - 0.03 = 0.67
			name:   "Joy: low primary groundedness → negative bias",
			aspect: "Joy",
			raw:    0.7,
			state: makeStateWith(0.5, map[string]float64{
				identity.StateVarGroundedness: 0.2,
			}),
			wantApprox: 0.67,
		},
		{
			// Fear: primary=groundedness (inverse), secondary=confidence (inverse)
			// Both vars at 0.5: primary_signed = 1-0.5 = 0.5, secondary_signed = 1-0.5 = 0.5
			// bias = 0.1*(0.5-0.5) + 0.05*(0.5-0.5) = 0 → zero bias
			// adjusted = 0.5
			name:       "Fear: all vars at 0.5 → zero bias (inverse symmetry)",
			aspect:     "Fear",
			raw:        0.5,
			state:      neutralState,
			wantApprox: 0.5,
			wantExact:  true,
		},
		{
			// Fear: primary=groundedness (inverse), so high groundedness → low primary_signed
			// groundedness=0.8 → primary_signed = 1-0.8 = 0.2 → bias = 0.1*(0.2-0.5) = -0.03
			// adjusted = 0.7 - 0.03 = 0.67
			// Compare with Joy at same groundedness=0.8 → +0.03. Opposite sign confirmed.
			name:   "Fear: high groundedness (inverse) → negative bias (opposite of Joy)",
			aspect: "Fear",
			raw:    0.7,
			state: makeStateWith(0.5, map[string]float64{
				identity.StateVarGroundedness: 0.8,
			}),
			wantApprox: 0.67,
		},
		{
			// Joy at groundedness=0.8 gives +0.03 → 0.73.
			// Fear at groundedness=0.8 gives -0.03 → 0.67.
			// This test validates pairing semantics: same var, opposite valence.
			name:   "pairing case: Joy+high groundedness up, Fear+high groundedness down",
			aspect: "Joy",
			raw:    0.7,
			state: makeStateWith(0.5, map[string]float64{
				identity.StateVarGroundedness: 0.8,
			}),
			wantApprox: 0.73, // Joy goes up
		},
		{
			// Sadness: primary=warmth (inverse) — no secondary.
			// warmth=0.1 → primary_signed = 1-0.1 = 0.9 → bias = 0.1*(0.9-0.5) = 0.04
			// adjusted = 0.5 + 0.04 = 0.54
			name:   "Sadness: inverse mapping — low warmth → positive bias for Sadness",
			aspect: "Sadness",
			raw:    0.5,
			state: makeStateWith(0.5, map[string]float64{
				identity.StateVarWarmth: 0.1,
			}),
			wantApprox: 0.54,
		},
		{
			// Bias clamping: Curiosity primary=confidence(1.0), secondary=sense_of_agency(1.0)
			// bias = 0.1*(1.0-0.5) + 0.05*(1.0-0.5) = 0.05 + 0.025 = 0.075
			// Not clamped (< 0.15). adjusted = 0.7 + 0.075 = 0.775
			name:   "Curiosity: high primary+secondary → combined positive bias, not clamped",
			aspect: "Curiosity",
			raw:    0.7,
			state: makeStateWith(0.5, map[string]float64{
				identity.StateVarConfidence:    1.0,
				identity.StateVarSenseOfAgency: 1.0,
			}),
			wantApprox: 0.775,
		},
		{
			// Shadow: primary=sense_of_agency(inverse), secondary=trust_in_user(inverse)
			// agency=1.0 → primary_signed=1-1.0=0.0, trust=1.0 → secondary_signed=1-1.0=0.0
			// bias = 0.1*(0-0.5) + 0.05*(0-0.5) = -0.05 + -0.025 = -0.075
			// adjusted = 0.5 - 0.075 = 0.425
			name:   "Shadow: inverse mapping — high vars → negative bias",
			aspect: "Shadow",
			raw:    0.5,
			state: makeStateWith(0.5, map[string]float64{
				identity.StateVarSenseOfAgency: 1.0,
				identity.StateVarTrustInUser:   1.0,
			}),
			wantApprox: 0.425,
		},
		{
			// Bias clamp test: var=0.0 (primary), no secondary → primary_signed = 1.0 (if inverse) or 0.0
			// Trust: primary=trust_in_user (not inverse). var=0.0 → bias=0.1*(0-0.5)= -0.05
			// Below clamp threshold of ±0.15 — not clamped at this single-var contribution.
			// raw=0.1, adjusted = 0.1 - 0.05 = 0.05
			name:   "Trust: very low trust_in_user → negative bias on alignment",
			aspect: "Trust",
			raw:    0.1,
			state: makeStateWith(0.5, map[string]float64{
				identity.StateVarTrustInUser: 0.0,
			}),
			wantApprox: 0.05,
		},
		{
			// Clamp test: extreme values push toward ±0.15 limit.
			// Joy: groundedness=1.0, warmth=1.0
			// primary_signed=1.0, secondary_signed=1.0
			// bias = 0.1*(1.0-0.5) + 0.05*(1.0-0.5) = 0.05 + 0.025 = 0.075 (not clamped)
			// But with raw=1.0, adjusted = 1.0 (clamped at upper bound)
			name:       "output clamp: raw+bias > 1.0 → clamped to 1.0",
			aspect:     "Joy",
			raw:        1.0,
			state:      makeStateWith(1.0, nil),
			wantApprox: 1.0,
			wantExact:  true,
		},
		{
			// Fear with fully inverse vars at high values: groundedness=1.0, confidence=1.0
			// primary_signed = 1-1.0 = 0.0 → 0.1*(0-0.5)= -0.05
			// secondary_signed = 1-1.0 = 0.0 → 0.05*(0-0.5) = -0.025
			// bias = -0.075 (not clamped)
			// raw=0.0 → adjusted = 0.0 (clamped at lower bound)
			name:       "output clamp: raw+bias < 0.0 → clamped to 0.0",
			aspect:     "Fear",
			raw:        0.0,
			state:      makeStateWith(1.0, nil),
			wantApprox: 0.0,
			wantExact:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyStateBias(tt.aspect, tt.raw, tt.state)
			if tt.wantExact {
				if got != tt.wantApprox {
					t.Errorf("ApplyStateBias(%q, %.4f, state) = %.6f; want exactly %.6f",
						tt.aspect, tt.raw, got, tt.wantApprox)
				}
			} else {
				if !approxEqual(got, tt.wantApprox, 1e-9) {
					t.Errorf("ApplyStateBias(%q, %.4f, state) = %.6f; want %.6f (tol 1e-9)",
						tt.aspect, tt.raw, got, tt.wantApprox)
				}
			}
		})
	}
}

// TestPairingSemantics verifies the Joy/Fear pairing behavior explicitly:
// same state var (groundedness), opposite valences → opposite bias directions.
func TestPairingSemantics(t *testing.T) {
	highGround := makeStateWith(0.5, map[string]float64{
		identity.StateVarGroundedness: 0.8,
	})
	raw := 0.7

	joyBiased := ApplyStateBias("Joy", raw, highGround)
	fearBiased := ApplyStateBias("Fear", raw, highGround)

	if joyBiased <= raw {
		t.Errorf("Joy with high groundedness should boost alignment: got %.4f, raw %.4f", joyBiased, raw)
	}
	if fearBiased >= raw {
		t.Errorf("Fear with high groundedness should reduce alignment: got %.4f, raw %.4f", fearBiased, raw)
	}

	joyDelta := joyBiased - raw
	fearDelta := fearBiased - raw
	// The deltas should be equal in magnitude and opposite in sign
	// (both have the same 0.1×groundedness primary coefficient, no secondary contribution from neutral).
	if !approxEqual(joyDelta, -fearDelta, 1e-9) {
		t.Errorf("Joy delta (%.4f) and Fear delta (%.4f) should be equal-and-opposite", joyDelta, fearDelta)
	}
}
