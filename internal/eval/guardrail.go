package eval

import (
	"context"
	"fmt"

	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
)

// Embedder is the slice of the backend the guardrail probes need (optional).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// GuardrailSuite is a standing check made of matched perturbation probes:
//
//   - DIFFICULTY MONOTONICITY: matched easy-vs-hard pairs on the SAME task —
//     the escalation score MUST rise with difficulty.
//   - TOPIC-COLLAPSE / keyword injection: injecting off-topic words that a
//     naive topic classifier would react to MUST NOT flip the routing decision
//     — proving the router keys on difficulty, not topic (our traffic is all
//     code, so a topic signal is worthless).
//
// It reports per-router pass rates; a healthy router scores ~1.0 on monotonicity
// and ~0.0 flip rate on injection.
type GuardrailSuite struct {
	Threshold float64
}

func NewGuardrailSuite() *GuardrailSuite { return &GuardrailSuite{Threshold: 0.5} }

func (g *GuardrailSuite) Name() string { return "guardrail-suite" }

// probePair is a matched easy/hard pair for the same underlying task.
var probeBases = []struct {
	easy string
	hard string
}{
	{
		easy: "Write a Go function to add two integers and return the sum.",
		hard: "Implement a lock-free, thread-safe concurrent counter in Go using atomic compare-and-swap, race-free under -race, with correct memory ordering.",
	},
	{
		easy: "Reverse a string in Go.",
		hard: "Implement a streaming UTF-8 aware string reverser that handles grapheme clusters and combining marks correctly across a distributed pipeline with backpressure.",
	},
	{
		easy: "Parse a small JSON object and print one field.",
		hard: "Write a zero-allocation streaming JSON parser in Go with a hand-rolled state machine, handling deeply nested recursion without stack overflow.",
	},
	{
		easy: "Add a --verbose flag to a CLI.",
		hard: "Design an idempotent, transactional schema migration with online backfill, avoiding long locks under concurrent writes and preserving consistency invariants.",
	},
	{
		easy: "Sum a slice of integers.",
		hard: "Implement leader election for a Raft cluster with randomized timeouts, term bookkeeping, and the election-safety invariant, race-free.",
	},
}

// injections are off-topic phrases a topic classifier might latch onto but that
// carry no real difficulty. Injecting them must not flip the decision.
var injections = []string{
	" (this is for a cooking blog about sourdough recipes)",
	" — context: we also run a philosophy podcast and a gardening newsletter",
	" btw the company mascot is a penguin named Steve",
	" note: unrelated to sports, astrology, or medieval history",
}

func (g *GuardrailSuite) Run(routers []router.Router, d Data) (Report, error) {
	rep := Report{Method: g.Name()}

	makeInst := func(text string) router.Instance {
		inst := router.Instance{Features: feature.Extract(text, "open")}
		if d.Embedder != nil && d.Ctx != nil {
			if e, err := d.Embedder.Embed(d.Ctx, text); err == nil {
				inst.Embedding = e
			}
		}
		return inst
	}

	// pre-embed probes once
	type pair struct{ easy, hard router.Instance }
	var pairs []pair
	for _, b := range probeBases {
		pairs = append(pairs, pair{makeInst(b.easy), makeInst(b.hard)})
	}
	type inj struct {
		base     router.Instance
		injected []router.Instance
	}
	var injs []inj
	for _, b := range probeBases {
		it := inj{base: makeInst(b.hard)}
		for _, s := range injections {
			it.injected = append(it.injected, makeInst(b.hard+s))
		}
		injs = append(injs, it)
	}

	for _, r := range routers {
		if err := r.Fit(TrainDataFrom(d, "")); err != nil {
			return rep, fmt.Errorf("fit %s: %w", r.Name(), err)
		}
		// monotonicity: hard score should exceed easy score
		monoPass := 0
		for _, p := range pairs {
			if r.Score(p.hard) > r.Score(p.easy) {
				monoPass++
			}
		}
		monoRate := float64(monoPass) / float64(len(pairs))

		// topic-collapse: decision must not flip under injection
		var total, flips int
		for _, it := range injs {
			base := r.Decide(it.base, g.Threshold)
			for _, ij := range it.injected {
				total++
				if r.Decide(ij, g.Threshold) != base {
					flips++
				}
			}
		}
		flipRate := 0.0
		if total > 0 {
			flipRate = float64(flips) / float64(total)
		}

		rep.Rows = append(rep.Rows, ReportRow{
			Router: r.Name(),
			Metrics: map[string]float64{
				"difficulty_monotonicity": monoRate, // want 1.0
				"topic_flip_rate":         flipRate, // want 0.0
			},
		})
	}
	rep.Notes = append(rep.Notes,
		"difficulty_monotonicity: fraction of easy/hard pairs where score rises with difficulty (want 1.0).",
		"topic_flip_rate: fraction of off-topic keyword injections that flipped the decision (want 0.0 — the topic-collapse guardrail).",
	)
	return rep, nil
}

var _ EvalMethod = (*GuardrailSuite)(nil)
