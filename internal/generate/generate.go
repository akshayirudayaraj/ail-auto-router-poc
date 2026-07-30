// Package generate produces realistic Claude-Code-style session logs (Pillar
// 1a) as JSONL. It is the ONLY synthetic piece: when real logs arrive it is
// swapped out and everything downstream is reused unchanged.
//
// The generative model of outcomes is deliberately a 1PL IRT model:
//
//	P(adequate | model m, prompt i) = sigmoid(theta_m - b_i)
//
// so that (a) adequacy is genuinely model-relative, (b) prompt difficulty b_i
// correlates with observable features, giving a predictive router something to
// learn, and (c) the IRT router's parameter recovery can be checked against the
// planted theta/b. Ground truth (adequacy, difficulty, the implicit signal
// each user turn expresses) is written to underscore-prefixed fields, consumed
// only by the extractor-quality report.
package generate

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// TruthParams is the planted ground truth for the whole run, written to a
// sidecar (data/_truth.json). Underscore prefix marks it as grader-only.
type TruthParams struct {
	Abilities   map[string]float64 `json:"abilities"`  // theta_m
	BaseModel   string             `json:"base_model"` // logging-policy default
	Epsilon     float64            `json:"epsilon"`
	NumSessions int                `json:"num_sessions"`
}

// Generator holds seeded state.
type Generator struct {
	cfg       config.Config
	rng       *rand.Rand
	models    []string
	base      string
	abilities map[string]float64
}

// New builds a seeded generator.
func New(cfg config.Config) *Generator {
	g := &Generator{
		cfg:    cfg,
		rng:    rand.New(rand.NewSource(cfg.Seed)),
		models: cfg.AllModels(),
	}
	g.base = g.models[0]
	g.abilities = assignAbilities(cfg)
	return g
}

// Abilities exposes the planted per-model ability (theta) map for a config.
// Used by the gold-set generator's synthetic fallback and by IRT recovery
// tests. It is ground truth about the generative model, not extracted data.
func Abilities(cfg config.Config) map[string]float64 { return assignAbilities(cfg) }

// SamplePrompts returns n dense, code-heavy prompts spread across difficulty
// tiers, for building the dual-arm gold benchmark. Seeded and deterministic.
func SamplePrompts(n int, seed int64) []struct {
	Prompt     string
	Difficulty float64
} {
	r := rand.New(rand.NewSource(seed + 101))
	out := make([]struct {
		Prompt     string
		Difficulty float64
	}, 0, n)
	for i := 0; i < n; i++ {
		tk := taskBank[r.Intn(len(taskBank))]
		out = append(out, struct {
			Prompt     string
			Difficulty float64
		}{Prompt: tk.OpenPrompt, Difficulty: tk.BaseDifficulty})
	}
	return out
}

// assignAbilities plants a theta per model: frontier highest; among locals a
// code-specialized model (name contains "coder") outranks a general one. The
// reference model (index 0) is pinned to 0 for IRT identifiability parity.
func assignAbilities(cfg config.Config) map[string]float64 {
	ab := map[string]float64{}
	for i, m := range cfg.LocalModels {
		theta := 0.0
		if strings.Contains(strings.ToLower(m), "coder") {
			theta = 1.1
		}
		theta += 0.1 * float64(i) // mild tie-break so models aren't identical
		ab[m] = theta
	}
	ab[cfg.FrontierModel] = 2.6
	return ab
}

// Run generates all sessions and writes them to data/raw_logs.jsonl, plus the
// ground-truth sidecar. Returns the path written and turn count.
func (g *Generator) Run() (string, int, error) {
	if err := os.MkdirAll(g.cfg.DataDir, 0o755); err != nil {
		return "", 0, err
	}
	path := filepath.Join(g.cfg.DataDir, "raw_logs.jsonl")
	f, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	enc := json.NewEncoder(f)

	// Spread sessions over ~30 days ending now, then sort by start time so the
	// log is chronological (temporal backtest splits on this).
	now := time.Now().Unix()
	window := int64(30 * 24 * 3600)
	starts := make([]int64, g.cfg.NumSessions)
	for i := range starts {
		starts[i] = now - window + int64(g.rng.Float64()*float64(window))
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })

	total := 0
	for s := 0; s < g.cfg.NumSessions; s++ {
		turns := g.genSession(fmt.Sprintf("s%04d", s), starts[s])
		for _, t := range turns {
			if err := enc.Encode(t); err != nil {
				return "", 0, err
			}
			total++
		}
	}

	// ground-truth sidecar (grader-only)
	truth := TruthParams{
		Abilities:   g.abilities,
		BaseModel:   g.base,
		Epsilon:     g.cfg.EpsilonGreedy,
		NumSessions: g.cfg.NumSessions,
	}
	tb, _ := json.MarshalIndent(truth, "", "  ")
	_ = os.WriteFile(filepath.Join(g.cfg.DataDir, "_truth.json"), tb, 0o644)

	return path, total, nil
}

// genSession produces the ordered turns for one session working one task.
func (g *Generator) genSession(sessionID string, start int64) []schema.RawTurn {
	tk := taskBank[g.rng.Intn(len(taskBank))]
	numAssist := 2 + g.rng.Intn(4) // 2..5 assistant turns
	var turns []schema.RawTurn
	ts := start
	turnIdx := 0
	subIdx := 0

	// forcedModel != "" means the previous user turn was a user-driven switch
	// to the frontier (deterministic action, propensity 1).
	forcedModel := ""

	// opening dense task-stating user prompt
	turns = append(turns, schema.RawTurn{
		SessionID: sessionID, TurnIndex: turnIdx, Timestamp: ts,
		Role: schema.RoleUser, Content: tk.OpenPrompt, TrueTopic: "code",
	})
	turnIdx++
	ts += 20 + int64(g.rng.Intn(120))

	for a := 0; a < numAssist; a++ {
		// choose serving model
		var model string
		var prop float64
		if forcedModel != "" {
			model, prop = forcedModel, 1.0
			forcedModel = ""
		} else {
			model, prop = g.policyChoose()
		}

		// difficulty for this step: base task difficulty + small per-turn noise,
		// with follow-up sub-tasks drifting slightly harder.
		b := tk.BaseDifficulty + 0.15*float64(a) + g.rng.NormFloat64()*0.25
		padq := sigmoid(g.abilities[model] - b)
		adq := g.rng.Float64() < padq

		turns = append(turns, schema.RawTurn{
			SessionID: sessionID, TurnIndex: turnIdx, Timestamp: ts,
			Role: schema.RoleAssistant, Content: g.renderResponse(tk, adq, subIdx),
			ServedModel: model, Propensity: fptr(prop),
			TrueAdequate: bptr(adq), TrueDifficulty: fptr(b), TrueTopic: "code",
		})
		turnIdx++
		ts += 15 + int64(g.rng.Intn(90))

		if a == numAssist-1 {
			break // session ends after last assistant turn
		}

		// the following user turn: signal chosen from this turn's adequacy
		sig := g.chooseSignal(adq)
		if sig == sigSwitch {
			forcedModel = g.cfg.FrontierModel
		}
		content := g.renderFollowup(tk, sig, &subIdx)
		turns = append(turns, schema.RawTurn{
			SessionID: sessionID, TurnIndex: turnIdx, Timestamp: ts,
			Role: schema.RoleUser, Content: content, TrueTopic: "code", TrueSignal: string(sig),
		})
		turnIdx++
		ts += 20 + int64(g.rng.Intn(150))
	}
	return turns
}

// policyChoose is the epsilon-greedy logging policy over all models, returning
// the chosen model and its propensity. Exploitation picks the cheapest local
// (base); exploration picks uniformly. Every model keeps propensity >= eps/K so
// the log has the overlap that off-policy estimation requires.
func (g *Generator) policyChoose() (string, float64) {
	K := len(g.models)
	eps := g.cfg.EpsilonGreedy
	var chosen string
	if g.rng.Float64() < eps {
		chosen = g.models[g.rng.Intn(K)]
	} else {
		chosen = g.base
	}
	prop := eps / float64(K)
	if chosen == g.base {
		prop += 1 - eps
	}
	return chosen, prop
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }
func fptr(f float64) *float64   { return &f }
func bptr(b bool) *bool         { return &b }
