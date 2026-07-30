package eval

import (
	"fmt"

	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// OffPolicy estimates the reward a candidate router (target policy) WOULD earn
// if deployed, from logs alone, using logged propensities — via Inverse
// Propensity Scoring (IPS) and a Doubly-Robust (DR) estimator whose outcome
// model Q is a fitted IRT.
//
// It REFUSES (returns an error) if the logs lack stochastic-policy propensities
// or overlap: a deterministic log cannot support counterfactual estimation.
// The synthetic log generator's epsilon-greedy policy records the propensities
// that make this path work.
//
// The target policy induced by a router: escalate -> frontier, else the base
// local model. IPS reweights logged rows where the logged action matches the
// target action; DR corrects with the Q-model.
type OffPolicy struct {
	Threshold      float64
	PropensityClip float64 // clip propensities to [clip, 1] to bound variance
	MinCoverage    float64 // min fraction of rows with usable propensity
}

func NewOffPolicy() *OffPolicy {
	return &OffPolicy{Threshold: 0.5, PropensityClip: 0.02, MinCoverage: 0.5}
}

func (o *OffPolicy) Name() string { return "off-policy-ips-dr" }

func (o *OffPolicy) Run(routers []router.Router, d Data) (Report, error) {
	rep := Report{Method: o.Name()}

	// Use implicit-source rows as the logged (action, reward) tuples.
	var log []schema.PointwiseRow
	for _, r := range d.Pointwise {
		if r.LabelSource == schema.LabelImplicit {
			log = append(log, r)
		}
	}
	if len(log) == 0 {
		return rep, fmt.Errorf("off-policy: no implicit-labeled log rows")
	}

	// --- REFUSAL guardrail: require stochastic propensities with overlap ---
	var withProp, stochastic int
	for _, r := range log {
		if r.Propensity != nil {
			withProp++
			if *r.Propensity > 0 && *r.Propensity < 0.999 {
				stochastic++
			}
		}
	}
	cov := float64(withProp) / float64(len(log))
	if cov < o.MinCoverage {
		return rep, fmt.Errorf("off-policy REFUSED: only %.0f%% of log rows carry propensities (need >=%.0f%%); "+
			"deterministic logs cannot support counterfactual estimation", cov*100, o.MinCoverage*100)
	}
	if stochastic == 0 {
		return rep, fmt.Errorf("off-policy REFUSED: no stochastic propensities (all deterministic); no overlap to reweight over")
	}

	// --- Q-model for DR: a fitted IRT over the implicit rows ---
	q := router.NewIRT()
	if err := q.Fit(router.TrainData{
		Pointwise: log, LocalModels: d.Cfg.LocalModels, FrontierModel: d.Cfg.FrontierModel,
		TrainSource: schema.LabelImplicit,
	}); err != nil {
		return rep, fmt.Errorf("off-policy Q fit: %w", err)
	}
	baseLocal := d.Cfg.LocalModels[0]
	frontier := d.Cfg.FrontierModel

	// logging-policy observed average reward (on-policy baseline)
	var logReward float64
	for _, r := range log {
		logReward += float64(r.Outcome)
	}
	logReward /= float64(len(log))

	for _, rt := range routers {
		if err := rt.Fit(TrainDataFrom(d, schema.LabelImplicit)); err != nil {
			return rep, fmt.Errorf("fit %s: %w", rt.Name(), err)
		}
		var vIPS, vDR, sumW, sumW2 float64
		n := float64(len(log))
		for _, r := range log {
			inst := router.InstanceFromPointwise(r)
			target := baseLocal
			if rt.Decide(inst, o.Threshold) {
				target = frontier
			}
			// Q predictions
			qTarget := q.PSuccess(target, inst)
			qLogged := q.PSuccess(r.Model, inst)

			p := 1.0
			if r.Propensity != nil {
				p = *r.Propensity
			}
			if p < o.PropensityClip {
				p = o.PropensityClip
			}
			ind := 0.0
			if r.Model == target {
				ind = 1.0
			}
			w := ind / p
			sumW += w
			sumW2 += w * w
			vIPS += w * float64(r.Outcome)
			vDR += qTarget + w*(float64(r.Outcome)-qLogged)
		}
		vIPS /= n
		vDR /= n
		// effective sample size of the importance weights
		ess := 0.0
		if sumW2 > 0 {
			ess = sumW * sumW / sumW2
		}
		rep.Rows = append(rep.Rows, ReportRow{
			Router: rt.Name(),
			Metrics: map[string]float64{
				"v_ips":     vIPS,
				"v_dr":      vDR,
				"ess":       ess,
				"uplift_dr": vDR - logReward,
			},
		})
	}

	rep.Detail = map[string]any{
		"log_rows":        len(log),
		"propensity_cov":  cov,
		"stochastic_rows": stochastic,
		"logging_reward":  logReward,
		"q_model":         "irt-1pl",
	}
	rep.Notes = append(rep.Notes,
		fmt.Sprintf("Logging-policy observed reward = %.3f (on-policy baseline). uplift_dr = V_DR - baseline.", logReward),
		"V_IPS is unbiased but high-variance (watch ESS); V_DR uses the IRT Q-model to cut variance.",
		"Rewards are the implicit outcome labels, so estimates inherit that label's noise (documented).",
	)
	return rep, nil
}

var _ EvalMethod = (*OffPolicy)(nil)
