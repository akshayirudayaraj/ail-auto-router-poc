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

// GoldReportDetail is the structured Detail attached to the gold report: the
// per-router cost/quality curves plus the reference anchors (always-local,
// oracle, always-frontier) that bound the achievable region.
type GoldReportDetail struct {
	Curves  []goldDetail `json:"curves"`
	Anchors []Baseline   `json:"anchors"`
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

	anchors, oracleLocalShare := GoldBaselines(d.Gold)

	var details []goldDetail
	for _, r := range routers {
		if err := r.Fit(td); err != nil {
			return rep, fmt.Errorf("fit %s: %w", r.Name(), err)
		}
		scores := scoreGold(r, d.Gold)
		curve := CostQualityCurve(scores, d.Gold)
		hull := UpperHull(curve)
		aiq := AIQ(curve)
		// Operate at the QUALITY-CALIBRATED threshold: the highest (most-local,
		// cheapest) threshold whose quality retention is still ≥ 100% of always-
		// frontier. This is CalibrateForQuality at target=1.0 — the same tuned
		// isoquality point offload_isoq advertises — so local_share, thrift, safety
		// and the scorecard all report that one operating point. If 100% quality is
		// unreachable without escalating everything, CalibrateForQuality returns the
		// always-escalate threshold (local_share → 0), which is the honest answer.
		offload, _, _ := IsoQualityOffload(curve)
		const qualityTarget = 1.0 // hold 100% of always-frontier quality (POC goal)
		opThr := CalibrateForQuality(scores, d.Gold, qualityTarget)
		op := Operating(scores, d.Gold, opThr)
		localShare := 1 - op.EscalationRate
		savingsCapture := 0.0
		if oracleLocalShare > 0 {
			savingsCapture = localShare / oracleLocalShare
		}

		rep.Rows = append(rep.Rows, ReportRow{
			Router: r.Name(),
			Metrics: map[string]float64{
				// headline scorecard for this POC: keep as much local as possible
				// WITHOUT losing quality vs always-frontier.
				"local_share@thr": localShare,
				"qual_retention":  op.QualityRetention,
				// why it works: caught the hard ones (safety), kept the easy ones
				// local (thrift), and how close to the oracle's safe offload.
				"safety":            op.Safety,
				"thrift":            op.Thrift,
				"savings_capture":   savingsCapture,
				"under_escal_cellB": op.Cells.UnderEscalation,
				// secondary / diagnostic
				"offload_isoq":    offload, // max local share while matching frontier quality
				"qual_cal_thr":    opThr,   // CalibrateForQuality@100%: the operating threshold above
				"escalation@thr":  op.EscalationRate,
				"quality@thr":     op.Quality,
				"over_escalation": op.Cells.OverEscalation,
				"aiq":             aiq,
				"auc":             AUC(scores, labels),
				"ece":             ECE(scores, labels, 10),
				"cost_vs_local":   op.CostVsLocal,
			},
		})
		details = append(details, goldDetail{Router: r.Name(), Curve: curve, Hull: hull})
	}
	rep.Detail = GoldReportDetail{Curves: details, Anchors: anchors}
	rep.Notes = append(rep.Notes,
		"Operating point is TUNED PER ROUTER by CalibrateForQuality at target=1.0: the highest (most-local, cheapest) threshold that still holds qual_retention ≥ 100% of always-frontier. Reported as qual_cal_thr. local_share@thr therefore equals offload_isoq, and thrift/safety are read at that same point.",
		"safety = of prompts where local fails, the share correctly escalated (protects quality). thrift = of prompts where local passes, the share kept local (captures savings).",
		"savings_capture = local share as a fraction of the oracle's (a perfect router escalates iff local would fail). cell-B (under_escal) = stayed local but frontier would have passed — the quality leak to minimize.",
		"Anchors: always-local / oracle / always-frontier bound the achievable region. AIQ (secondary) is threshold-independent.",
		"Gold outcomes are a strictly stronger label source than the training labels (no circularity). Only the gold set (and later online A/B) give trustworthy ABSOLUTE numbers.",
	)
	return rep, nil
}

var _ EvalMethod = (*GoldEval)(nil)
