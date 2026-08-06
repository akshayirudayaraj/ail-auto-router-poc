package materialize

import (
	"context"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/label"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func rec(task, arm string, oracle bool, src schema.LabelSource, outcome int, split, sess string) label.LabelRecord {
	model := "gpt-oss:20b"
	if arm == "frontier" {
		model = "opus"
	}
	return label.LabelRecord{
		SessionID: sess, TaskID: task, Model: model, Arm: arm, Split: split,
		HasExecutableOracle: oracle, Outcome: outcome, LabelSource: src, LabelConfidence: 1,
	}
}

func testCfg() config.Config {
	return config.Config{LocalModels: []string{"gpt-oss:20b"}, FrontierModel: "opus", DataDir: "data"}
}

func TestBuild(t *testing.T) {
	resolved := []label.LabelRecord{
		// train task, executed both arms -> 2 pointwise + 1 pairwise, NO gold (train)
		rec("t-train-exec", "local", true, schema.LabelExecuted, 0, "train", "s1"),
		rec("t-train-exec", "frontier", true, schema.LabelExecuted, 1, "train", "s2"),
		// train task, no-oracle judge labels -> pointwise/pairwise (legit weak)
		rec("t-train-judge", "local", false, schema.LabelJudge, 1, "train", "s3"),
		rec("t-train-judge", "frontier", false, schema.LabelJudge, 1, "train", "s4"),
		// holdout task, executed both arms -> 1 GOLD row, not in pointwise
		rec("t-hold-exec", "local", true, schema.LabelExecuted, 0, "holdout", "s5"),
		rec("t-hold-exec", "frontier", true, schema.LabelExecuted, 1, "holdout", "s6"),
		// LANDMINE: oracle-bearing but only implicit -> QUARANTINED everywhere
		rec("t-oracle-ungraded", "local", true, schema.LabelImplicit, 1, "train", "s7"),
		rec("t-oracle-ungraded", "frontier", true, schema.LabelImplicit, 1, "train", "s8"),
		// holdout task with executed on ONE arm only -> dropped from gold
		rec("t-hold-half", "local", true, schema.LabelExecuted, 1, "holdout", "s9"),
	}
	issues := map[string]string{
		"t-train-exec": "fix a", "t-train-judge": "fix b",
		"t-hold-exec": "fix c", "t-oracle-ungraded": "fix d", "t-hold-half": "fix e",
	}
	sessions := map[string]Session{
		"s5": {InputTokens: 10, OutputTokens: 5}, // local holdout -> cost 15*1
		"s6": {InputTokens: 20, OutputTokens: 0}, // frontier holdout -> cost 20*15
	}

	ds, meta, err := Build(context.Background(), testCfg(), resolved, issues, sessions, nil)
	if err != nil {
		t.Fatal(err)
	}

	// quarantine: both oracle-ungraded arms dropped
	if meta.QuarantinedOracleUngraded != 2 {
		t.Errorf("quarantine = %d, want 2", meta.QuarantinedOracleUngraded)
	}
	// pointwise: train-exec(2) + train-judge(2) = 4; NO holdout, NO quarantined
	if meta.NPointwise != 4 {
		t.Errorf("pointwise = %d, want 4", meta.NPointwise)
	}
	for _, p := range ds.Pointwise {
		if p.PromptID == "t-hold-exec" || p.PromptID == "t-oracle-ungraded" || p.PromptID == "t-hold-half" {
			t.Errorf("pointwise leaked holdout/quarantined task %s", p.PromptID)
		}
	}
	// pairwise: only the two full-dual-arm train tasks
	if meta.NPairwise != 2 {
		t.Errorf("pairwise = %d, want 2", meta.NPairwise)
	}
	// gold: only t-hold-exec (dual executed holdout); half-arm dropped
	if meta.NGold != 1 {
		t.Fatalf("gold = %d, want 1", meta.NGold)
	}
	if meta.HoldoutDroppedNotDualExec != 1 {
		t.Errorf("holdout dropped = %d, want 1", meta.HoldoutDroppedNotDualExec)
	}
	g := ds.Gold[0]
	if g.PromptID != "t-hold-exec" || g.OutcomeLocal != 0 || g.OutcomeFrontier != 1 || !g.Executable {
		t.Errorf("gold row wrong: %+v", g)
	}
	if g.CostLocal != 15 || g.CostFrontier != 300 {
		t.Errorf("cost local=%v frontier=%v, want 15/300", g.CostLocal, g.CostFrontier)
	}
	if g.LocalModel != "gpt-oss:20b" || g.FrontierModel != "opus" {
		t.Errorf("gold models = %s/%s", g.LocalModel, g.FrontierModel)
	}
	if meta.FirewallWarning != "" {
		t.Errorf("unexpected firewall warning: %s", meta.FirewallWarning)
	}
}

func TestPreferenceAndWeaker(t *testing.T) {
	if preference(1, 0) != "a" || preference(0, 1) != "b" || preference(1, 1) != "tie" {
		t.Error("preference mapping wrong")
	}
	if weaker(schema.LabelExecuted, schema.LabelImplicit) != schema.LabelImplicit {
		t.Error("weaker should pick implicit")
	}
	if weaker(schema.LabelJudge, schema.LabelExecuted) != schema.LabelJudge {
		t.Error("weaker should pick judge")
	}
}
