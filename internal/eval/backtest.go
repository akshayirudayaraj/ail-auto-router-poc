package eval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// TemporalBacktest splits the log-derived dataset by SESSION and TIME (never
// randomly — a random split leaks within-session context across the boundary)
// and evaluates how well each router ranks "the local rung was inadequate" on
// the held-out future.
//
// It enforces the label-source rule: EVAL labels must come from a STRICTLY
// STRONGER source than TRAIN labels (executed > human > judge > implicit). If
// train and eval share a source, it emits a WARNING — the numbers then measure
// self-agreement with the label heuristic, not correctness.
//
// Because logs are censored (one model per prompt), this method RANKS routers;
// it does not produce absolute cost/quality numbers (only the gold set does).
type TemporalBacktest struct {
	TrainFrac   float64 // fraction of sessions (earliest) used for training
	Threshold   float64
	TrainSource schema.LabelSource
}

func NewTemporalBacktest() *TemporalBacktest {
	return &TemporalBacktest{TrainFrac: 0.7, Threshold: 0.5, TrainSource: schema.LabelImplicit}
}

func (t *TemporalBacktest) Name() string { return "temporal-backtest" }

func sessionOf(promptID string) string {
	if i := strings.Index(promptID, "-t"); i >= 0 {
		return promptID[:i]
	}
	return promptID
}

func (t *TemporalBacktest) Run(routers []router.Router, d Data) (Report, error) {
	rep := Report{Method: t.Name()}
	if len(d.Pointwise) == 0 {
		return rep, fmt.Errorf("backtest: empty dataset")
	}

	// earliest timestamp per session
	minTS := map[string]int64{}
	for _, r := range d.Pointwise {
		if ts, ok := minTS[r.SessionID]; !ok || r.Timestamp < ts {
			minTS[r.SessionID] = r.Timestamp
		}
	}
	type st struct {
		id string
		ts int64
	}
	sess := make([]st, 0, len(minTS))
	for id, ts := range minTS {
		sess = append(sess, st{id, ts})
	}
	sort.Slice(sess, func(i, j int) bool { return sess[i].ts < sess[j].ts })
	cut := int(float64(len(sess)) * t.TrainFrac)
	if cut < 1 {
		cut = 1
	}
	if cut >= len(sess) {
		cut = len(sess) - 1
	}
	trainSet := map[string]bool{}
	for _, s := range sess[:cut] {
		trainSet[s.id] = true
	}
	splitTS := sess[cut].ts

	// train rows: train sessions, chosen train source
	var trainRows []schema.PointwiseRow
	for _, r := range d.Pointwise {
		if trainSet[r.SessionID] && r.LabelSource == t.TrainSource {
			trainRows = append(trainRows, r)
		}
	}
	// pairwise filtered to train sessions (avoid leakage)
	var trainPairs []schema.PairwiseRow
	for _, p := range d.Pairwise {
		if trainSet[sessionOf(p.PromptID)] {
			trainPairs = append(trainPairs, p)
		}
	}

	// eval rows: held-out sessions, LOCAL-served (censoring: only local rows let
	// us ask "did local fail?"). Pick the strongest source present in eval.
	isFrontier := func(m string) bool {
		return m == d.Cfg.FrontierModel || strings.HasPrefix(m, "claude")
	}
	evalSource := pickEvalSource(d.Pointwise, trainSet)
	var evalRows []schema.PointwiseRow
	for _, r := range d.Pointwise {
		if !trainSet[r.SessionID] && !isFrontier(r.Model) && r.LabelSource == evalSource {
			evalRows = append(evalRows, r)
		}
	}

	// label-source guardrail
	if schema.LabelStrength(evalSource) <= schema.LabelStrength(t.TrainSource) {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf(
			"LABEL CIRCULARITY: eval source %q is not strictly stronger than train source %q — "+
				"these numbers measure self-agreement with the %s heuristic, not correctness. "+
				"Populate more judge/executed eval labels to fix.",
			evalSource, t.TrainSource, t.TrainSource))
	}
	if len(evalRows) < 5 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf(
			"only %d eval rows (source %q) in the held-out split — metrics are high-variance.",
			len(evalRows), evalSource))
	}

	td := router.TrainData{
		Pointwise: trainRows, Pairwise: trainPairs,
		LocalModels: d.Cfg.LocalModels, FrontierModel: d.Cfg.FrontierModel,
		TrainSource: t.TrainSource,
	}

	labels := make([]int, len(evalRows))
	for i, r := range evalRows {
		if r.Outcome == 0 { // local inadequate -> should escalate
			labels[i] = 1
		}
	}

	for _, r := range routers {
		if err := r.Fit(td); err != nil {
			return rep, fmt.Errorf("fit %s: %w", r.Name(), err)
		}
		scores := make([]float64, len(evalRows))
		correct := 0
		for i, row := range evalRows {
			inst := router.InstanceFromPointwise(row)
			scores[i] = r.Score(inst)
			if r.Decide(inst, t.Threshold) == (labels[i] == 1) {
				correct++
			}
		}
		acc := 0.0
		if len(evalRows) > 0 {
			acc = float64(correct) / float64(len(evalRows))
		}
		rep.Rows = append(rep.Rows, ReportRow{
			Router: r.Name(),
			Metrics: map[string]float64{
				"auc":     AUC(scores, labels),
				"ece":     ECE(scores, labels, 10),
				"acc@thr": acc,
			},
		})
	}

	rep.Detail = map[string]any{
		"split_ts":       splitTS,
		"train_sessions": cut,
		"eval_sessions":  len(sess) - cut,
		"train_rows":     len(trainRows),
		"eval_rows":      len(evalRows),
		"train_source":   t.TrainSource,
		"eval_source":    evalSource,
	}
	rep.Notes = append(rep.Notes,
		fmt.Sprintf("Split by session+time at unix %d: %d train / %d eval sessions; train_rows=%d eval_rows=%d (eval source=%q).",
			splitTS, cut, len(sess)-cut, len(trainRows), len(evalRows), evalSource),
		"Backtests only RANK routers — they inherit the label heuristic's blind spots and log censoring. Absolute numbers come from the gold set only.",
	)
	return rep, nil
}

// pickEvalSource returns the strongest label source present among held-out,
// local-served rows (falls back to implicit).
func pickEvalSource(rows []schema.PointwiseRow, trainSet map[string]bool) schema.LabelSource {
	best := schema.LabelImplicit
	for _, r := range rows {
		if trainSet[r.SessionID] {
			continue
		}
		if schema.LabelStrength(r.LabelSource) > schema.LabelStrength(best) {
			best = r.LabelSource
		}
	}
	return best
}

var _ EvalMethod = (*TemporalBacktest)(nil)
