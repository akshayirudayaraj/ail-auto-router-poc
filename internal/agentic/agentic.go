// Package agentic bridges the non-portable agentic runner (agentic/, Python)
// into the portable Go framework. It reads the runner's per-(task,arm) result
// JSONs — whose outcomes come from EXECUTED unit tests inside the real Claude
// Code harness — and assembles them into the existing dual-arm GoldRow schema
// with Executable=true. The resulting gold set is consumed by the existing eval
// harness (internal/eval) UNCHANGED: dual-arm gold, AIQ, cost/quality curve,
// cell-B.
//
// This is the Go side of the Go/Python boundary (see DECISIONS D12): schema,
// scoring and integration stay in stdlib Go; the harness-driving and
// SWE-bench/Docker orchestration live under agentic/ in Python.
package agentic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/feature"
	"github.com/akshayirudayaraj/ail-routing-test/internal/resultsfs"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// Cost convention mirrors internal/gold: relative units = tokens * price with
// the frontier rung priced 15x local. We count BILLABLE (input+output) tokens
// and exclude prompt-cache re-reads — otherwise frontier's heavily-cached
// system prompt would dominate the units and distort the cost/quality curve.
// This matches gold.go's fresh-token convention.
const (
	priceLocal    = 1.0
	priceFrontier = 15.0
)

func billable(r Result) int { return r.InputTokens + r.OutputTokens }

// Result is the subset of a runner result JSON the Go side consumes.
//
// Resolved is a *bool on purpose: the log-first runner no longer grades during
// generation, so a fresh record has NO `resolved` key. A pointer distinguishes
// "absent" (nil → not yet graded) from "graded as false", which BuildGold needs
// so it can refuse to fabricate a 0 outcome (see the guard there).
type Result struct {
	TaskID              string  `json:"task_id"`
	Tier                string  `json:"tier"`
	Arm                 string  `json:"arm"`
	Model               string  `json:"model"`
	OllamaModel         string  `json:"ollama_model"`
	ServedModel         string  `json:"served_model"`          // real model behind the arm (roster source)
	HasExecutableOracle bool    `json:"has_executable_oracle"` // record carries a gradable oracle
	Resolved            *bool   `json:"resolved"`              // nil = not yet graded (offline engine)
	FailToPassOK        bool    `json:"fail_to_pass_ok"`
	PassToPassOK        bool    `json:"pass_to_pass_ok"`
	WallClockS          float64 `json:"wall_clock_s"`
	TimedOut            bool    `json:"timed_out"`
	HitTurnCap          bool    `json:"hit_turn_cap"`
	EmptyPatch          bool    `json:"empty_patch"`
	NumTurns            int     `json:"num_turns"`
	ToolCallsAttempted  int     `json:"tool_calls_attempted"`
	ToolCallsErrored    int     `json:"tool_calls_errored"`
	AnyValidToolCall    bool    `json:"any_valid_tool_call"`
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	NativeToolCalls     int     `json:"native_tool_calls"`
	RescuedToolCalls    int     `json:"rescued_tool_calls"`
	CostUnits           float64 `json:"cost_units"`
	ReportedCostUSD     float64 `json:"reported_cost_usd"`
	ResultSubtype       string  `json:"result_subtype"`
	IsError             bool    `json:"is_error"`
}

// Task is the minimal task metadata (issue text drives PromptText/Features).
type Task struct {
	ID    string `json:"id"`
	Tier  string `json:"tier"`
	Issue string `json:"issue"`
}

// LoadResults reads every *.json result from the runner results dir, keeping the
// newest per (task,arm) if duplicates exist.
func LoadResults(dir string) ([]Result, error) {
	paths := resultsfs.Records(dir) // layout-agnostic (flat or type subdirs)
	best := map[string]Result{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r Result
		if json.Unmarshal(b, &r) != nil || r.TaskID == "" {
			continue
		}
		best[r.TaskID+"__"+r.Arm] = r
	}
	out := make([]Result, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TaskID != out[j].TaskID {
			return out[i].TaskID < out[j].TaskID
		}
		return out[i].Arm < out[j].Arm
	})
	return out, nil
}

// LoadTasks reads task.json issue text for each task under tasksDir.
func LoadTasks(tasksDir string) (map[string]Task, error) {
	out := map[string]Task{}
	paths, _ := filepath.Glob(filepath.Join(tasksDir, "*", "task.json"))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var t Task
		if json.Unmarshal(b, &t) == nil && t.ID != "" {
			out[t.ID] = t
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tasks under %s", tasksDir)
	}
	return out, nil
}

// Embedder is the slice of the backend the gold builder needs.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// BuildGold pairs the local and frontier result for each task into a dual-arm
// GoldRow with Executable=true. PromptText is the issue; Features come from the
// existing feature extractor; embeddings from the existing embed backend (best
// effort — nil if unavailable). Only tasks with BOTH arms present become rows.
func BuildGold(ctx context.Context, cfg config.Config, results []Result,
	tasks map[string]Task, emb Embedder) ([]schema.GoldRow, Meta, error) {

	byTask := map[string]map[string]Result{}
	for _, r := range results {
		if byTask[r.TaskID] == nil {
			byTask[r.TaskID] = map[string]Result{}
		}
		byTask[r.TaskID][r.Arm] = r
	}

	ids := make([]string, 0, len(byTask))
	for id := range byTask {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// GUARD (stopgap). Gold assembly here is being SUPERSEDED by the offline label
	// engine (PR#2: executed labels -> Resolve -> future materialization). The
	// log-first runner no longer grades during generation, so an oracle-bearing
	// record arrives with NO `resolved`. Refuse to fabricate a 0 outcome from that
	// absence (which would silently produce all-zero gold); require the offline
	// engine to grade first. Do NOT "fix" this by re-adding grading to generation.
	var ungraded []string
	for _, id := range ids {
		for _, arm := range []string{"local", "frontier"} {
			if r, ok := byTask[id][arm]; ok && r.HasExecutableOracle && r.Resolved == nil {
				ungraded = append(ungraded, id+"/"+arm)
			}
		}
	}
	if len(ungraded) > 0 {
		sort.Strings(ungraded)
		show := ungraded
		if len(show) > 5 {
			show = show[:5]
		}
		return nil, Meta{Synthetic: false, Executable: true}, fmt.Errorf(
			"gold assembly: %d oracle-bearing record(s) have no executed outcome "+
				"(`resolved`) — the offline label engine (PR#2) must grade them "+
				"first; refusing to score ungraded records as failures. e.g. %v",
			len(ungraded), show)
	}

	var rows []schema.GoldRow
	meta := Meta{Synthetic: false, Executable: true}
	localOnly := 0
	for _, id := range ids {
		arms := byTask[id]
		local, hasL := arms["local"]
		front, hasF := arms["frontier"]
		if !hasF {
			continue // need at least frontier arm for a dual-arm row
		}
		t := tasks[id]
		var e []float32
		if emb != nil {
			e, _ = emb.Embed(ctx, t.Issue)
		}
		// Roster names come from the records (served_model), not hardcoded — the
		// roster is now opus / gpt-oss:20b and changes per config.
		frontModel := firstNonEmpty(front.ServedModel, front.Model, "frontier")
		localModel := "(local arm missing)"
		// If the local arm never ran (GPU-contended overnight), record the row
		// as frontier-only with OutcomeLocal=0 and mark it, so partial runs are
		// still usable. Fully-paired rows are the default.
		outLocal := 0
		costLocal := 0.0
		if hasL {
			outLocal = outcome(local)
			costLocal = float64(billable(local)) * priceLocal
			localModel = firstNonEmpty(local.ServedModel, local.OllamaModel, "local")
		} else {
			localOnly++
		}
		if meta.FrontierModel == "" {
			meta.FrontierModel, meta.LocalModel = frontModel, localModel
		}
		costFront := float64(billable(front)) * priceFrontier
		rows = append(rows, schema.GoldRow{
			PromptID:        id,
			PromptText:      t.Issue,
			Features:        feature.Extract(t.Issue, "open"),
			Embedding:       e,
			OutcomeLocal:    outLocal,
			OutcomeFrontier: outcome(front),
			LocalModel:      localModel,
			FrontierModel:   frontModel,
			CostLocal:       costLocal,
			CostFrontier:    costFront,
			Executable:      true,
		})
	}
	meta.N = len(rows)
	meta.LocalArmMissing = localOnly
	if len(rows) == 0 {
		return rows, meta, fmt.Errorf("no gold rows: need at least the frontier arm run (see agentic/results)")
	}
	return rows, meta, nil
}

// outcome derefs a graded Resolved (nil → 0). The BuildGold guard has already
// rejected oracle-bearing records with a nil Resolved, so nil here means a
// non-oracle record, scored 0.
func outcome(r Result) int {
	if r.Resolved == nil {
		return 0
	}
	return b2i(*r.Resolved)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Meta describes the agentic gold set provenance.
type Meta struct {
	Synthetic       bool   `json:"synthetic"`
	Executable      bool   `json:"executable"`
	LocalModel      string `json:"local_model"`
	FrontierModel   string `json:"frontier_model"`
	N               int    `json:"n"`
	LocalArmMissing int    `json:"local_arm_missing"`
	Reason          string `json:"reason,omitempty"`
}

// SaveGold writes gold.jsonl + gold_meta.json into dataDir (created if needed).
func SaveGold(dataDir string, rows []schema.GoldRow, meta Meta) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dataDir, "gold.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(dataDir, "gold_meta.json"), mb, 0o644)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
