package label

import (
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func exec_(task string, out int) LabelRecord {
	return LabelRecord{TaskID: task, Model: "m", LabelSource: schema.LabelExecuted, Outcome: out}
}
func judge_(task string, out int, conf float64) LabelRecord {
	return LabelRecord{TaskID: task, Model: "m", LabelSource: schema.LabelJudge, Outcome: out, LabelConfidence: conf}
}
func impl_(task string, out int) LabelRecord {
	return LabelRecord{TaskID: task, Model: "m", LabelSource: schema.LabelImplicit, Outcome: out}
}

// implSig_ is an implicit record carrying a specific behavioral signal (for the
// consensus tiering: switch/paste_error/negative/retry are strong failures,
// moveon a strong success, complete/none are weak).
func implSig_(task string, out int, conf float64, signal string) LabelRecord {
	return LabelRecord{TaskID: task, Model: "m", LabelSource: schema.LabelImplicit, Outcome: out,
		LabelConfidence: conf, Evidence: map[string]any{"signal": signal}}
}

func TestCalibrate_JudgeVsExecuted(t *testing.T) {
	// 4 sessions with executed truth + judge prediction.
	// truth:  t1 inadequate(0), t2 inadequate(0), t3 adequate(1), t4 adequate(1)
	// judge:  t1 0 (TP),        t2 1 (FN),        t3 1 (TN),      t4 0 (FP)
	recs := []LabelRecord{
		exec_("t1", 0), judge_("t1", 0, 0.9),
		exec_("t2", 0), judge_("t2", 1, 0.6),
		exec_("t3", 1), judge_("t3", 1, 0.8),
		exec_("t4", 1), judge_("t4", 0, 0.7),
	}
	rep := Calibrate(recs)
	j := rep.BySource["judge"]
	if j.N != 4 {
		t.Fatalf("N=%d want 4", j.N)
	}
	if j.TP != 1 || j.FP != 1 || j.FN != 1 || j.TN != 1 {
		t.Errorf("confusion TP/FP/FN/TN = %d/%d/%d/%d want 1/1/1/1", j.TP, j.FP, j.FN, j.TN)
	}
	if j.Accuracy != 0.5 {
		t.Errorf("accuracy=%v want 0.5", j.Accuracy)
	}
	if j.Precision != 0.5 || j.Recall != 0.5 {
		t.Errorf("precision/recall = %v/%v want 0.5/0.5", j.Precision, j.Recall)
	}
	if rep.JudgeAccuracy() != 0.5 {
		t.Errorf("JudgeAccuracy()=%v", rep.JudgeAccuracy())
	}
}

func TestCalibrate_NoOverlap(t *testing.T) {
	rep := Calibrate([]LabelRecord{judge_("t1", 0, 0.9), impl_("t2", 1)})
	if rep.BySource["judge"].N != 0 || rep.Note == "" {
		t.Errorf("expected N=0 and a note; got N=%d note=%q", rep.BySource["judge"].N, rep.Note)
	}
}

func TestFuse_Consensus(t *testing.T) {
	p := DefaultFuseParams()

	// agree + STRONG signal -> consensus source, judge outcome, confidence boosted
	j := judge_("t", 0, 0.8)
	h := implSig_("t", 0, 0.75, "negative") // strong failure, agrees with judge=0
	got, ok := FuseSession(&j, &h, p)
	if !ok || got.LabelSource != schema.LabelConsensus || got.Outcome != 0 || got.LabelConfidence <= 0.8 {
		t.Errorf("agree+strong: src=%v outcome=%d conf=%v (want consensus/0/>0.8)", got.LabelSource, got.Outcome, got.LabelConfidence)
	}
	if got.Evidence["agreement"] != true {
		t.Errorf("agree: agreement flag not set")
	}

	// STRONG FAILURE cue vs a judge "success" -> FLIP to inadequate
	j2 := judge_("t", 1, 0.8)
	h2 := implSig_("t", 0, 0.8, "paste_error")
	got2, _ := FuseSession(&j2, &h2, p)
	if got2.Outcome != 0 || got2.Evidence["rule"] != "consensus_veto_flip0" || got2.Evidence["flipped"] != true {
		t.Errorf("veto: want outcome 0 flipped; got outcome=%d rule=%v", got2.Outcome, got2.Evidence["rule"])
	}

	// WEAK implicit disagreeing (complete/none) -> ignored: outcome & conf unchanged
	j3 := judge_("t", 0, 0.8)
	h3 := impl_("t", 1) // no signal => none (weak)
	got3, _ := FuseSession(&j3, &h3, p)
	if got3.Outcome != 0 || got3.LabelConfidence != 0.8 || got3.Evidence["rule"] != "consensus_weak_ignore" {
		t.Errorf("weak disagree: want 0/0.8/ignore; got outcome=%d conf=%v rule=%v", got3.Outcome, got3.LabelConfidence, got3.Evidence["rule"])
	}

	// STRONG SUCCESS vs a judge failure -> keep judge (don't fabricate adequate), penalized
	j4 := judge_("t", 0, 0.8)
	h4 := implSig_("t", 1, 0.7, "moveon")
	got4, _ := FuseSession(&j4, &h4, p)
	if got4.Outcome != 0 || got4.Evidence["rule"] != "consensus_conflict_keepjudge" || got4.LabelConfidence >= 0.8 {
		t.Errorf("conflict: want outcome 0 kept & penalized; got outcome=%d conf=%v rule=%v", got4.Outcome, got4.LabelConfidence, got4.Evidence["rule"])
	}

	// judge only -> stays judge source + rule
	j5 := judge_("t", 1, 0.7)
	got5, _ := FuseSession(&j5, nil, p)
	if got5.Outcome != 1 || got5.Evidence["rule"] != "judge_only" || got5.LabelSource != schema.LabelJudge {
		t.Errorf("judge-only: outcome=%d rule=%v src=%v", got5.Outcome, got5.Evidence["rule"], got5.LabelSource)
	}
}

func TestResolveWithFusion_ExecutedWins(t *testing.T) {
	recs := []LabelRecord{
		exec_("t1", 1),          // executed present -> wins
		judge_("t1", 0, 0.9),    // ignored for t1
		judge_("t2", 0, 0.6), impl_("t2", 1), // no executed -> fused (disagree, judge wins)
	}
	got := ResolveWithFusion(recs, DefaultFuseParams())
	if got["t1|m"].LabelSource != schema.LabelExecuted || got["t1|m"].Outcome != 1 {
		t.Errorf("t1 should be executed/1; got %v/%d", got["t1|m"].LabelSource, got["t1|m"].Outcome)
	}
	if got["t2|m"].Outcome != 0 || got["t2|m"].Evidence["disagreement_flag"] != true {
		t.Errorf("t2 should fuse to judge outcome 0 with disagreement flag; got %+v", got["t2|m"])
	}
}
