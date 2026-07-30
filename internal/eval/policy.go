package eval

import (
	"sort"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// This file is the small POLICY layer: turn a router's continuous score into a
// deployable decision rule by (a) calibrating a threshold to a target
// escalation rate or a target quality, and (b) gating escalations against a
// frontier quota.
//
// IMPORTANT: calibrating to a target QUALITY requires dual-arm outcomes, so it
// is only trustworthy on the gold set (or later online A/B). A threshold
// calibrated on logs to a target escalation RATE is fine, but its resulting
// quality is not knowable from censored logs.

// CalibrateEscalationRate returns the threshold that makes the fraction of
// scores >= threshold approximately targetRate.
func CalibrateEscalationRate(scores []float64, targetRate float64) float64 {
	if len(scores) == 0 {
		return 0.5
	}
	if targetRate <= 0 {
		return 1.0001 // never escalate
	}
	if targetRate >= 1 {
		return -0.0001 // always escalate
	}
	sorted := append([]float64{}, scores...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	// we want the top targetRate fraction to escalate
	idx := int(targetRate * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// CalibrateForQuality returns the HIGHEST threshold (fewest escalations, lowest
// cost) whose achieved quality retention on the gold set is >= target. Requires
// dual-arm gold. Falls back to always-escalate if the target is unreachable.
func CalibrateForQuality(scores []float64, gold []schema.GoldRow, targetRetention float64) float64 {
	curve := CostQualityCurve(scores, gold)
	// quality retention denominator = all-frontier quality
	var qFront float64
	for _, r := range gold {
		qFront += float64(r.OutcomeFrontier)
	}
	if qFront == 0 {
		return 0.5
	}
	// CostQualityCurve is sorted by threshold ascending; higher threshold =>
	// fewer escalations. Walk from highest threshold down; return the first
	// (highest) threshold meeting the target.
	qFrontPerRow := qFront / float64(len(gold))
	best := -0.0001 // always escalate (max quality) fallback
	for i := len(curve) - 1; i >= 0; i-- {
		retention := curve[i].Quality / qFrontPerRow
		if retention >= targetRetention {
			best = curve[i].Threshold
			break
		}
	}
	return best
}

// QuotaGate enforces a hard cap on the fraction of requests escalated to the
// frontier, on top of a score threshold. It approximates the "user's frontier
// quota" constraint: escalate only if the score clears the threshold AND budget
// remains.
type QuotaGate struct {
	Threshold   float64
	MaxFraction float64 // e.g. 0.3 => at most 30% of requests may escalate
	total       int
	escalated   int
}

// NewQuotaGate makes a gate for a given threshold and budget fraction.
func NewQuotaGate(threshold, maxFraction float64) *QuotaGate {
	return &QuotaGate{Threshold: threshold, MaxFraction: maxFraction}
}

// Decide returns true to escalate for a request with the given score, honoring
// both the threshold and the running quota.
func (q *QuotaGate) Decide(score float64) bool {
	q.total++
	if score < q.Threshold {
		return false
	}
	// budget so far (allow if we're under the running cap)
	if float64(q.escalated+1) <= q.MaxFraction*float64(q.total) {
		q.escalated++
		return true
	}
	return false
}

// Stats returns totals for logging.
func (q *QuotaGate) Stats() (total, escalated int) { return q.total, q.escalated }
