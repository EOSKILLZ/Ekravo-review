package repocontext

// pageRank runs a personalized PageRank over the file graph. personalization
// carries mass on the PR's changed files; a small uniform floor keeps an
// isolated but important entry point from disappearing.
func (ix *Index) pageRank(personalization map[int]float64, iterations int) []float64 {
	n := len(ix.Files)
	if n == 0 {
		return nil
	}
	if iterations <= 0 {
		iterations = 20
	}
	const damping = 0.85

	personal := make([]float64, n)
	total := 0.0
	for i, v := range personalization {
		if i >= 0 && i < n && v > 0 {
			personal[i] += v
			total += v
		}
	}
	if total == 0 {
		for i := range personal {
			personal[i] = 1 / float64(n)
		}
	} else {
		for i := range personal {
			personal[i] = 0.9*personal[i]/total + 0.1/float64(n)
		}
	}

	rank := append([]float64(nil), personal...)
	next := make([]float64, n)
	for it := 0; it < iterations; it++ {
		for i := range next {
			next[i] = 0
		}
		dangling := 0.0
		for i := 0; i < n; i++ {
			out := ix.graph[i]
			if len(out) == 0 {
				dangling += rank[i]
				continue
			}
			sum := 0.0
			for _, w := range out {
				sum += w
			}
			if sum <= 0 {
				dangling += rank[i]
				continue
			}
			for j, w := range out {
				if j < n {
					next[j] += damping * rank[i] * w / sum
				}
			}
		}
		for i := 0; i < n; i++ {
			next[i] += (1-damping)*personal[i] + damping*dangling*personal[i]
		}
		rank, next = next, rank
	}
	return rank
}
