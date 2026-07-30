// Package gold builds the dual-arm gold benchmark (Pillar 3): a fixed set
// where BOTH arms' outcomes (local and frontier) are known. This is the only
// source that yields trustworthy ABSOLUTE cost/quality numbers.
//
// Outcomes are obtained by actually calling each arm and judging both
// responses (real backend). If the frontier/judge backend is unavailable or a
// spend cap is hit, it falls back to a SYNTHETIC dual-arm set drawn from the
// same 1PL model the log generator uses — so the eval harness runs anywhere.
// A clear seam (GoldRow.Executable) is left to later populate outcomes with
// EXECUTED unit-test pass/fail for real coding tasks.
package gold

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/generate"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// price weights convert tokens to relative cost units. The frontier rung is
// intentionally ~15x the local rung (a representative $/token ratio); the
// absolute scale is irrelevant, only the ratio drives the cost/quality curve.
const (
	priceLocal    = 1.0
	priceFrontier = 15.0
)

// Backend is the slice of the model backend gold generation needs.
type Backend interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Generate(ctx context.Context, model string, msgs []backend.Message) (string, error)
	Judge(ctx context.Context, prompt, response string) (backend.JudgeResult, error)
	AnthropicAvailable() bool
}

// Meta describes how a gold set was produced.
type Meta struct {
	Synthetic     bool   `json:"synthetic"`
	LocalModel    string `json:"local_model"`
	FrontierModel string `json:"frontier_model"`
	N             int    `json:"n"`
	Reason        string `json:"reason,omitempty"`
}

// Generate builds the gold set. localArm defaults to the first local model.
func Generate(ctx context.Context, cfg config.Config, be Backend) ([]schema.GoldRow, Meta, error) {
	localArm := cfg.LocalModels[0]
	frontierArm := cfg.FrontierModel
	prompts := generate.SamplePrompts(cfg.NumGoldRows, cfg.Seed)

	meta := Meta{LocalModel: localArm, FrontierModel: frontierArm, N: len(prompts)}

	// Decide real vs synthetic up front so a gold set is internally consistent.
	if !be.AnthropicAvailable() {
		meta.Synthetic = true
		meta.Reason = "anthropic backend unavailable; using synthetic dual-arm outcomes"
		return synthetic(cfg, prompts, localArm, frontierArm), meta, nil
	}

	var rows []schema.GoldRow
	for i, p := range prompts {
		id := fmt.Sprintf("gold-%03d", i)
		emb, _ := be.Embed(ctx, p.Prompt)
		msgs := []backend.Message{{Role: "user", Content: p.Prompt}}

		localResp, lerr := be.Generate(ctx, localArm, msgs)
		frontResp, ferr := be.Generate(ctx, frontierArm, msgs)
		if ferr != nil {
			// Frontier unavailable/capped mid-run: fall back to synthetic for
			// the whole set to keep it consistent.
			meta.Synthetic = true
			meta.Reason = "frontier generation failed/capped (" + ferr.Error() + "); using synthetic outcomes"
			return synthetic(cfg, prompts, localArm, frontierArm), meta, nil
		}
		var lj, fj backend.JudgeResult
		if lerr == nil {
			lj, _ = be.Judge(ctx, p.Prompt, localResp)
		}
		fj, _ = be.Judge(ctx, p.Prompt, frontResp)

		rows = append(rows, schema.GoldRow{
			PromptID:        id,
			PromptText:      p.Prompt,
			Features:        feature.Extract(p.Prompt, "open"),
			Embedding:       emb,
			OutcomeLocal:    b2i(lj.Adequate && lerr == nil),
			OutcomeFrontier: b2i(fj.Adequate),
			LocalModel:      localArm,
			FrontierModel:   frontierArm,
			CostLocal:       cost(p.Prompt, localResp, priceLocal),
			CostFrontier:    cost(p.Prompt, frontResp, priceFrontier),
			Executable:      false, // seam: real unit-test outcomes go here
		})
	}
	return rows, meta, nil
}

// synthetic plants dual-arm outcomes from the 1PL model. Deterministic (seeded)
// so the benchmark is stable across runs.
func synthetic(cfg config.Config, prompts []struct {
	Prompt     string
	Difficulty float64
}, localArm, frontierArm string) []schema.GoldRow {
	ab := generate.Abilities(cfg)
	r := rand.New(rand.NewSource(cfg.Seed + 202))
	var rows []schema.GoldRow
	for i, p := range prompts {
		b := p.Difficulty + r.NormFloat64()*0.2
		pl := sigmoid(ab[localArm] - b)
		pf := sigmoid(ab[frontierArm] - b)
		// approximate response length so costs vary a little
		respTok := 120 + r.Intn(200)
		rows = append(rows, schema.GoldRow{
			PromptID:        fmt.Sprintf("gold-%03d", i),
			PromptText:      p.Prompt,
			Features:        feature.Extract(p.Prompt, "open"),
			OutcomeLocal:    b2i(r.Float64() < pl),
			OutcomeFrontier: b2i(r.Float64() < pf),
			LocalModel:      localArm,
			FrontierModel:   frontierArm,
			CostLocal:       float64(respTok) * priceLocal,
			CostFrontier:    float64(respTok) * priceFrontier,
			Executable:      false,
		})
	}
	return rows
}

func cost(prompt, resp string, price float64) float64 {
	toks := (len(prompt)+len(resp))/4 + 1
	return float64(toks) * price
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Save writes the gold set and its meta to DataDir.
func Save(cfg config.Config, rows []schema.GoldRow, meta Meta) error {
	path := filepath.Join(cfg.DataDir, "gold.jsonl")
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
	mb, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(cfg.DataDir, "gold_meta.json"), mb, 0o644)
}

// Load reads a gold set from DataDir.
func Load(cfg config.Config) ([]schema.GoldRow, error) {
	f, err := os.Open(filepath.Join(cfg.DataDir, "gold.jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []schema.GoldRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		var r schema.GoldRow
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			rows = append(rows, r)
		}
	}
	return rows, sc.Err()
}
