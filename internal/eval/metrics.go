// Package eval is the evaluation harness (Pillar 3). It encodes the four
// things that make router evaluation hard — censoring, no-oracle, feedback
// loops, and label circularity — as explicit guardrails, and implements
// several evaluation methods behind a common interface.
//
// This file is the metrics module: AUC, calibration/ECE, escalation rate,
// quality retention, cost, and the RouterBench-style cost/quality curve with
// its convex hull, AIQ, and the under-/over-escalation cell rates.
package eval

import (
	"math"
	"sort"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// AUC is the area under the ROC curve for scores against binary labels
// (label 1 = positive). Computed via the rank-sum (Mann-Whitney U) identity.
// Returns 0.5 when one class is absent.
func AUC(scores []float64, labels []int) float64 {
	type ps struct {
		s float64
		y int
	}
	n := len(scores)
	arr := make([]ps, n)
	var npos, nneg int
	for i := range scores {
		arr[i] = ps{scores[i], labels[i]}
		if labels[i] == 1 {
			npos++
		} else {
			nneg++
		}
	}
	if npos == 0 || nneg == 0 {
		return 0.5
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].s < arr[j].s })
	// assign average ranks (1..n), handling ties
	ranks := make([]float64, n)
	i := 0
	for i < n {
		j := i
		for j+1 < n && arr[j+1].s == arr[i].s {
			j++
		}
		avg := float64(i+j+2) / 2.0 // ranks are 1-indexed
		for k := i; k <= j; k++ {
			ranks[k] = avg
		}
		i = j + 1
	}
	var sumRankPos float64
	for k := range arr {
		if arr[k].y == 1 {
			sumRankPos += ranks[k]
		}
	}
	u := sumRankPos - float64(npos)*(float64(npos)+1)/2.0
	return u / (float64(npos) * float64(nneg))
}

// ECE is the expected calibration error over nbins equal-width bins. probs are
// predicted P(label=1); labels are the outcomes. Lower is better.
func ECE(probs []float64, labels []int, nbins int) float64 {
	if nbins <= 0 {
		nbins = 10
	}
	type bin struct {
		n          int
		sumP, sumY float64
	}
	bins := make([]bin, nbins)
	for i, p := range probs {
		b := int(p * float64(nbins))
		if b >= nbins {
			b = nbins - 1
		}
		if b < 0 {
			b = 0
		}
		bins[b].n++
		bins[b].sumP += p
		bins[b].sumY += float64(labels[i])
	}
	var ece float64
	n := float64(len(probs))
	if n == 0 {
		return 0
	}
	for _, b := range bins {
		if b.n == 0 {
			continue
		}
		conf := b.sumP / float64(b.n)
		acc := b.sumY / float64(b.n)
		ece += float64(b.n) / n * math.Abs(acc-conf)
	}
	return ece
}

// CQPoint is one operating point on the cost/quality curve.
type CQPoint struct {
	Threshold  float64 `json:"threshold"`
	Escalation float64 `json:"escalation"` // fraction routed to frontier
	Quality    float64 `json:"quality"`    // mean achieved adequacy
	Cost       float64 `json:"cost"`       // mean achieved cost (relative units)
}

// CostQualityCurve sweeps the decision threshold over a router's gold scores
// and reports achieved (cost, quality) at each. Both arms' outcomes are known
// (dual-arm gold), so there is no censoring here — this is the honest anchor.
func CostQualityCurve(scores []float64, gold []schema.GoldRow) []CQPoint {
	// candidate thresholds: just above/below every distinct score, so we sweep
	// from "never escalate" to "always escalate".
	ts := make([]float64, 0, len(scores)+2)
	ts = append(ts, math.Inf(1)) // never escalate
	seen := map[float64]bool{}
	for _, s := range scores {
		if !seen[s] {
			seen[s] = true
			ts = append(ts, s)
		}
	}
	ts = append(ts, math.Inf(-1)) // always escalate
	sort.Float64s(ts)

	var pts []CQPoint
	for _, t := range ts {
		var q, c, esc float64
		for i, row := range gold {
			escalate := scores[i] >= t
			if escalate {
				q += float64(row.OutcomeFrontier)
				c += row.CostFrontier
				esc++
			} else {
				q += float64(row.OutcomeLocal)
				c += row.CostLocal
			}
		}
		n := float64(len(gold))
		tt := t
		if math.IsInf(t, 1) {
			tt = 1.0001
		} else if math.IsInf(t, -1) {
			tt = -0.0001
		}
		pts = append(pts, CQPoint{Threshold: tt, Escalation: esc / n, Quality: q / n, Cost: c / n})
	}
	return pts
}

// UpperHull returns the upper-left convex frontier: for increasing cost, the
// maximum achievable quality (the RouterBench-style Pareto frontier). Input
// need not be sorted.
func UpperHull(pts []CQPoint) []CQPoint {
	cp := append([]CQPoint{}, pts...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Cost != cp[j].Cost {
			return cp[i].Cost < cp[j].Cost
		}
		return cp[i].Quality > cp[j].Quality
	})
	// keep a non-decreasing-quality frontier
	var front []CQPoint
	bestQ := math.Inf(-1)
	for _, p := range cp {
		if p.Quality > bestQ {
			front = append(front, p)
			bestQ = p.Quality
		}
	}
	// upper convex hull over the frontier (monotone chain, keep upper turns)
	var hull []CQPoint
	for _, p := range front {
		for len(hull) >= 2 && cross(hull[len(hull)-2], hull[len(hull)-1], p) >= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}
	return hull
}

func cross(o, a, b CQPoint) float64 {
	return (a.Cost-o.Cost)*(b.Quality-o.Quality) - (a.Quality-o.Quality)*(b.Cost-o.Cost)
}

// AIQ is the Area under the (non-decreasing) achievable-quality frontier:
// the best quality reachable at each budget, trapezoid-integrated over cost
// normalized to [0,1] across the GLOBAL all-local→all-frontier range (the
// RouterBench convention). Normalizing over the shared global range — not each
// router's own hull span — is what lets AIQ rank routers: one that buys q=1.0
// at lower cost integrates a larger area. Takes the full cost/quality curve.
func AIQ(curve []CQPoint) float64 {
	if len(curve) == 0 {
		return 0
	}
	cp := append([]CQPoint{}, curve...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Cost < cp[j].Cost })
	cLo, cHi := cp[0].Cost, cp[len(cp)-1].Cost
	if cHi <= cLo {
		return cp[len(cp)-1].Quality
	}
	// cumulative-max quality as budget increases = achievable frontier
	runMax := math.Inf(-1)
	xs := make([]float64, len(cp))
	qs := make([]float64, len(cp))
	for i, p := range cp {
		if p.Quality > runMax {
			runMax = p.Quality
		}
		xs[i] = (p.Cost - cLo) / (cHi - cLo)
		qs[i] = runMax
	}
	var area float64
	for i := 1; i < len(cp); i++ {
		area += (qs[i] + qs[i-1]) / 2 * (xs[i] - xs[i-1])
	}
	return area
}

// LocalOffloadAtFrontierQuality is this POC's headline metric: the largest share
// of requests a router can keep on the LOCAL model while STILL matching
// always-frontier's quality (retention = 1.0). Higher = more offload at no
// quality cost. Threshold-independent — swept over the whole curve. Returns 0
// when the router can't reach frontier quality at any operating point (e.g.
// always-local, whose quality ceiling sits below the frontier's).
//
// Unlike AIQ (a $-cost-weighted area), this needs no cost model — it answers the
// question that actually matters here: "how much can we route to local without
// losing quality?" It rises automatically as the local model improves.
func LocalOffloadAtFrontierQuality(curve []CQPoint) float64 {
	if len(curve) == 0 {
		return 0
	}
	frontierQ := 0.0 // quality when everything is escalated
	for _, p := range curve {
		if p.Escalation > 1-1e-9 {
			frontierQ = p.Quality
		}
	}
	offload := 0.0
	for _, p := range curve {
		if p.Quality >= frontierQ-1e-9 {
			if ls := 1 - p.Escalation; ls > offload {
				offload = ls
			}
		}
	}
	return offload
}

// EscalationCells are the dual-arm confusion cells for a fixed decision vector.
// UnderEscalation ("cell B"): stayed local, local failed, frontier would have
// passed — the costly miss the router must minimize. OverEscalation: escalated
// though local would have passed — wasted spend.
type EscalationCells struct {
	UnderEscalation float64 `json:"under_escalation_rate"` // cell B
	OverEscalation  float64 `json:"over_escalation_rate"`
	CorrectEscalate float64 `json:"correct_escalate_rate"` // escalated, local would fail
	CorrectStay     float64 `json:"correct_stay_rate"`     // stayed, local passes
}

// Cells computes the escalation confusion cells over the gold set given each
// row's escalate decision.
func Cells(decisions []bool, gold []schema.GoldRow) EscalationCells {
	var under, over, cesc, cstay float64
	n := float64(len(gold))
	for i, row := range gold {
		esc := decisions[i]
		localPass := row.OutcomeLocal == 1
		frontPass := row.OutcomeFrontier == 1
		switch {
		case !esc && !localPass && frontPass:
			under++
		case esc && localPass:
			over++
		case esc && !localPass:
			cesc++
		case !esc && localPass:
			cstay++
		}
	}
	if n == 0 {
		return EscalationCells{}
	}
	return EscalationCells{
		UnderEscalation: under / n,
		OverEscalation:  over / n,
		CorrectEscalate: cesc / n,
		CorrectStay:     cstay / n,
	}
}

// OperatingPoint summarizes a router at one threshold on the gold set.
type OperatingPoint struct {
	Threshold        float64         `json:"threshold"`
	EscalationRate   float64         `json:"escalation_rate"`
	Quality          float64         `json:"quality"`           // mean achieved adequacy
	QualityRetention float64         `json:"quality_retention"` // vs always-frontier
	Cost             float64         `json:"cost"`              // mean achieved cost
	CostVsLocal      float64         `json:"cost_vs_local"`     // vs always-local
	Cells            EscalationCells `json:"cells"`
}

// Operating computes the operating point for a router's gold scores at a
// threshold, including quality retention and relative cost.
func Operating(scores []float64, gold []schema.GoldRow, threshold float64) OperatingPoint {
	decisions := make([]bool, len(gold))
	var q, c, esc float64
	var qFront, cLocal float64
	for i, row := range gold {
		decisions[i] = scores[i] >= threshold
		qFront += float64(row.OutcomeFrontier)
		cLocal += row.CostLocal
		if decisions[i] {
			q += float64(row.OutcomeFrontier)
			c += row.CostFrontier
			esc++
		} else {
			q += float64(row.OutcomeLocal)
			c += row.CostLocal
		}
	}
	n := float64(len(gold))
	op := OperatingPoint{
		Threshold:      threshold,
		EscalationRate: esc / n,
		Quality:        q / n,
		Cost:           c / n,
		Cells:          Cells(decisions, gold),
	}
	if qFront > 0 {
		op.QualityRetention = q / qFront
	}
	if cLocal > 0 {
		op.CostVsLocal = c / cLocal
	}
	return op
}
