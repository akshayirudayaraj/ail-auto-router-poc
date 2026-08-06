package label

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/akshayirudayaraj/ail-routing-test/internal/extract"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// Fusion combines the weak labels (judge + heuristics) on an oracle-LESS session
// into one canonical CONSENSUS outcome + confidence (source = "consensus").
// Judge-primary by default: the judge sees full context (the evidence pack) and
// sets the outcome, with heuristics modulating confidence — EXCEPT a strong,
// high-precision behavioral FAILURE cue (user pasted an error, said "wrong",
// retried, or escalated) vetoes a judge "success" and flips the outcome to 0
// (false-adequate is the costly routing error). Weak implicit defaults
// (complete/none) are treated as noise. Reliabilities can later be calibrated
// against the executed oracle (FuseParams.JudgeAccuracy/HeurAccuracy).

// FuseParams tune fusion. JudgeAccuracy/HeurAccuracy come from Calibrate (0 =
// uncalibrated → fall back to the labels' own confidence).
type FuseParams struct {
	JudgeAccuracy   float64 // calibrated P(judge correct); 0 if unknown
	HeurAccuracy    float64 // calibrated P(heuristic correct); 0 if unknown
	DisagreePenalty float64 // confidence multiplier when judge & heuristic disagree
	AgreeBoost      float64 // fraction of the headroom to (1.0) added on agreement
}

// DefaultFuseParams: halve confidence on disagreement, close 30% of the gap to 1.0
// on agreement.
func DefaultFuseParams() FuseParams {
	return FuseParams{DisagreePenalty: 0.5, AgreeBoost: 0.3}
}

// FuseSession produces the canonical judge-primary label from the weak records for
// one session (either may be nil). Returns ok=false if neither is present.
func FuseSession(judge, heur *LabelRecord, p FuseParams) (LabelRecord, bool) {
	switch {
	case judge == nil && heur == nil:
		return LabelRecord{}, false

	case judge == nil: // heuristic only (rare) — weakest canonical
		rec := *heur
		if p.HeurAccuracy > 0 {
			rec.LabelConfidence = p.HeurAccuracy
		}
		rec.Evidence = mergeEvidence(rec.Evidence, map[string]any{
			"fused": true, "rule": "heuristic_only", "heuristic_outcome": heur.Outcome})
		return rec, true

	case heur == nil: // judge only — the common oracle-less case
		rec := *judge
		rec.LabelConfidence = base(p.JudgeAccuracy, judge.LabelConfidence)
		rec.Evidence = mergeEvidence(rec.Evidence, map[string]any{
			"fused": true, "rule": "judge_only", "judge_outcome": judge.Outcome})
		return rec, true

	default: // both present — CONSENSUS of judge + implicit (Strategy A)
		rec := *judge
		rec.LabelSource = schema.LabelConsensus
		b := base(p.JudgeAccuracy, judge.LabelConfidence)

		sig := heurSignal(heur)
		strongFail := extract.SignalIsFailure(sig) // switch/paste_error/negative/retry
		strongSuccess := sig == extract.SigMoveOn  // explicit praise / positive move-on
		strong := strongFail || strongSuccess      // implicit carries a high-precision cue
		agree := judge.Outcome == heur.Outcome

		var conf float64
		var rule string
		switch {
		case agree:
			// concur — boost confidence, but only when implicit is a real signal
			// (a bare complete/none default adds ~nothing).
			boost := 0.0
			if strong {
				boost = p.AgreeBoost
			}
			conf = b + (1-b)*boost
			rule = "consensus_agree"
		case strongFail && judge.Outcome == 1:
			// A strong behavioral FAILURE cue (user pasted an error, said "wrong",
			// retried, or escalated local→frontier) against a plausible-looking
			// judge "success": trust the cue and FLIP to inadequate. False-adequate
			// is the costly routing error, and the cue sees what the diff can't.
			rec.Outcome = 0
			conf = heurConf(heur) // the failure cue's own (per-signal) precision
			rule = "consensus_veto_flip0"
		case strong:
			// Conflicting STRONG success vs a judge failure: do NOT fabricate an
			// "adequate" — keep the judge's conservative outcome, mark it uncertain.
			conf = b * p.DisagreePenalty
			rule = "consensus_conflict_keepjudge"
		default:
			// weak implicit (complete/none) disagreeing is noise — ignore it.
			conf = b
			rule = "consensus_weak_ignore"
		}
		rec.LabelConfidence = clamp01(conf)
		rec.Evidence = mergeEvidence(rec.Evidence, map[string]any{
			"fused":             true,
			"rule":              rule,
			"consensus":         true,
			"judge_outcome":     judge.Outcome,
			"heuristic_outcome": heur.Outcome,
			"implicit_signal":   string(sig),
			"strong_signal":     strong,
			"agreement":         agree,
			"base_confidence":   b,
			"calibrated":        p.JudgeAccuracy > 0,
			"disagreement_flag": !agree,
			"flipped":           rule == "consensus_veto_flip0",
		})
		return rec, true
	}
}

// heurSignal reads the implicit signal kind off a heuristic record's evidence
// (heuristics.go stamps evidence["signal"]).
func heurSignal(heur *LabelRecord) extract.SignalKind {
	if heur == nil || heur.Evidence == nil {
		return extract.SigNone
	}
	if s, ok := heur.Evidence["signal"].(string); ok {
		return extract.SignalKind(s)
	}
	return extract.SigNone
}

// heurConf returns the heuristic label's confidence (the per-signal precision),
// with a sane floor when unset.
func heurConf(heur *LabelRecord) float64 {
	if heur != nil && heur.LabelConfidence > 0 {
		return heur.LabelConfidence
	}
	return 0.6
}

func base(calibAcc, labelConf float64) float64 {
	if calibAcc > 0 {
		return calibAcc
	}
	return labelConf
}

func mergeEvidence(dst, extra map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range extra {
		dst[k] = v
	}
	return dst
}

// ResolveWithFusion is the calibrated canonical-label selector: executed wins where
// present; otherwise the judge/heuristic records are FUSED (judge-primary). Returns
// one canonical LabelRecord per (task, model). Unlike Resolve (strength-only), this
// blends the weak sources and carries a disagreement flag.
func ResolveWithFusion(recs []LabelRecord, p FuseParams) map[string]LabelRecord {
	type bucket struct{ executed, judge, heur *LabelRecord }
	buckets := map[string]*bucket{}
	for i := range recs {
		r := recs[i]
		b := buckets[r.key()]
		if b == nil {
			b = &bucket{}
			buckets[r.key()] = b
		}
		switch r.LabelSource {
		case schema.LabelExecuted:
			b.executed = &recs[i]
		case schema.LabelJudge:
			b.judge = &recs[i]
		case schema.LabelImplicit:
			b.heur = &recs[i]
		}
	}
	out := map[string]LabelRecord{}
	for k, b := range buckets {
		if b.executed != nil {
			out[k] = *b.executed
			continue
		}
		if fused, ok := FuseSession(b.judge, b.heur, p); ok {
			out[k] = fused
		}
	}
	return out
}

// SaveResolved writes the canonical labels to <dir>/labels/resolved.jsonl (sorted
// by session for stable diffs).
func SaveResolved(dir string, resolved map[string]LabelRecord) error {
	ldir := filepath.Join(dir, "labels")
	if err := os.MkdirAll(ldir, 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(resolved))
	for k := range resolved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	f, err := os.Create(filepath.Join(ldir, "resolved.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, k := range keys {
		if err := enc.Encode(resolved[k]); err != nil {
			return err
		}
	}
	return w.Flush()
}
