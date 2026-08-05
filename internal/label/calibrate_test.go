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

func TestFuse_JudgePrimary(t *testing.T) {
	p := DefaultFuseParams()

	// agree -> outcome judge, confidence boosted above base
	j := judge_("t", 0, 0.8)
	h := impl_("t", 0)
	got, ok := FuseSession(&j, &h, p)
	if !ok || got.Outcome != 0 || got.LabelConfidence <= 0.8 {
		t.Errorf("agree: outcome=%d conf=%v (want 0, >0.8)", got.Outcome, got.LabelConfidence)
	}
	if got.Evidence["agreement"] != true {
		t.Errorf("agree: agreement flag not set")
	}

	// disagree -> outcome STILL judge, confidence penalized, flagged
	j2 := judge_("t", 0, 0.8)
	h2 := impl_("t", 1)
	got2, _ := FuseSession(&j2, &h2, p)
	if got2.Outcome != 0 {
		t.Errorf("disagree: outcome must stay judge's (0); got %d", got2.Outcome)
	}
	if got2.LabelConfidence >= 0.8 {
		t.Errorf("disagree: confidence should be penalized below base; got %v", got2.LabelConfidence)
	}
	if got2.Evidence["disagreement_flag"] != true {
		t.Errorf("disagree: disagreement_flag not set")
	}

	// judge only
	j3 := judge_("t", 1, 0.7)
	got3, _ := FuseSession(&j3, nil, p)
	if got3.Outcome != 1 || got3.Evidence["rule"] != "judge_only" {
		t.Errorf("judge-only: outcome=%d rule=%v", got3.Outcome, got3.Evidence["rule"])
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
