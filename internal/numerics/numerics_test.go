package numerics

import (
	"math"
	"testing"
)

func TestSigmoid(t *testing.T) {
	if math.Abs(Sigmoid(0)-0.5) > 1e-12 {
		t.Fatal("sigmoid(0) != 0.5")
	}
	if Sigmoid(50) <= 0.99 || Sigmoid(-50) >= 0.01 {
		t.Fatal("sigmoid saturation wrong")
	}
	// no overflow
	if math.IsNaN(Sigmoid(1e6)) || math.IsNaN(Sigmoid(-1e6)) {
		t.Fatal("sigmoid overflow")
	}
}

func TestCosine(t *testing.T) {
	a := []float32{1, 0, 0}
	if c := Cosine(a, a); math.Abs(c-1) > 1e-6 {
		t.Fatalf("cosine self = %v", c)
	}
	b := []float32{0, 1, 0}
	if c := Cosine(a, b); math.Abs(c) > 1e-6 {
		t.Fatalf("orthogonal cosine = %v", c)
	}
	if c := Cosine(a, []float32{0, 0, 0}); c != 0 {
		t.Fatalf("zero-vector cosine = %v", c)
	}
}

// TestFitLogisticSeparable checks the classifier learns an obvious boundary.
func TestFitLogisticSeparable(t *testing.T) {
	var X [][]float64
	var y []int
	for i := 0; i < 100; i++ {
		x := float64(i)/100 - 0.5
		X = append(X, []float64{x})
		if x > 0 {
			y = append(y, 1)
		} else {
			y = append(y, 0)
		}
	}
	std := FitStandardizer(X)
	var Xs [][]float64
	for _, r := range X {
		Xs = append(Xs, std.Transform(r))
	}
	m := FitLogistic(Xs, y, nil, DefaultLogRegParams())
	// point clearly positive
	if p := m.PredictProba(std.Transform([]float64{0.4})); p < 0.7 {
		t.Fatalf("expected high proba, got %v", p)
	}
	if p := m.PredictProba(std.Transform([]float64{-0.4})); p > 0.3 {
		t.Fatalf("expected low proba, got %v", p)
	}
}

func TestNewtonRoot(t *testing.T) {
	// find root of f(x) = x^2 - 2 near x=1 -> sqrt(2)
	f := func(x float64) float64 { return x*x - 2 }
	fp := func(x float64) float64 { return 2 * x }
	x := Newton(f, fp, 1.0, 50, 1e-10)
	if math.Abs(x-math.Sqrt2) > 1e-6 {
		t.Fatalf("newton sqrt2 = %v", x)
	}
}

func TestStandardizerZeroVariance(t *testing.T) {
	X := [][]float64{{5, 1}, {5, 2}, {5, 3}}
	std := FitStandardizer(X)
	if std.Std[0] != 1 {
		t.Fatalf("zero-variance column std should be 1, got %v", std.Std[0])
	}
	out := std.Transform([]float64{5, 2})
	if out[0] != 0 {
		t.Fatalf("constant column should map to 0, got %v", out[0])
	}
}
