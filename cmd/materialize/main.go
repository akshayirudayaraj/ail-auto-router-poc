// Command materialize is the offline-engine -> harness bridge (OFFLINE_ENGINE_PLAN
// O6). It reads the fused canonical labels (agentic/results/labels/resolved.jsonl)
// and writes pointwise.jsonl / pairwise.jsonl / gold.jsonl + gold_meta.json into a
// data dir the EXISTING train + eval harness consumes unchanged:
//
//	make agentic-grade agentic-calibrate     # offline engine -> resolved.jsonl
//	bin/materialize                          # resolved -> data_agentic/{pointwise,pairwise,gold}
//	AIL_DATA_DIR=data_agentic bin/train      # fit routers on real agentic train data
//	AIL_DATA_DIR=data_agentic bin/eval       # evaluate on executed holdout gold
//
// This SUPERSEDES bin/agentic (internal/agentic.BuildGold): outcomes come from the
// engine's calibrated labels, gold is EXECUTED-only and drawn from the HOLDOUT
// split, and oracle-bearing-but-ungraded sessions are quarantined (see
// internal/materialize doc).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/akshayirudayaraj/ail-routing-test/internal/agentic"
	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/label"
	"github.com/akshayirudayaraj/ail-routing-test/internal/materialize"
)

func main() {
	lg := log.New(os.Stderr, "[materialize] ", 0)
	var (
		dataDir    = flag.String("data-dir", "data_agentic", "output data dir")
		resultsDir = flag.String("results-dir", filepath.Join("agentic", "results"), "runner results dir (holds labels/resolved.jsonl + run records)")
		tasksDir   = flag.String("tasks-dir", filepath.Join("agentic", "tasks"), "task definitions dir")
		noEmbed    = flag.Bool("no-embed", false, "skip embeddings (kNN degrades but no Ollama dependency)")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}

	resolved, err := label.LoadResolved(*resultsDir)
	if err != nil {
		lg.Fatalf("load resolved labels from %s/labels/resolved.jsonl: %v (run `make agentic-grade agentic-calibrate` first)", *resultsDir, err)
	}
	if len(resolved) == 0 {
		lg.Fatalf("no canonical labels in %s/labels/resolved.jsonl", *resultsDir)
	}

	tasks, err := agentic.LoadTasks(*tasksDir)
	if err != nil {
		lg.Fatalf("load tasks: %v", err)
	}
	issues := make(map[string]string, len(tasks))
	for id, t := range tasks {
		issues[id] = t.Issue
	}

	sessions, err := materialize.LoadSessions(*resultsDir)
	if err != nil {
		lg.Fatalf("load run records: %v", err)
	}

	var emb materialize.Embedder
	if !*noEmbed {
		emb = backend.New(cfg, lg) // best-effort; Build tolerates embed errors
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ds, meta, err := materialize.Build(ctx, cfg, resolved, issues, sessions, emb)
	if err != nil {
		lg.Fatalf("build: %v", err)
	}
	if err := materialize.Save(*dataDir, ds, meta); err != nil {
		lg.Fatalf("save: %v", err)
	}

	lg.Printf("wrote %s: pointwise=%d pairwise=%d gold=%d (local=%s frontier=%s)",
		*dataDir, meta.NPointwise, meta.NPairwise, meta.NGold, meta.LocalModel, meta.FrontierModel)
	if meta.QuarantinedOracleUngraded > 0 {
		lg.Printf("quarantined %d oracle-bearing ungraded sessions (not materialized as truth)", meta.QuarantinedOracleUngraded)
	}
	if meta.HoldoutDroppedNotDualExec > 0 {
		lg.Printf("dropped %d holdout tasks lacking executed labels on BOTH arms", meta.HoldoutDroppedNotDualExec)
	}
	if meta.FirewallWarning != "" {
		lg.Printf("WARNING: %s", meta.FirewallWarning)
	}
	for _, n := range meta.Notes {
		lg.Printf("note: %s", n)
	}
}
