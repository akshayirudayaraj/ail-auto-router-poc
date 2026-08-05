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
	"path/filepath"
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
		dryRun    = flag.Bool("dry-run", false, "build + print evidence packs; no model call")
		forceK    = flag.Bool("force-k", false, "always run K votes (calibration sample)")
		k          = flag.Int("k", 3, "votes when escalated")
		calibrate  = flag.Bool("calibrate", false, "calibrate weak labels vs executed truth + fuse; no model call")
		heuristics = flag.Bool("heuristics", false, "mine implicit labels from sim-user reactions; no model call")
	)
	flag.Parse()

	lg := log.New(os.Stderr, "[label] ", 0)
	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}

	// -calibrate: score judge/heuristics against executed truth, fuse the weak
	// labels (judge-primary) into canonical labels, write the report + resolved.
	// No backend needed.
	if *calibrate {
		recs, err := label.LoadLabels(*results)
		if err != nil {
			lg.Fatalf("load labels: %v", err)
		}
		if len(recs) == 0 {
			lg.Fatalf("no labels under %s/labels (run grade_offline.py and/or the judge first)", *results)
		}
		rep := label.Calibrate(recs)
		if err := label.SaveCalibration(*results, rep); err != nil {
			lg.Fatalf("save calibration: %v", err)
		}
		fp := label.DefaultFuseParams()
		fp.JudgeAccuracy = rep.JudgeAccuracy()
		if c, ok := rep.BySource["implicit"]; ok {
			fp.HeurAccuracy = c.Accuracy
		}
		resolved := label.ResolveWithFusion(recs, fp)
		if err := label.SaveResolved(*results, resolved); err != nil {
			lg.Fatalf("save resolved: %v", err)
		}
		if err := label.AssertEvalStrongerThanTrain(resolved); err != nil {
			lg.Printf("WARN %v", err)
		}
		j := rep.BySource["judge"]
		lg.Printf("calibration: executed=%d | judge N=%d acc=%.3f P=%.3f R=%.3f | judge↔heur agree=%.3f (n=%d)%s",
			rep.NExecuted, j.N, j.Accuracy, j.Precision, j.Recall, rep.JudgeHeurAgreement, rep.NJudgeHeurPairs,
			noteSuffix(rep.Note))
		lg.Printf("resolved %d canonical labels -> labels/resolved.jsonl (+ calibration/report.json)", len(resolved))
		return
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

	// -heuristics: mine implicit labels from the sim-user reactions in each
	// session's RawTurn log. Deterministic, no backend. Rewrites implicit.jsonl.
	if *heuristics {
		isFront := label.FrontierPredicate(cfg.FrontierModel)
		var all []label.LabelRecord
		var withReaction int
		for _, r := range refs {
			sp := filepath.Join(*results, r.Key+".session.jsonl")
			ts := time.Now().Unix()
			recs, err := label.HeuristicLabelsFromSession(sp, isFront, r.Ident(splitByTask[r.TaskID], ts), ts)
			if err != nil {
				lg.Printf("skip %s: %v", r.Key, err)
				continue
			}
			for _, rec := range recs {
				if v, _ := rec.Evidence["had_user_reaction"].(bool); v {
					withReaction++
				}
			}
			all = append(all, recs...)
		}
		if err := label.WriteLabels(*results, schema.LabelImplicit, all); err != nil {
			lg.Fatalf("write implicit labels: %v", err)
		}
		lg.Printf("heuristics: %d implicit labels (%d from a real user reaction, rest weak default) -> labels/implicit.jsonl",
			len(all), withReaction)
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

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " | " + note
}
