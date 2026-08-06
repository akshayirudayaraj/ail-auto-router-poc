// Package numerics is the hand-rolled, stdlib-only linear-algebra and
// optimization the routers need: vector ops, a logistic-regression trained by
// gradient descent, and Newton's method for scalar root/optimum finding (used
// by IRT ability estimation). Kept tiny and dependency-free so it ports
// straight into the Go gateway.
package numerics

import (
	"math"
)

// Sigmoid is the logistic function, guarded against overflow.
func Sigmoid(x float64) float64 {
	if x >= 0 {
		z := math.Exp(-x)
		return 1 / (1 + z)
	}
	z := math.Exp(x)
	return z / (1 + z)
}

// Dot returns the dot product of two equal-length vectors.
func Dot(a, b []float64) float64 {
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// Dot32 is Dot for float32 inputs (embeddings), accumulating in float64.
func Dot32(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// Norm32 is the L2 norm of a float32 vector.
func Norm32(a []float32) float64 {
	var s float64
	for _, v := range a {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s)
}

// Cosine returns cosine similarity of two float32 vectors in [-1,1]. Returns 0
// if either vector is zero-length or all-zero.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	na, nb := Norm32(a), Norm32(b)
	if na == 0 || nb == 0 {
		return 0
	}
	return Dot32(a, b) / (na * nb)
}

// Standardizer captures per-feature mean/std for z-scoring dense features.
// Fitting on train and reusing on eval prevents leakage.
type Standardizer struct {
	Mean []float64
	Std  []float64
}

// FitStandardizer computes column means and (population) std devs. Zero-variance
// columns get Std=1 so scaling is a no-op for them.
func FitStandardizer(rows [][]float64) Standardizer {
	if len(rows) == 0 {
		return Standardizer{}
	}
	d := len(rows[0])
	mean := make([]float64, d)
	std := make([]float64, d)
	for _, r := range rows {
		for j := 0; j < d; j++ {
			mean[j] += r[j]
		}
	}
	n := float64(len(rows))
	for j := range mean {
		mean[j] /= n
	}
	for _, r := range rows {
		for j := 0; j < d; j++ {
			dd := r[j] - mean[j]
			std[j] += dd * dd
		}
	}
	for j := range std {
		std[j] = math.Sqrt(std[j] / n)
		if std[j] < 1e-9 {
			std[j] = 1
		}
	}
	return Standardizer{Mean: mean, Std: std}
}

// Transform z-scores a single feature vector in place-safe fashion (returns a
// new slice).
func (s Standardizer) Transform(x []float64) []float64 {
	if len(s.Mean) == 0 {
		out := make([]float64, len(x))
		copy(out, x)
		return out
	}
	out := make([]float64, len(x))
	for j := range x {
		out[j] = (x[j] - s.Mean[j]) / s.Std[j]
	}
	return out
}

// LogisticRegression is a binary L2-regularized logistic-regression model
// trained by full-batch gradient descent. Weights include a bias term appended
// as the last coordinate (the design matrix is augmented with a 1).
type LogisticRegression struct {
	W []float64 // length = numFeatures + 1 (last entry is bias)
}

// LogRegParams controls training.
type LogRegParams struct {
	LR     float64 // learning rate
	L2     float64 // L2 penalty (not applied to bias)
	Epochs int
	Tol    float64 // stop if mean |gradient| < Tol
}

// DefaultLogRegParams are sane defaults for standardized features.
func DefaultLogRegParams() LogRegParams {
	return LogRegParams{LR: 0.1, L2: 1e-3, Epochs: 500, Tol: 1e-6}
}

// FitLogistic trains on X (n×d) with binary labels y (0/1). Optional per-sample
// weights w (nil => all 1). Returns the fitted model. Features should be
// standardized by the caller for stable convergence.
func FitLogistic(X [][]float64, y []int, w []float64, p LogRegParams) LogisticRegression {
	n := len(X)
	if n == 0 {
		return LogisticRegression{}
	}
	d := len(X[0])
	weights := make([]float64, d+1) // + bias
	grad := make([]float64, d+1)

	sumW := 0.0
	for i := 0; i < n; i++ {
		if w != nil {
			sumW += w[i]
		} else {
			sumW += 1
		}
	}
	if sumW == 0 {
		sumW = 1
	}

	for epoch := 0; epoch < p.Epochs; epoch++ {
		for j := range grad {
			grad[j] = 0
		}
		for i := 0; i < n; i++ {
			z := weights[d] // bias
			for j := 0; j < d; j++ {
				z += weights[j] * X[i][j]
			}
			pred := Sigmoid(z)
			wi := 1.0
			if w != nil {
				wi = w[i]
			}
			err := (pred - float64(y[i])) * wi
			for j := 0; j < d; j++ {
				grad[j] += err * X[i][j]
			}
			grad[d] += err
		}
		// mean gradient + L2 (bias excluded)
		var gnorm float64
		for j := 0; j < d; j++ {
			grad[j] = grad[j]/sumW + p.L2*weights[j]
			gnorm += math.Abs(grad[j])
		}
		grad[d] /= sumW
		gnorm += math.Abs(grad[d])
		for j := range weights {
			weights[j] -= p.LR * grad[j]
		}
		if gnorm/float64(d+1) < p.Tol {
			break
		}
	}
	return LogisticRegression{W: weights}
}

// PredictProba returns P(y=1 | x). x length must equal len(W)-1.
func (m LogisticRegression) PredictProba(x []float64) float64 {
	d := len(m.W) - 1
	if d < 0 {
		return 0.5
	}
	z := m.W[d]
	for j := 0; j < d && j < len(x); j++ {
		z += m.W[j] * x[j]
	}
	return Sigmoid(z)
}

// Newton runs 1-D Newton's method to find a root of f given its derivative
// fprime, starting at x0. Returns the estimate after up to maxIter steps or
// when |step| < tol. Falls back to a damped step if the derivative is tiny.
func Newton(f, fprime func(float64) float64, x0 float64, maxIter int, tol float64) float64 {
	x := x0
	for i := 0; i < maxIter; i++ {
		fx := f(x)
		d := fprime(x)
		if math.Abs(d) < 1e-9 {
			d = math.Copysign(1e-9, d)
			if d == 0 {
				d = 1e-9
			}
		}
		step := fx / d
		// damp large steps for stability
		if step > 2 {
			step = 2
		} else if step < -2 {
			step = -2
		}
		x -= step
		if math.Abs(step) < tol {
			break
		}
	}
	return x
}
