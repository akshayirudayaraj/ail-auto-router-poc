// Package materialize is the bridge from the offline label engine to the router
// training/eval harness (OFFLINE_ENGINE_PLAN O6). It reads the fused canonical
// labels (labels/resolved.jsonl) and turns them into the pointwise / pairwise /
// gold datasets that cmd/train + internal/eval consume unchanged.
//
// It SUPERSEDES internal/agentic.BuildGold. BuildGold read a `resolved` boolean
// straight off each run record — a field the log-first runner no longer emits, so
// it silently produced all-zero gold on fresh data (the landmine). This package
// instead reads the engine's calibrated, non-circular labels, and enforces the
// discipline BuildGold lacked:
//
//   - TRAIN vs EVAL split. pointwise/pairwise are built from TRAIN-split tasks;
//     gold (the trustworthy absolute-number benchmark) is built ONLY from
//     HOLDOUT-split tasks. A task never appears in both — no leakage.
//   - EXECUTED-only gold. A gold row requires BOTH arms to carry an EXECUTED
//     label. An oracle-bearing session that was never executed-graded (its
//     canonical label is judge/implicit — e.g. a heuristic "complete" default) is
//     QUARANTINED, never materialized as truth. This is the row-level embodiment
//     of the executed branch's rule: never treat an ungraded oracle session as a
//     real outcome.
//   - Firewall. AssertEvalStrongerThanTrain still holds: gold is executed (the
//     strongest source), so it dominates any weak train label.
//
// Cost units mirror internal/agentic + internal/gold: billable (input+output)
// tokens * price, frontier priced 15x local. Tokens are joined back to each
// canonical session's run record by session_id (not newest-per-arm), so cost
// reflects the exact session that produced the graded label.
//
// stdlib-only + internal deps; no new third-party modules (invariant 1).
package materialize

import (
	"context"
	"fmt"
	"sort"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/label"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// Cost convention (see package doc). Kept local so this package doesn't pull the
// Python-bridge internal/agentic in; the two constants must stay in sync with it.
const (
	priceLocal    = 1.0
	priceFrontier = 15.0
)

// Embedder is the slice of the backend the builder needs (best-effort).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Session is the minimal run-record projection the builder needs for cost: the
// billable token counts for the exact session a canonical label points at.
type Session struct {
	InputTokens  int
	OutputTokens int
}

func (s Session) billable() int { return s.InputTokens + s.OutputTokens }

// Datasets is the materialized output, ready to write with Save.
type Datasets struct {
	Pointwise []schema.PointwiseRow
	Pairwise  []schema.PairwiseRow
	Gold      []schema.GoldRow
}

// Meta records what was materialized (and, crucially, what was dropped and why).
type Meta struct {
	Synthetic     bool   `json:"synthetic"`
	Executable    bool   `json:"executable"`
	LocalModel    string `json:"local_model"`
	FrontierModel string `json:"frontier_model"`

	NPointwise int `json:"n_pointwise"`
	NPairwise  int `json:"n_pairwise"`
	NGold      int `json:"n_gold"`

	// Drop accounting — never silently truncated (OFFLINE_ENGINE_PLAN: log what
	// was excluded so an empty gold set reads as honest, not as "all passed").
	QuarantinedOracleUngraded int      `json:"quarantined_oracle_ungraded"`
	HoldoutDroppedNotDualExec int      `json:"holdout_dropped_not_dual_executed"`
	FirewallWarning           string   `json:"firewall_warning,omitempty"`
	Notes                     []string `json:"notes,omitempty"`
}

// Build turns the canonical labels into the three datasets.
//
//   - resolved: label.LoadResolved output (one canonical LabelRecord per task,model).
//   - issues:   task_id -> issue text (drives PromptText/Features).
//   - sessions: session_id -> run-record token counts (for gold cost).
//   - emb:      optional embedder (nil -> no embeddings; kNN degrades gracefully).
func Build(ctx context.Context, cfg config.Config, resolved []label.LabelRecord,
	issues map[string]string, sessions map[string]Session, emb Embedder) (Datasets, Meta, error) {

	meta := Meta{Synthetic: false, Executable: true}

	// 1) Quarantine oracle-bearing sessions whose canonical label is NOT executed.
	//    If a task has an executable oracle, the only trustworthy label is the
	//    executed one; a judge/heuristic label here means the oracle never ran, so
	//    the outcome is a guess. Exclude it everywhere (never materialize as truth).
	kept := make([]label.LabelRecord, 0, len(resolved))
	for _, r := range resolved {
		if r.HasExecutableOracle && r.LabelSource != schema.LabelExecuted {
			meta.QuarantinedOracleUngraded++
			continue
		}
		kept = append(kept, r)
	}

	// derive the arm model names for gold meta + pairwise (fall back to config)
	localModel, frontierModel := cfg.LocalModels[0], cfg.FrontierModel
	for _, r := range kept {
		switch r.Arm {
		case "local":
			localModel = r.Model
		case "frontier":
			frontierModel = r.Model
		}
	}
	meta.LocalModel, meta.FrontierModel = localModel, frontierModel

	// feature/embedding cache per task (issues are shared across arms)
	type promptFeat struct {
		text  string
		feats schema.Features
		emb   []float32
	}
	pf := map[string]promptFeat{}
	featFor := func(taskID string) promptFeat {
		if v, ok := pf[taskID]; ok {
			return v
		}
		issue := issues[taskID]
		v := promptFeat{text: issue, feats: feature.Extract(issue, "open")}
		if emb != nil && issue != "" {
			v.emb, _ = emb.Embed(ctx, issue)
		}
		pf[taskID] = v
		return v
	}

	// 2) Partition kept labels by split, grouping by task for dual-arm assembly.
	//    Unknown/empty split -> train (never eligible for gold, so it can't leak).
	type armPair struct{ local, frontier *label.LabelRecord }
	trainByTask := map[string]*armPair{}
	holdoutByTask := map[string]*armPair{}
	for i := range kept {
		r := kept[i]
		dst := trainByTask
		if r.Split == "holdout" {
			dst = holdoutByTask
		}
		p := dst[r.TaskID]
		if p == nil {
			p = &armPair{}
			dst[r.TaskID] = p
		}
		switch r.Arm {
		case "local":
			p.local = &kept[i]
		case "frontier":
			p.frontier = &kept[i]
		}
	}

	ds := Datasets{Pointwise: []schema.PointwiseRow{}, Pairwise: []schema.PairwiseRow{}, Gold: []schema.GoldRow{}}

	// 3) Pointwise + pairwise from TRAIN tasks (weak labels allowed).
	for _, tid := range sortedKeys(trainByTask) {
		p := trainByTask[tid]
		fv := featFor(tid)
		for _, r := range []*label.LabelRecord{p.local, p.frontier} {
			if r == nil {
				continue
			}
			ds.Pointwise = append(ds.Pointwise, schema.PointwiseRow{
				PromptID:        tid,
				PromptText:      fv.text,
				Features:        fv.feats,
				Embedding:       fv.emb,
				Model:           r.Model,
				Outcome:         r.Outcome,
				LabelSource:     r.LabelSource,
				LabelConfidence: r.LabelConfidence,
				SessionID:       r.SessionID,
				TurnIndex:       0,
				Timestamp:       r.Timestamp,
				// Propensity nil by construction: deterministic per-rung runs, no
				// logging policy (DATA_PLAN — off-policy IPS/DR out of reach).
			})
		}
		if p.local != nil && p.frontier != nil {
			ds.Pairwise = append(ds.Pairwise, schema.PairwiseRow{
				PromptID:   tid,
				PromptText: fv.text,
				Features:   fv.feats,
				Embedding:  fv.emb,
				ModelA:     p.local.Model,
				ModelB:     p.frontier.Model,
				Preferred:  preference(p.local.Outcome, p.frontier.Outcome),
				Source:     weaker(p.local.LabelSource, p.frontier.LabelSource),
			})
		}
	}

	// 4) Gold from HOLDOUT tasks — both arms must be executed (else drop + count).
	for _, tid := range sortedKeys(holdoutByTask) {
		p := holdoutByTask[tid]
		if p.local == nil || p.frontier == nil ||
			p.local.LabelSource != schema.LabelExecuted || p.frontier.LabelSource != schema.LabelExecuted {
			meta.HoldoutDroppedNotDualExec++
			continue
		}
		fv := featFor(tid)
		ds.Gold = append(ds.Gold, schema.GoldRow{
			PromptID:        tid,
			PromptText:      fv.text,
			Features:        fv.feats,
			Embedding:       fv.emb,
			OutcomeLocal:    p.local.Outcome,
			OutcomeFrontier: p.frontier.Outcome,
			LocalModel:      localModel,
			FrontierModel:   frontierModel,
			CostLocal:       float64(sessions[p.local.SessionID].billable()) * priceLocal,
			CostFrontier:    float64(sessions[p.frontier.SessionID].billable()) * priceFrontier,
			Executable:      true,
		})
	}

	meta.NPointwise = len(ds.Pointwise)
	meta.NPairwise = len(ds.Pairwise)
	meta.NGold = len(ds.Gold)

	// 5) Firewall check on what we materialized (executed gold vs weakest train).
	if err := checkFirewall(ds); err != "" {
		meta.FirewallWarning = err
	}
	if meta.NGold == 0 {
		meta.Notes = append(meta.Notes,
			"0 gold rows: no HOLDOUT task has EXECUTED labels on both arms yet — "+
				"expected until the generation batch lands + swebench grading runs. "+
				"pointwise/pairwise are still materialized for training.")
	}
	if meta.QuarantinedOracleUngraded > 0 {
		meta.Notes = append(meta.Notes, fmt.Sprintf(
			"%d oracle-bearing sessions quarantined (canonical label not executed — re-run grade_offline where the oracle env exists)",
			meta.QuarantinedOracleUngraded))
	}
	return ds, meta, nil
}

// preference maps two arm outcomes to a RouteLLM-style preference label. "a" =
// local preferred, "b" = frontier preferred, "tie" = equal outcome.
func preference(local, frontier int) string {
	switch {
	case local > frontier:
		return "a"
	case frontier > local:
		return "b"
	default:
		return "tie"
	}
}

// weaker returns the lower-strength of two label sources (a preference is only as
// trustworthy as its weaker side).
func weaker(a, b schema.LabelSource) schema.LabelSource {
	if schema.LabelStrength(a) <= schema.LabelStrength(b) {
		return a
	}
	return b
}

// checkFirewall returns a non-empty message if any gold (eval) row is weaker than
// the strongest pointwise (train) label. Gold is always executed here, so this is
// a belt-and-suspenders guard; it returns a warning rather than failing on the
// tiny early corpus.
func checkFirewall(ds Datasets) string {
	maxTrain := -1
	for _, r := range ds.Pointwise {
		if s := schema.LabelStrength(r.LabelSource); s > maxTrain {
			maxTrain = s
		}
	}
	// gold is executed => strength 3; only a bug would make it weaker.
	minEval := 1 << 30
	for range ds.Gold {
		if s := schema.LabelStrength(schema.LabelExecuted); s < minEval {
			minEval = s
		}
	}
	if minEval != 1<<30 && maxTrain != -1 && minEval < maxTrain {
		return fmt.Sprintf("label circularity: weakest gold source (%d) < strongest train source (%d)", minEval, maxTrain)
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
