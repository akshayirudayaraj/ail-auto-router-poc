package eval

import (
	"fmt"

	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// GoldEval is the dual-arm gold benchmark: BOTH arms' outcomes are known, so it
// is free of the censoring that plagues observational logs and yields the only
// trustworthy ABSOLUTE cost/quality numbers. It reports the RouterBench-style
// cost/quality curve, its convex hull, AIQ, and the under-/over-escalation
// cells, plus AUC/ECE of the escalation score against "local inadequate".
type GoldEval struct {
	// Threshold is the operating point at which per-router escalation/quality/
	// cost/cells are reported (AIQ is threshold-independent).
	Threshold float64
	// TrainSource is the label source routers are fit on; gold outcomes are a
	// strictly stronger source (judge/executed), so there is no circularity.
	TrainSource schema.LabelSource
}

func NewGoldEval() *GoldEval {
	return &GoldEval{Threshold: 0.5, TrainSource: schema.LabelImplicit}
}

func (g *GoldEval) Name() string { return "dual-arm-gold" }

// goldDetail is the per-router structured detail saved to JSON.
type goldDetail struct {
	Router string    `json:"router"`
	Curve  []CQPoint `json:"curve"`
	Hull   []CQPoint `json:"hull"`
}

func (g *GoldEval) Run(routers []router.Router, d Data) (Report, error) {
	rep := Report{Method: g.Name()}
	if len(d.Gold) == 0 {
		return rep, fmt.Errorf("gold eval: empty gold set")
	}
	td := TrainDataFrom(d, g.TrainSource)

	// "needs frontier" label = local rung inadequate on this prompt.
	labels := make([]int, len(d.Gold))
	for i, row := range d.Gold {
		if row.OutcomeLocal == 0 {
			labels[i] = 1
		}
	}

	var details []goldDetail
	for _, r := range routers {
		if err := r.Fit(td); err != nil {
			return rep, fmt.Errorf("fit %s: %w", r.Name(), err)
		}
		scores := scoreGold(r, d.Gold)
		curve := CostQualityCurve(scores, d.Gold)
		hull := UpperHull(curve)
		aiq := AIQ(curve)
		op := Operating(scores, d.Gold, g.Threshold)

		rep.Rows = append(rep.Rows, ReportRow{
			Router: r.Name(),
			Metrics: map[string]float64{
				"auc":               AUC(scores, labels),
				"ece":               ECE(scores, labels, 10),
				"aiq":               aiq,
				"escalation@thr":    op.EscalationRate,
				"quality@thr":       op.Quality,
				"qual_retention":    op.QualityRetention,
				"cost_vs_local":     op.CostVsLocal,
				"under_escal_cellB": op.Cells.UnderEscalation,
				"over_escalation":   op.Cells.OverEscalation,
			},
		})
		details = append(details, goldDetail{Router: r.Name(), Curve: curve, Hull: hull})
	}
	rep.Detail = details
	rep.Notes = append(rep.Notes,
		fmt.Sprintf("Operating threshold = %.2f. AIQ is threshold-independent (area under the cost/quality hull).", g.Threshold),
		"cell-B (under_escal) = stayed local but frontier would have passed — the costly miss.",
		"Gold outcomes are a strictly stronger label source than the training labels (no circularity).",
		"Only the gold set (and later online A/B) give trustworthy ABSOLUTE numbers.",
	)
	return rep, nil
}

var _ EvalMethod = (*GoldEval)(nil)
