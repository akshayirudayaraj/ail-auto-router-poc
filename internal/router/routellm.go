package router

import (
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/numerics"
)

// RouteLLM is a RouteLLM-style router: a logistic regression predicting
// P(frontier preferred over local) — equivalently P(local inadequate) — from
// prompt structural features.
//
// DATA SHAPE: pairwise preferences (frontier vs local). Because observational
// logs are censored (one model per prompt), we derive those pairs two ways,
// both handled in Fit:
//   - real local-vs-frontier pairwise rows (Preferred tells us who won);
//   - pointwise local-served rows as pseudo-pairs: a prompt where the local
//     rung was inadequate is a "frontier preferred" example (y=1), an adequate
//     one is "local sufficient" (y=0). This is the pointwise->pairwise
//     derivation.
//
// Features only (not the 768-d embedding): with a few hundred logged examples,
// a linear model over 10 structural features is robust and stays portable;
// embeddings are left to the kNN / encoder routers.
type RouteLLM struct {
	model numerics.LogisticRegression
	std   numerics.Standardizer
	fit   bool
}

func NewRouteLLM() *RouteLLM { return &RouteLLM{} }

func (r *RouteLLM) Name() string { return "routellm-logistic" }

func (r *RouteLLM) Fit(d TrainData) error {
	isFrontier := frontierPred(d)
	var X [][]float64
	var y []int
	var w []float64

	// (a) real pairwise rows that pit a local against the frontier
	for _, p := range d.Pairwise {
		aF, bF := isFrontier(p.ModelA), isFrontier(p.ModelB)
		if aF == bF {
			continue // need exactly one frontier side
		}
		if p.Preferred == "tie" {
			continue
		}
		frontierPreferred := (aF && p.Preferred == "a") || (bF && p.Preferred == "b")
		X = append(X, feature.Vector(p.Features))
		y = append(y, b2iR(frontierPreferred))
		w = append(w, 1.0)
	}

	// (b) pointwise local-served rows as pseudo-pairs
	for _, row := range pointwiseForSource(d) {
		if isFrontier(row.Model) {
			continue // only local-served rows tell us if local sufficed
		}
		needFrontier := row.Outcome == 0
		X = append(X, feature.Vector(row.Features))
		y = append(y, b2iR(needFrontier))
		conf := row.LabelConfidence
		if conf <= 0 {
			conf = 0.5
		}
		w = append(w, conf)
	}

	if len(X) < 4 {
		// not enough signal; leave unfit so Score falls back to a prior
		return nil
	}
	r.std = numerics.FitStandardizer(X)
	Xs := make([][]float64, len(X))
	for i := range X {
		Xs[i] = r.std.Transform(X[i])
	}
	r.model = numerics.FitLogistic(Xs, y, w, numerics.DefaultLogRegParams())
	r.fit = true
	return nil
}

func (r *RouteLLM) Score(inst Instance) float64 {
	if !r.fit {
		// unfit prior: lean on the hard-keyword difficulty signal
		return inst.Features.HardKeywordScore
	}
	return r.model.PredictProba(r.std.Transform(inst.Vec()))
}

func (r *RouteLLM) Decide(inst Instance, threshold float64) bool {
	return decideAt(r.Score(inst), threshold)
}

// frontierPred builds a frontier-model predicate from the training config.
func frontierPred(d TrainData) func(string) bool {
	front := map[string]bool{d.FrontierModel: true}
	return func(m string) bool { return front[m] || strings.HasPrefix(m, "claude") }
}

func b2iR(b bool) int {
	if b {
		return 1
	}
	return 0
}

var _ Router = (*RouteLLM)(nil)
