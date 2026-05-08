package db

// ScoredEmbedding is the input type for MMR reranking.
type ScoredEmbedding struct {
	Score     float32
	Embedding []float32
	Index     int // position in original slice; callers use this to map back
}

// MMR returns indices into candidates in MMR-diversified order (up to k).
// lambda weights relevance vs. diversity: 1.0 = pure relevance, 0.0 = pure diversity.
// Candidates whose cosine similarity to any already-selected item exceeds
// collapseThreshold are dropped permanently before the pick loop.
func MMR(candidates []ScoredEmbedding, k int, lambda, collapseThreshold float32) []int {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) <= k {
		// No selection needed — return all sorted by score descending.
		order := make([]int, len(candidates))
		for i := range order {
			order[i] = i
		}
		sortByScore(candidates, order)
		result := make([]int, len(order))
		for i, o := range order {
			result[i] = candidates[o].Index
		}
		return result
	}

	remaining := make([]int, len(candidates)) // indices into candidates
	for i := range remaining {
		remaining[i] = i
	}

	// Seed: pick highest-scoring candidate.
	best := 0
	for i := 1; i < len(remaining); i++ {
		if candidates[remaining[i]].Score > candidates[remaining[best]].Score {
			best = i
		}
	}
	selected := []int{remaining[best]}
	remaining = append(remaining[:best], remaining[best+1:]...)

	result := []int{candidates[selected[0]].Index}

	for len(result) < k && len(remaining) > 0 {
		bestIdx := -1
		bestMMR := float32(-1e9)

		nextRemaining := remaining[:0]
		for _, ri := range remaining {
			maxSim := maxCosineToSelected(candidates[ri].Embedding, candidates, selected)
			if maxSim > collapseThreshold {
				continue // collapsed — drop permanently
			}
			mmrScore := lambda*candidates[ri].Score - (1-lambda)*maxSim
			nextRemaining = append(nextRemaining, ri)
			if bestIdx == -1 || mmrScore > bestMMR {
				bestMMR = mmrScore
				bestIdx = ri
			}
		}
		remaining = nextRemaining

		if bestIdx == -1 {
			break
		}

		selected = append(selected, bestIdx)
		result = append(result, candidates[bestIdx].Index)

		// Remove bestIdx from remaining.
		for i, ri := range remaining {
			if ri == bestIdx {
				remaining = append(remaining[:i], remaining[i+1:]...)
				break
			}
		}
	}

	return result
}

func maxCosineToSelected(emb []float32, candidates []ScoredEmbedding, selected []int) float32 {
	var max float32
	for _, si := range selected {
		sim := Cosine(emb, candidates[si].Embedding)
		if sim > max {
			max = sim
		}
	}
	return max
}

// sortByScore sorts order indices by candidates[i].Score descending (insertion sort; k is small).
func sortByScore(candidates []ScoredEmbedding, order []int) {
	for i := 1; i < len(order); i++ {
		key := order[i]
		j := i - 1
		for j >= 0 && candidates[order[j]].Score < candidates[key].Score {
			order[j+1] = order[j]
			j--
		}
		order[j+1] = key
	}
}
