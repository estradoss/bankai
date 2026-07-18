package memory

import (
	"sort"
	"strings"
)

// DuplicateCluster is a group of memories whose bodies are highly similar —
// candidates for consolidation ("dream").
type DuplicateCluster struct {
	Names      []string
	Similarity float64 // representative pairwise similarity within the cluster
}

// jaccard returns the token-set Jaccard similarity of two strings.
func jaccard(a, b string) float64 {
	ta, tb := tokenize(a), tokenize(b)
	if len(ta) == 0 && len(tb) == 0 {
		return 0
	}
	inter := 0
	for w := range ta {
		if _, ok := tb[w]; ok {
			inter++
		}
	}
	union := len(ta) + len(tb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// Consolidate clusters memories whose bodies are at least `threshold` similar
// (Jaccard, 0..1). It is read-only — the "dream" analysis proposes merges rather
// than performing them, so the user stays in control. Ported from vibelearn's
// dream/consolidation service (offline portion).
func (s *Store) Consolidate(threshold float64) []DuplicateCluster {
	mems := s.List()
	n := len(mems)
	if n < 2 {
		return nil
	}
	// Union-find over memories linked by above-threshold similarity.
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	best := map[int]float64{}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			sim := jaccard(mems[i].Body, mems[j].Body)
			if sim >= threshold {
				ri, rj := find(i), find(j)
				if ri != rj {
					parent[ri] = rj
				}
				root := find(i)
				if sim > best[root] {
					best[root] = sim
				}
			}
		}
	}
	groups := map[int][]string{}
	for i := 0; i < n; i++ {
		r := find(i)
		groups[r] = append(groups[r], mems[i].Name)
	}
	var out []DuplicateCluster
	for r, names := range groups {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		out = append(out, DuplicateCluster{Names: names, Similarity: best[r]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity != out[j].Similarity {
			return out[i].Similarity > out[j].Similarity
		}
		return strings.Join(out[i].Names, ",") < strings.Join(out[j].Names, ",")
	})
	return out
}
