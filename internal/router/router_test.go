package router

import (
	"math"
	"math/rand"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// makeRow builds a pointwise row whose only varying feature is the
// hard-keyword score, so difficulty is a clean 1-D function for recovery tests.
func makeRow(model string, hard float64, outcome int) schema.PointwiseRow {
	return schema.PointwiseRow{
		Model:           model,
		Outcome:         outcome,
		LabelSource:     schema.LabelImplicit,
		LabelConfidence: 1,
		Features:        schema.Features{HardKeywordScore: hard, TurnType: "open"},
	}
}

func sig(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

// TestIRTRecovery checks the 1PL fit recovers a planted ability gap and that
// difficulty tracks the feature. Data are generated from the exact model the
// router assumes: P(success) = sigmoid(theta_m - b(x)).
func TestIRTRecovery(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	const (
		thetaRef    = 0.0
		thetaStrong = 2.0
		wTrue       = 3.0 // b = wTrue*hard - 1
		cTrue       = -1.0
	)
	var rows []schema.PointwiseRow
	for i := 0; i < 500; i++ {
		hard := r.Float64() // in [0,1]
		b := wTrue*hard + cTrue
		for _, mt := range []struct {
			name  string
			theta float64
		}{{"ref", thetaRef}, {"strong", thetaStrong}} {
			p := sig(mt.theta - b)
			out := 0
			if r.Float64() < p {
				out = 1
			}
			rows = append(rows, makeRow(mt.name, hard, out))
		}
	}

	m := NewIRT()
	if err := m.Fit(TrainData{
		Pointwise:     rows,
		LocalModels:   []string{"ref"},
		FrontierModel: "strong",
	}); err != nil {
		t.Fatal(err)
	}
	ab := m.Abilities()
	if math.Abs(ab["ref"]) > 1e-9 {
		t.Fatalf("reference theta not pinned to 0: %v", ab["ref"])
	}
	// recovered gap should be clearly positive and near 2.0
	if ab["strong"] < 1.2 || ab["strong"] > 2.8 {
		t.Fatalf("theta_strong not recovered: got %.3f want ~2.0", ab["strong"])
	}
	// difficulty (hence escalation score) must increase with hard-keyword score
	easy := Instance{Features: schema.Features{HardKeywordScore: 0.0, TurnType: "open"}}
	hard := Instance{Features: schema.Features{HardKeywordScore: 1.0, TurnType: "open"}}
	if m.Score(hard) <= m.Score(easy) {
		t.Fatalf("escalation score should rise with difficulty: easy=%.3f hard=%.3f",
			m.Score(easy), m.Score(hard))
	}
}

// TestIRTOnboardNewModel freezes difficulties and recovers a new model's theta
// by Newton on an anchor set.
func TestIRTOnboardNewModel(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	var rows []schema.PointwiseRow
	for i := 0; i < 500; i++ {
		hard := r.Float64()
		b := 3.0*hard - 1.0
		for _, mt := range []struct {
			name  string
			theta float64
		}{{"ref", 0.0}, {"strong", 2.0}} {
			out := 0
			if r.Float64() < sig(mt.theta-b) {
				out = 1
			}
			rows = append(rows, makeRow(mt.name, hard, out))
		}
	}
	m := NewIRT()
	_ = m.Fit(TrainData{Pointwise: rows, LocalModels: []string{"ref"}, FrontierModel: "strong"})

	// new model with true theta = 1.0; build anchors, freeze b, onboard
	const thetaNew = 1.0
	var anchors []schema.PointwiseRow
	for i := 0; i < 300; i++ {
		hard := r.Float64()
		b := 3.0*hard - 1.0
		out := 0
		if r.Float64() < sig(thetaNew-b) {
			out = 1
		}
		anchors = append(anchors, makeRow("new", hard, out))
	}
	got := m.OnboardModel("new", anchors)
	if got < 0.4 || got > 1.6 {
		t.Fatalf("onboarded theta_new=%.3f want ~1.0", got)
	}
	// ordering sanity: ref < new < strong
	ab := m.Abilities()
	if !(ab["ref"] < got && got < ab["strong"]) {
		t.Fatalf("ability ordering wrong: ref=%.2f new=%.2f strong=%.2f", ab["ref"], got, ab["strong"])
	}
}

// TestRouteLLMLearnsDifficulty: local-inadequate on hard prompts, adequate on
// easy ones -> higher escalation score for hard.
func TestRouteLLMLearnsDifficulty(t *testing.T) {
	var rows []schema.PointwiseRow
	for i := 0; i < 100; i++ {
		rows = append(rows, makeRow("local", 0.9, 0)) // hard -> local inadequate
		rows = append(rows, makeRow("local", 0.0, 1)) // easy -> local adequate
	}
	rl := NewRouteLLM()
	if err := rl.Fit(TrainData{Pointwise: rows, LocalModels: []string{"local"}, FrontierModel: "claude-x"}); err != nil {
		t.Fatal(err)
	}
	easy := Instance{Features: schema.Features{HardKeywordScore: 0.0, TurnType: "open"}}
	hard := Instance{Features: schema.Features{HardKeywordScore: 0.9, TurnType: "open"}}
	if rl.Score(hard) <= rl.Score(easy) {
		t.Fatalf("routellm should escalate more on hard: easy=%.3f hard=%.3f", rl.Score(easy), rl.Score(hard))
	}
}

// TestKNNVote: nearest neighbors that failed locally push the score up.
func TestKNNVote(t *testing.T) {
	hardEmb := []float32{1, 0}
	easyEmb := []float32{0, 1}
	var rows []schema.PointwiseRow
	for i := 0; i < 20; i++ {
		rows = append(rows, schema.PointwiseRow{Model: "local", Outcome: 0, LabelConfidence: 1, Embedding: hardEmb})
		rows = append(rows, schema.PointwiseRow{Model: "local", Outcome: 1, LabelConfidence: 1, Embedding: easyEmb})
	}
	k := NewKNN(5)
	_ = k.Fit(TrainData{Pointwise: rows, LocalModels: []string{"local"}, FrontierModel: "claude-x"})
	if s := k.Score(Instance{Embedding: hardEmb}); s < 0.8 {
		t.Fatalf("expected high escalation near failed cluster, got %.3f", s)
	}
	if s := k.Score(Instance{Embedding: easyEmb}); s > 0.2 {
		t.Fatalf("expected low escalation near succeeded cluster, got %.3f", s)
	}
}

func TestBaselines(t *testing.T) {
	inst := Instance{}
	if (AlwaysLocal{}).Decide(inst, 0.5) {
		t.Fatal("always-local should not escalate")
	}
	if !((AlwaysFrontier{}).Decide(inst, 0.5)) {
		t.Fatal("always-frontier should escalate")
	}
}
