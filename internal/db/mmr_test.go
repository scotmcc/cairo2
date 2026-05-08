package db

import (
	"math"
	"testing"
)

// unitVec returns a unit vector of length n with v[0]=1.0 (all others 0).
func unitVec(n int) []float32 {
	v := make([]float32, n)
	v[0] = 1.0
	return v
}

// rotatedVec returns a unit vector at angle radians from unitVec in the 0-1 plane.
func rotatedVec(n int, angle float64) []float32 {
	v := make([]float32, n)
	v[0] = float32(math.Cos(angle))
	v[1] = float32(math.Sin(angle))
	return v
}

// nearDup returns a vector very close to base, with a tiny perturbation on dim d.
func nearDup(base []float32, d int, delta float32) []float32 {
	v := make([]float32, len(base))
	copy(v, base)
	v[d] += delta
	// Renormalize.
	var norm float32
	for _, x := range v {
		norm += x * x
	}
	norm = float32(math.Sqrt(float64(norm)))
	for i := range v {
		v[i] /= norm
	}
	return v
}

// TestMMR_DiversityUnderDuplicateCluster verifies that 5 near-duplicates (cosine ≥ 0.97)
// yield at most 1 selected from that cluster when k=3.
func TestMMR_DiversityUnderDuplicateCluster(t *testing.T) {
	const dims = 64
	base := unitVec(dims)

	candidates := make([]ScoredEmbedding, 10)
	// Indices 0-4: near-duplicates of base (cosine ~0.999).
	for i := 0; i < 5; i++ {
		candidates[i] = ScoredEmbedding{
			Score:     0.9 - float32(i)*0.01, // slightly descending scores
			Embedding: nearDup(base, 1, float32(i+1)*0.001),
			Index:     i,
		}
	}
	// Indices 5-9: distinct vectors spread across the space.
	angles := []float64{math.Pi / 4, math.Pi / 2, 3 * math.Pi / 4, math.Pi, 5 * math.Pi / 4}
	for i, angle := range angles {
		candidates[5+i] = ScoredEmbedding{
			Score:     0.8 - float32(i)*0.01,
			Embedding: rotatedVec(dims, angle),
			Index:     5 + i,
		}
	}

	selected := MMR(candidates, 3, 0.7, 0.92)

	if len(selected) != 3 {
		t.Fatalf("expected 3 selected, got %d", len(selected))
	}

	dupCount := 0
	for _, idx := range selected {
		if idx < 5 {
			dupCount++
		}
	}
	if dupCount > 1 {
		t.Errorf("expected at most 1 from duplicate cluster, got %d (selected: %v)", dupCount, selected)
	}
}

// TestMMR_ExactK verifies no panic when len(candidates)==k.
func TestMMR_ExactK(t *testing.T) {
	const dims = 8
	candidates := []ScoredEmbedding{
		{Score: 0.9, Embedding: rotatedVec(dims, 0), Index: 0},
		{Score: 0.8, Embedding: rotatedVec(dims, math.Pi/2), Index: 1},
		{Score: 0.7, Embedding: rotatedVec(dims, math.Pi), Index: 2},
	}
	selected := MMR(candidates, 3, 0.7, 0.92)
	if len(selected) != 3 {
		t.Fatalf("expected 3, got %d", len(selected))
	}
}

// TestMMR_Empty verifies empty input returns nil without panic.
func TestMMR_Empty(t *testing.T) {
	selected := MMR(nil, 5, 0.7, 0.92)
	if selected != nil {
		t.Errorf("expected nil, got %v", selected)
	}
	selected = MMR([]ScoredEmbedding{}, 5, 0.7, 0.92)
	if selected != nil {
		t.Errorf("expected nil for empty slice, got %v", selected)
	}
}
