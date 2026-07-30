// Package router holds the candidate predictive routers behind one interface,
// plus trivial baselines. Every router turns prompt features (and optionally an
// embedding) into an escalation score in [0,1] — higher means "local will
// likely be inadequate, prefer the frontier" — and a threshold turns that into
// a decision.
//
// Breadth over polish: the point is to compare several principled designs on
// the same structured dataset. Each router documents the data SHAPE it needs
// (pointwise vs pairwise) and how it derives one from the other.
package router

import (
	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// Instance is a single prompt to route.
type Instance struct {
	Features  schema.Features
	Embedding []float32
}

// Vec returns the dense structural feature vector for linear models.
func (i Instance) Vec() []float64 { return feature.Vector(i.Features) }

// InstanceFromPointwise / FromGold adapt dataset rows into routing instances.
func InstanceFromPointwise(r schema.PointwiseRow) Instance {
	return Instance{Features: r.Features, Embedding: r.Embedding}
}
func InstanceFromGold(r schema.GoldRow) Instance {
	return Instance{Features: r.Features, Embedding: r.Embedding}
}

// TrainData is everything a router may consume. Each router uses the subset it
// needs. Rows may carry mixed label sources; routers that train pick a source.
type TrainData struct {
	Pointwise     []schema.PointwiseRow
	Pairwise      []schema.PairwiseRow
	LocalModels   []string
	FrontierModel string
	// TrainSource is the label source routers should train on (the harness sets
	// this so eval can enforce a strictly-stronger eval source).
	TrainSource schema.LabelSource
}

// Router is the common interface.
type Router interface {
	// Fit trains on the dataset. Training-free routers may just store data.
	Fit(d TrainData) error
	// Score returns P(escalate) in [0,1]; higher = local more likely inadequate.
	Score(inst Instance) float64
	// Decide returns true to escalate to the frontier, given a threshold.
	Decide(inst Instance, threshold float64) bool
	// Name identifies the router.
	Name() string
}

// pointwiseForSource filters pointwise rows to a single label source (so
// training doesn't mix, e.g., implicit and judge labels).
func pointwiseForSource(d TrainData) []schema.PointwiseRow {
	if d.TrainSource == "" {
		return d.Pointwise
	}
	var out []schema.PointwiseRow
	for _, r := range d.Pointwise {
		if r.LabelSource == d.TrainSource {
			out = append(out, r)
		}
	}
	return out
}

func decideAt(score, threshold float64) bool { return score >= threshold }

// ---- trivial baselines (eval anchors) ----

// AlwaysLocal never escalates (score 0). Lower bound on cost, upper bound on
// under-escalation.
type AlwaysLocal struct{}

func (AlwaysLocal) Fit(TrainData) error           { return nil }
func (AlwaysLocal) Score(Instance) float64        { return 0 }
func (AlwaysLocal) Decide(Instance, float64) bool { return false }
func (AlwaysLocal) Name() string                  { return "always-local" }

// AlwaysFrontier always escalates (score 1). Upper bound on cost and quality.
type AlwaysFrontier struct{}

func (AlwaysFrontier) Fit(TrainData) error           { return nil }
func (AlwaysFrontier) Score(Instance) float64        { return 1 }
func (AlwaysFrontier) Decide(Instance, float64) bool { return true }
func (AlwaysFrontier) Name() string                  { return "always-frontier" }
