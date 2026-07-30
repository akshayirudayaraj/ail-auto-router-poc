package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/numerics"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// Backend is the slice of the model backend extraction needs.
type Backend interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Judge(ctx context.Context, prompt, response string) (backend.JudgeResult, error)
}

// Result summarizes an extraction run.
type Result struct {
	Observations int
	ImplicitRows int
	JudgeRows    int
	PairwiseRows int
	EmbedFail    int
	JudgeFail    int
	PointwisePath string
	PairwisePath  string
}

// Run executes the full label engine: reconstruct sessions, mine implicit
// signals, embed prompts, judge a sample, derive pairwise data, and write the
// structured datasets. It reads logs STRIPPED of ground truth.
func Run(ctx context.Context, cfg config.Config, be Backend) (Result, error) {
	var res Result
	logPath := filepath.Join(cfg.DataDir, "raw_logs.jsonl")
	turns, err := LoadRaw(logPath, true /* strip hidden */)
	if err != nil {
		return res, fmt.Errorf("load logs: %w", err)
	}
	// Defense in depth: refuse to proceed if any hidden field survived.
	for _, t := range turns {
		if t.HasHidden() {
			return res, fmt.Errorf("extract: hidden ground-truth field leaked into extraction input (bug)")
		}
	}

	sessions := Reconstruct(turns)
	obs := Observations(sessions)
	res.Observations = len(obs)

	isFrontier := frontierPredicate(cfg)

	// --- embeddings (dedup by prompt text to save calls) ---
	embByText := map[string][]float32{}
	for _, o := range obs {
		if _, ok := embByText[o.Prompt]; ok {
			continue
		}
		emb, err := be.Embed(ctx, o.Prompt)
		if err != nil {
			res.EmbedFail++
			embByText[o.Prompt] = nil // cache the failure so we don't retry each row
			continue
		}
		embByText[o.Prompt] = emb
	}

	// --- implicit rows (every observation) ---
	var rows []schema.PointwiseRow
	for _, o := range obs {
		lab := InferSignal(o, isFrontier)
		rows = append(rows, schema.PointwiseRow{
			PromptID:        o.PromptID,
			PromptText:      o.Prompt,
			Features:        feature.Extract(o.Prompt, o.TurnType),
			Embedding:       embByText[o.Prompt],
			Model:           o.Model,
			Outcome:         lab.Outcome,
			LabelSource:     schema.LabelImplicit,
			LabelConfidence: lab.Confidence,
			SessionID:       o.SessionID,
			TurnIndex:       o.TurnIndex,
			Timestamp:       o.Timestamp,
			Propensity:      o.Propensity,
		})
	}
	res.ImplicitRows = len(rows)

	// --- judge labels on a seeded sample (judging is expensive) ---
	sampleIdx := sampleIndices(len(obs), cfg.JudgeSample, cfg.Seed)
	for _, i := range sampleIdx {
		o := obs[i]
		jr, err := be.Judge(ctx, o.Prompt, o.Response)
		if err != nil {
			res.JudgeFail++
			continue
		}
		outcome := 0
		if jr.Adequate {
			outcome = 1
		}
		conf := jr.Score
		if conf <= 0 {
			conf = 0.5
		}
		rows = append(rows, schema.PointwiseRow{
			PromptID:        o.PromptID,
			PromptText:      o.Prompt,
			Features:        feature.Extract(o.Prompt, o.TurnType),
			Embedding:       embByText[o.Prompt],
			Model:           o.Model,
			Outcome:         outcome,
			LabelSource:     schema.LabelJudge,
			LabelConfidence: conf,
			SessionID:       o.SessionID,
			TurnIndex:       o.TurnIndex,
			Timestamp:       o.Timestamp,
			Propensity:      o.Propensity,
		})
		res.JudgeRows++
	}

	// --- pairwise derivation ---
	pairs := DerivePairwise(cfg, sessions, obs, embByText, isFrontier)
	res.PairwiseRows = len(pairs)

	// --- write datasets ---
	res.PointwisePath = filepath.Join(cfg.DataDir, "pointwise.jsonl")
	if err := writeJSONL(res.PointwisePath, rows); err != nil {
		return res, err
	}
	res.PairwisePath = filepath.Join(cfg.DataDir, "pairwise.jsonl")
	if err := writeJSONL(res.PairwisePath, pairs); err != nil {
		return res, err
	}
	return res, nil
}

// frontierPredicate returns a function classifying a model as the frontier rung.
func frontierPredicate(cfg config.Config) func(string) bool {
	front := map[string]bool{cfg.FrontierModel: true, cfg.JudgeModel: true}
	return func(m string) bool {
		return front[m] || strings.HasPrefix(m, "claude")
	}
}

// DerivePairwise builds pairwise preferences (RouteLLM's data shape) from
// pointwise logs. Real logs are censored — each prompt is served by only one
// model — so we recover pairs two ways, both source=implicit:
//
//  1. Session-local escalation: a local turn followed by a frontier turn on
//     the same task where the local answer was inferred inadequate. The
//     frontier model is preferred. This is the cleanest natural pair.
//  2. Nearest-neighbor cross-model matching: for each observation, its closest
//     other-model observation by embedding cosine forms an approximate pair;
//     the one with the better inferred outcome is preferred (tie if equal).
//     This is an APPROXIMATION to get training volume and is documented as such.
func DerivePairwise(cfg config.Config, sessions []Session, obs []ServedObs,
	embByText map[string][]float32, isFrontier func(string) bool) []schema.PairwiseRow {

	var pairs []schema.PairwiseRow
	seen := map[string]bool{}

	// (1) escalation pairs
	for _, s := range sessions {
		var lastAssist *ServedObs
		for i := range s.Turns {
			t := s.Turns[i]
			if t.Role != schema.RoleAssistant {
				continue
			}
			cur := findObs(obs, s.ID, t.TurnIndex)
			if cur == nil {
				continue
			}
			if lastAssist != nil && !isFrontier(lastAssist.Model) && isFrontier(cur.Model) {
				// escalation: prefer the frontier model on the earlier prompt
				pairs = append(pairs, schema.PairwiseRow{
					PromptID:   lastAssist.PromptID,
					PromptText: lastAssist.Prompt,
					Features:   feature.Extract(lastAssist.Prompt, lastAssist.TurnType),
					Embedding:  embByText[lastAssist.Prompt],
					ModelA:     lastAssist.Model,
					ModelB:     cur.Model,
					Preferred:  "b",
					Source:     schema.LabelImplicit,
				})
			}
			c := *cur
			lastAssist = &c
		}
	}

	// (2) nearest-neighbor cross-model approximation
	for i := range obs {
		best := -1
		bestSim := 0.30 // similarity floor; below this a pair is meaningless
		ei := embByText[obs[i].Prompt]
		if ei == nil {
			continue
		}
		for j := range obs {
			if i == j || obs[j].Model == obs[i].Model {
				continue
			}
			ej := embByText[obs[j].Prompt]
			if ej == nil {
				continue
			}
			if sim := numerics.Cosine(ei, ej); sim > bestSim {
				bestSim = sim
				best = j
			}
		}
		if best < 0 {
			continue
		}
		key := pairKey(obs[i].PromptID, obs[best].PromptID)
		if seen[key] {
			continue
		}
		seen[key] = true
		li := InferSignal(obs[i], isFrontier)
		lj := InferSignal(obs[best], isFrontier)
		pref := "tie"
		if li.Outcome > lj.Outcome {
			pref = "a"
		} else if lj.Outcome > li.Outcome {
			pref = "b"
		}
		pairs = append(pairs, schema.PairwiseRow{
			PromptID:   obs[i].PromptID,
			PromptText: obs[i].Prompt,
			Features:   feature.Extract(obs[i].Prompt, obs[i].TurnType),
			Embedding:  ei,
			ModelA:     obs[i].Model,
			ModelB:     obs[best].Model,
			Preferred:  pref,
			Source:     schema.LabelImplicit,
		})
	}
	return pairs
}

func findObs(obs []ServedObs, sid string, turnIdx int) *ServedObs {
	for i := range obs {
		if obs[i].SessionID == sid && obs[i].TurnIndex == turnIdx {
			return &obs[i]
		}
	}
	return nil
}

func pairKey(a, b string) string {
	if a < b {
		return a + "|" + b
	}
	return b + "|" + a
}

// sampleIndices returns up to k distinct indices in [0,n), seeded.
func sampleIndices(n, k int, seed int64) []int {
	if k >= n {
		k = n
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	r := rand.New(rand.NewSource(seed + 7))
	r.Shuffle(n, func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	out := idx[:k]
	sort.Ints(out)
	return out
}

func writeJSONL[T any](path string, rows []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}
