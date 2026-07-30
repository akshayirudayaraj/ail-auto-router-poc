package router

import (
	"math"

	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/numerics"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// IRT is a 1-parameter-logistic (Rasch) item-response router:
//
//	P(success | model m, prompt i) = sigmoid(theta_m - b_i)
//	b_i = w·features_i + c      (difficulty is a linear function of features)
//
// theta_m are per-model abilities. Parameters are fit jointly by MLE (gradient
// descent on the weighted negative log-likelihood) over all logged (m, i)
// outcomes. This is exactly the generative model behind the synthetic logs, so
// recovery of the planted theta/b is checkable (see irt_test.go).
//
// DATA SHAPE: pointwise (model, prompt, outcome). Routing score for a prompt is
// the estimated probability the LOCAL rung is inadequate:
// Score = 1 - sigmoid(theta_localRef - b_i).
//
// IDENTIFIABILITY: theta of the reference model (index 0 in ModelIndex, which
// the harness sets to the local reference) is pinned to 0.
type IRT struct {
	w        []float64          // feature weights for difficulty
	c        float64            // difficulty intercept
	theta    map[string]float64 // per-model ability
	refModel string             // pinned theta=0
	localRef string             // model whose adequacy the routing score uses
	std      numerics.Standardizer
	fit      bool
}

func NewIRT() *IRT { return &IRT{theta: map[string]float64{}} }

func (m *IRT) Name() string { return "irt-1pl" }

// IRTParams controls the joint MLE fit.
type IRTParams struct {
	LR     float64
	L2     float64
	Epochs int
}

func defaultIRTParams() IRTParams { return IRTParams{LR: 0.05, L2: 1e-3, Epochs: 800} }

func (m *IRT) Fit(d TrainData) error {
	rows := pointwiseForSource(d)
	if len(rows) < 8 {
		return nil // leave unfit -> prior score
	}
	// reference + local reference models
	m.refModel = d.LocalModels[0]
	m.localRef = d.LocalModels[0]

	// standardize features so difficulty weights are well-scaled
	raw := make([][]float64, len(rows))
	for i, r := range rows {
		raw[i] = feature.Vector(r.Features)
	}
	m.std = numerics.FitStandardizer(raw)
	X := make([][]float64, len(rows))
	for i := range raw {
		X[i] = m.std.Transform(raw[i])
	}
	dfeat := len(X[0])
	m.w = make([]float64, dfeat)
	m.c = 0
	for _, r := range rows {
		if _, ok := m.theta[r.Model]; !ok {
			m.theta[r.Model] = 0
		}
	}

	p := defaultIRTParams()
	gw := make([]float64, dfeat)
	for epoch := 0; epoch < p.Epochs; epoch++ {
		for j := range gw {
			gw[j] = 0
		}
		gc := 0.0
		gtheta := map[string]float64{}
		var sumW float64
		for i, r := range rows {
			b := m.c + numerics.Dot(m.w, X[i])
			z := m.theta[r.Model] - b
			pr := numerics.Sigmoid(z)
			wt := r.LabelConfidence
			if wt <= 0 {
				wt = 1
			}
			sumW += wt
			// d/dz of NLL = (p - y); z = theta - (c + w·x)
			g := (pr - float64(r.Outcome)) * wt
			// theta gradient (+g), pinned ref excluded
			if r.Model != m.refModel {
				gtheta[r.Model] += g
			}
			// b appears with -1 in z, so w,c gradients get -g * dx
			gc += -g
			for j := range m.w {
				gw[j] += -g * X[i][j]
			}
		}
		if sumW == 0 {
			sumW = 1
		}
		// apply updates (mean gradient + L2)
		m.c -= p.LR * (gc / sumW)
		for j := range m.w {
			m.w[j] -= p.LR * (gw[j]/sumW + p.L2*m.w[j])
		}
		for mdl := range m.theta {
			if mdl == m.refModel {
				m.theta[mdl] = 0
				continue
			}
			g := gtheta[mdl] / sumW
			m.theta[mdl] -= p.LR * (g + p.L2*m.theta[mdl])
		}
	}
	m.fit = true
	return nil
}

// difficulty returns b_i for an instance.
func (m *IRT) difficulty(inst Instance) float64 {
	x := m.std.Transform(feature.Vector(inst.Features))
	return m.c + numerics.Dot(m.w, x)
}

// PSuccess returns the estimated P(success | model, prompt).
func (m *IRT) PSuccess(model string, inst Instance) float64 {
	theta := m.theta[model]
	return numerics.Sigmoid(theta - m.difficulty(inst))
}

func (m *IRT) Score(inst Instance) float64 {
	if !m.fit {
		return inst.Features.HardKeywordScore
	}
	// P(local inadequate) = 1 - P(success | local ref)
	return 1 - m.PSuccess(m.localRef, inst)
}

func (m *IRT) Decide(inst Instance, threshold float64) bool {
	return decideAt(m.Score(inst), threshold)
}

// Abilities exposes the fitted theta map (for reports and recovery tests).
func (m *IRT) Abilities() map[string]float64 {
	out := map[string]float64{}
	for k, v := range m.theta {
		out[k] = v
	}
	return out
}

// OnboardModel estimates a single ability for a NEW model without refitting the
// difficulties: it freezes b_i and Newton-solves the 1-D MLE for theta on an
// anchor set of that model's observed outcomes. This is the cheap
// new-model-onboarding path.
func (m *IRT) OnboardModel(model string, anchors []schema.PointwiseRow) float64 {
	if !m.fit || len(anchors) == 0 {
		return 0
	}
	// score' wrt theta: sum_i (y_i - sigmoid(theta - b_i)) = 0
	// derivative: -sum_i sigmoid'(.) = -sum_i p(1-p)
	f := func(theta float64) float64 {
		var s float64
		for _, r := range anchors {
			b := m.difficulty(Instance{Features: r.Features, Embedding: r.Embedding})
			p := numerics.Sigmoid(theta - b)
			s += float64(r.Outcome) - p
		}
		return s
	}
	fp := func(theta float64) float64 {
		var s float64
		for _, r := range anchors {
			b := m.difficulty(Instance{Features: r.Features, Embedding: r.Embedding})
			p := numerics.Sigmoid(theta - b)
			s += -p * (1 - p)
		}
		if math.Abs(s) < 1e-9 {
			s = -1e-9
		}
		return s
	}
	theta := numerics.Newton(f, fp, 0, 50, 1e-8)
	m.theta[model] = theta
	return theta
}

var _ Router = (*IRT)(nil)
