// Command label is the offline scoring engine's runner (OFFLINE_ENGINE_PLAN).
// It reads the pre-outcome session artifacts under a results dir, builds a
// deterministic evidence pack per session, and — for the judge branch — asks the
// frontier judge for an adequacy verdict, appending judge LabelRecords to
// labels/judge.jsonl. Generation assigns no outcome; this is where judge outcomes
// come from.
//
// The executed-oracle branch (Python, Docker/swebench) and the fusion/calibration
// step are separate (deferred here); this command wires the judge branch only.
//
//	go run ./cmd/label -dry-run                 # build + print packs, no model call
//	go run ./cmd/label                          # judge every session (needs subscription)
//	go run ./cmd/label -session <key> -dry-run  # one session
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/label"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func main() {
	var (
		results = flag.String("results", "agentic/results", "dir of run records + logs")
		tasks   = flag.String("tasks", "agentic/tasks", "dir of task.json + oracle")
		session = flag.String("session", "", "judge only this session key (default: all)")
		dryRun  = flag.Bool("dry-run", false, "build + print evidence packs; no model call")
		forceK  = flag.Bool("force-k", false, "always run K votes (calibration sample)")
		k       = flag.Int("k", 3, "votes when escalated")
	)
	flag.Parse()

	lg := log.New(os.Stderr, "[label] ", 0)
	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}

	refs, err := label.ListSessions(*results)
	if err != nil {
		lg.Fatalf("list sessions: %v", err)
	}
	if *session != "" {
		filtered := refs[:0]
		for _, r := range refs {
			if r.Key == *session {
				filtered = append(filtered, r)
			}
		}
		refs = filtered
	}
	if len(refs) == 0 {
		lg.Fatalf("no sessions under %s", *results)
	}
	splitByTask := label.LoadSplitByTask(*results)

	// Dry run: just render the packs (no backend needed).
	if *dryRun {
		for _, r := range refs {
			pack, err := label.BuildFromResults(*results, *tasks, r.Key)
			if err != nil {
				lg.Printf("skip %s: %v", r.Key, err)
				continue
			}
			fmt.Printf("\n===== %s (split=%s oracle=%v) =====\n%s\n",
				r.Key, splitByTask[r.TaskID], r.HasOracle, pack.Render())
		}
		lg.Printf("dry-run: rendered %d packs", len(refs))
		return
	}

	be := backend.New(cfg, lg)
	if !be.AnthropicAvailable() {
		lg.Fatalf("judge branch needs the Anthropic subscription (or ANTHROPIC_API_KEY); "+
			"re-run with -dry-run to just build packs")
	}
	assessor := label.BackendAssessor{Backend: be, Model: cfg.JudgeModel}
	opts := label.DefaultJudgeOptions()
	opts.K = *k
	opts.ForceK = *forceK

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	var recs []label.LabelRecord
	var adequate, inadequate, skipped int
	for _, r := range refs {
		pack, err := label.BuildFromResults(*results, *tasks, r.Key)
		if err != nil {
			lg.Printf("skip %s: %v", r.Key, err)
			skipped++
			continue
		}
		ident := r.Ident(splitByTask[r.TaskID], time.Now().Unix())
		rec, votes, err := label.JudgeSession(ctx, assessor, pack, ident, opts)
		if err != nil {
			lg.Printf("judge %s: %v", r.Key, err)
			skipped++
			continue
		}
		recs = append(recs, rec)
		if rec.Outcome == 1 {
			adequate++
		} else {
			inadequate++
		}
		lg.Printf("%-52s split=%-7s oracle=%-5v outcome=%d conf=%.2f votes=%d",
			r.Key, ident.Split, r.HasOracle, rec.Outcome, rec.LabelConfidence, len(votes))
	}

	if err := label.AppendLabels(*results, schema.LabelJudge, recs); err != nil {
		lg.Fatalf("write labels: %v", err)
	}
	lg.Printf("judged %d sessions -> labels/judge.jsonl (adequate=%d inadequate=%d skipped=%d)",
		len(recs), adequate, inadequate, skipped)
	be.LogStats()
}
