package eval

import (
	"math"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func TestAUC(t *testing.T) {
	// perfect separation
	scores := []float64{0.1, 0.2, 0.8, 0.9}
	labels := []int{0, 0, 1, 1}
	if a := AUC(scores, labels); math.Abs(a-1.0) > 1e-9 {
		t.Fatalf("perfect AUC = %v", a)
	}
	// reversed
	if a := AUC([]float64{0.9, 0.8, 0.2, 0.1}, labels); math.Abs(a-0.0) > 1e-9 {
		t.Fatalf("reversed AUC = %v", a)
	}
	// single class -> 0.5
	if a := AUC(scores, []int{1, 1, 1, 1}); a != 0.5 {
		t.Fatalf("single-class AUC = %v", a)
	}
	// ties handled (all equal scores -> 0.5)
	if a := AUC([]float64{0.5, 0.5, 0.5, 0.5}, labels); math.Abs(a-0.5) > 1e-9 {
		t.Fatalf("all-ties AUC = %v", a)
	}
}

func TestECEPerfect(t *testing.T) {
	// probabilities equal to empirical accuracy per bin -> ECE ~ 0
	probs := []float64{0.0, 0.0, 1.0, 1.0}
	labels := []int{0, 0, 1, 1}
	if e := ECE(probs, labels, 10); e > 1e-9 {
		t.Fatalf("perfect ECE = %v", e)
	}
}

func goldFixture() []schema.GoldRow {
	// easy prompts: local passes; hard prompts: only frontier passes.
	var g []schema.GoldRow
	for i := 0; i < 5; i++ {
		g = append(g, schema.GoldRow{OutcomeLocal: 1, OutcomeFrontier: 1, CostLocal: 1, CostFrontier: 15})
	}
	for i := 0; i < 5; i++ {
		g = append(g, schema.GoldRow{OutcomeLocal: 0, OutcomeFrontier: 1, CostLocal: 1, CostFrontier: 15})
	}
	return g
}

func TestCostQualityCurveAndAIQ(t *testing.T) {
	gold := goldFixture()
	// oracle scores: 0 for easy (local ok), 1 for hard (needs frontier)
	scores := []float64{0, 0, 0, 0, 0, 1, 1, 1, 1, 1}
	curve := CostQualityCurve(scores, gold)
	hull := UpperHull(curve)
	if len(hull) < 2 {
		t.Fatal("hull too small")
	}
	// endpoints: cheapest = all local, dearest = all frontier
	if hull[0].Cost > hull[len(hull)-1].Cost {
		t.Fatal("hull not sorted by cost")
	}
	_ = hull
	aiq := AIQ(curve)
	if aiq <= 0 || aiq > 1.0001 {
		t.Fatalf("AIQ out of range: %v", aiq)
	}
	// oracle should beat a random router's AIQ
	rnd := []float64{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5}
	aiqRnd := AIQ(CostQualityCurve(rnd, gold))
	if aiq <= aiqRnd {
		t.Fatalf("oracle AIQ %.3f should beat random %.3f", aiq, aiqRnd)
	}
}

func TestCells(t *testing.T) {
	gold := goldFixture()
	// always-local decision
	dec := make([]bool, len(gold))
	c := Cells(dec, gold)
	// 5 hard rows: local fail, frontier pass, stayed local => under-escalation
	if math.Abs(c.UnderEscalation-0.5) > 1e-9 {
		t.Fatalf("under-escalation = %v want 0.5", c.UnderEscalation)
	}
	if c.OverEscalation != 0 {
		t.Fatalf("over-escalation should be 0, got %v", c.OverEscalation)
	}
}

func TestOperatingRetention(t *testing.T) {
	gold := goldFixture()
	// escalate exactly the hard rows
	scores := []float64{0, 0, 0, 0, 0, 1, 1, 1, 1, 1}
	op := Operating(scores, gold, 0.5)
	// achieves full frontier-quality (all pass) at partial cost
	if math.Abs(op.QualityRetention-1.0) > 1e-9 {
		t.Fatalf("quality retention = %v want 1.0", op.QualityRetention)
	}
	if op.CostVsLocal <= 1.0 {
		t.Fatalf("cost vs local should exceed 1, got %v", op.CostVsLocal)
	}
	if op.EscalationRate != 0.5 {
		t.Fatalf("escalation rate = %v want 0.5", op.EscalationRate)
	}
}

func TestCalibrateEscalationRate(t *testing.T) {
	scores := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0}
	thr := CalibrateEscalationRate(scores, 0.3)
	esc := 0
	for _, s := range scores {
		if s >= thr {
			esc++
		}
	}
	if esc < 2 || esc > 4 { // ~30% of 10
		t.Fatalf("calibrated escalations = %d, want ~3", esc)
	}
}

func TestQuotaGate(t *testing.T) {
	q := NewQuotaGate(0.0, 0.3) // threshold 0 so score never blocks; quota 30%
	esc := 0
	for i := 0; i < 100; i++ {
		if q.Decide(1.0) {
			esc++
		}
	}
	if esc > 30 {
		t.Fatalf("quota gate allowed %d escalations, cap ~30", esc)
	}
	if esc < 28 {
		t.Fatalf("quota gate too conservative: %d", esc)
	}
}
