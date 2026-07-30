package router

import (
	"sort"

	"github.com/akshayirudayaraj/ail-routing-test/internal/numerics"
)

// KNN is a training-free, drift-friendly router: it stores labeled local-served
// prompts and, at decision time, estimates P(local inadequate) as the
// cosine-weighted vote of the k nearest neighbors' outcomes.
//
// DATA SHAPE: pointwise. Only LOCAL-served rows are indexed, because the
// question is "will the local rung suffice here?". If a prompt has no embedding
// or no local neighbors, it falls back to a feature-based prior.
type KNN struct {
	K         int
	embs      [][]float32
	inadeq    []float64 // 1 - outcome, aligned with embs
	weight    []float64 // label confidence
	fallbackK int
}

func NewKNN(k int) *KNN {
	if k <= 0 {
		k = 15
	}
	return &KNN{K: k}
}

func (r *KNN) Name() string { return "knn" }

func (r *KNN) Fit(d TrainData) error {
	isFrontier := frontierPred(d)
	r.embs = nil
	r.inadeq = nil
	r.weight = nil
	for _, row := range pointwiseForSource(d) {
		if isFrontier(row.Model) || len(row.Embedding) == 0 {
			continue
		}
		r.embs = append(r.embs, row.Embedding)
		r.inadeq = append(r.inadeq, float64(1-row.Outcome))
		w := row.LabelConfidence
		if w <= 0 {
			w = 0.5
		}
		r.weight = append(r.weight, w)
	}
	return nil
}

type neighbor struct {
	sim float64
	idx int
}

func (r *KNN) Score(inst Instance) float64 {
	if len(inst.Embedding) == 0 || len(r.embs) == 0 {
		return inst.Features.HardKeywordScore // prior fallback
	}
	nb := make([]neighbor, 0, len(r.embs))
	for i, e := range r.embs {
		nb = append(nb, neighbor{sim: numerics.Cosine(inst.Embedding, e), idx: i})
	}
	sort.Slice(nb, func(i, j int) bool { return nb[i].sim > nb[j].sim })
	k := r.K
	if k > len(nb) {
		k = len(nb)
	}
	var num, den float64
	for _, n := range nb[:k] {
		// weight by similarity (clamped >=0) and label confidence
		s := n.sim
		if s < 0 {
			s = 0
		}
		wgt := s * r.weight[n.idx]
		num += wgt * r.inadeq[n.idx]
		den += wgt
	}
	if den == 0 {
		return inst.Features.HardKeywordScore
	}
	return num / den
}

func (r *KNN) Decide(inst Instance, threshold float64) bool {
	return decideAt(r.Score(inst), threshold)
}

var _ Router = (*KNN)(nil)
