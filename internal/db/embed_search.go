package db

import (
	"sort"
	"time"
)

// Embeddable is implemented by any type that carries an embedding vector.
type Embeddable interface {
	GetEmbedding() []float32
	GetEmbedModel() string
	GetImportance() float64
	GetUpdatedAt() time.Time
}

// SearchTopK scores items against query by cosine × decayImportance,
// returns the top-k results. Skips items with mismatched model or dimension.
func SearchTopK[T Embeddable](items []T, query []float32, queryModel string, k int) []T {
	type scored struct {
		item  T
		score float32
	}
	var candidates []scored
	for _, item := range items {
		emb := item.GetEmbedding()
		if len(emb) == 0 || item.GetEmbedModel() != queryModel || len(emb) != len(query) {
			continue
		}
		score := cosine(query, emb) * float32(decayImportance(item.GetImportance(), item.GetUpdatedAt()))
		candidates = append(candidates, scored{item, score})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	if k > len(candidates) {
		k = len(candidates)
	}
	out := make([]T, k)
	for i := range out {
		out[i] = candidates[i].item
	}
	return out
}
