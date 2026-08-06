// Command agentic assembles the EXECUTED-ground-truth dual-arm gold set from the
// agentic runner's results (agentic/results/*.json) and writes it, plus a copy
// of the synthetic training data, into a data dir the existing eval harness can
// consume unchanged:
//
//	make gen extract              # synthetic training pointwise/pairwise (./data)
//	python agentic/runner/run_agentic.py --arms frontier,local   # execute arms
//	bin/agentic                  # assemble data_agentic/gold.jsonl (Executable=true)
//	AIL_DATA_DIR=data_agentic bin/eval   # run the harness on the agentic gold
//
// The gold outcomes come from real unit-test pass/fail inside the Claude Code
// harness, so they are a strictly-stronger label source (executed) than the
// implicit training labels — the eval harness enforces exactly that, so there
// is no circularity.
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/akshayirudayaraj/ail-routing-test/internal/agentic"
	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
)

func main() {
	lg := log.New(os.Stderr, "[agentic] ", 0)
	var (
		dataDir    = flag.String("data-dir", "data_agentic", "output data dir for the agentic gold set + copied training data")
		resultsDir = flag.String("results-dir", filepath.Join("agentic", "results"), "runner results dir")
		tasksDir   = flag.String("tasks-dir", filepath.Join("agentic", "tasks"), "task definitions dir")
		trainSrc   = flag.String("train-src", "data", "dir to copy pointwise/pairwise training data from")
		noEmbed    = flag.Bool("no-embed", false, "skip embeddings (kNN degrades but no Ollama dependency)")
	)
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}

	results, err := agentic.LoadResults(*resultsDir)
	if err != nil || len(results) == 0 {
		lg.Fatalf("load results from %s: %v (run agentic/runner/run_agentic.py first)", *resultsDir, err)
	}
	tasks, err := agentic.LoadTasks(*tasksDir)
	if err != nil {
		lg.Fatalf("load tasks: %v", err)
	}
	lg.Printf("loaded %d results across %d tasks", len(results), len(tasks))

	var emb agentic.Embedder
	if !*noEmbed {
		be := backend.New(cfg, lg)
		emb = be // best-effort; BuildGold tolerates embed errors
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rows, meta, err := agentic.BuildGold(ctx, cfg, results, tasks, emb)
	if err != nil {
		lg.Fatalf("build gold: %v", err)
	}
	if err := agentic.SaveGold(*dataDir, rows, meta); err != nil {
		lg.Fatalf("save gold: %v", err)
	}
	lg.Printf("wrote %s/gold.jsonl (%d dual-arm rows, Executable=%v, local-arm-missing=%d)",
		*dataDir, meta.N, meta.Executable, meta.LocalArmMissing)

	// Copy synthetic training data so the eval harness has pointwise/pairwise to
	// fit routers on. Gold (executed) is a strictly-stronger eval source.
	for _, name := range []string{"pointwise.jsonl", "pairwise.jsonl"} {
		src := filepath.Join(*trainSrc, name)
		dst := filepath.Join(*dataDir, name)
		if err := copyFile(src, dst); err != nil {
			lg.Printf("WARNING: could not copy training data %s: %v (run `make gen extract` first)", name, err)
		} else {
			lg.Printf("copied training data %s -> %s", src, dst)
		}
	}

	// Console summary of the headline routing signal.
	printSummary(lg, results)
}

// res derefs a graded outcome (nil → false). Only reached after BuildGold's guard
// has confirmed all oracle-bearing records are graded, so nil here is benign.
func res(r agentic.Result) bool { return r.Resolved != nil && *r.Resolved }

func printSummary(lg *log.Logger, results []agentic.Result) {
	byTask := map[string]map[string]agentic.Result{}
	for _, r := range results {
		if byTask[r.TaskID] == nil {
			byTask[r.TaskID] = map[string]agentic.Result{}
		}
		byTask[r.TaskID][r.Arm] = r
	}
	var cellB, bothPass, bothFail, localOnlyPass, paired int
	var localNative, localRescued, localToolCalls int
	var frontierCost, perfectCost float64
	for _, arms := range byTask {
		l, hasL := arms["local"]
		f, hasF := arms["frontier"]
		if !hasF {
			continue
		}
		if hasL {
			paired++
			localNative += l.NativeToolCalls
			localRescued += l.RescuedToolCalls
			localToolCalls += l.ToolCallsAttempted
			lRes, fRes := res(l), res(f)
			switch {
			case !lRes && fRes:
				cellB++
			case lRes && fRes:
				bothPass++
			case !lRes && !fRes:
				bothFail++
			case lRes && !fRes:
				localOnlyPass++
			}
			// perfect router: use local when local passes, else frontier
			if lRes {
				perfectCost += float64(l.InputTokens+l.OutputTokens) * 1.0
			} else {
				perfectCost += float64(f.InputTokens+f.OutputTokens) * 15.0
			}
		}
		frontierCost += float64(f.InputTokens+f.OutputTokens) * 15.0
	}
	lg.Printf("---- agentic routing signal (paired tasks=%d) ----", paired)
	lg.Printf("cell-B (local FAIL, frontier PASS) = %d  [the escalation-worthy set]", cellB)
	lg.Printf("both pass=%d  both fail=%d  local-only pass=%d", bothPass, bothFail, localOnlyPass)
	if localNative+localRescued > 0 {
		lg.Printf("local native tool-call fidelity = %d/%d native (%.0f%%); %d rescued from prose-JSON",
			localNative, localNative+localRescued,
			100*float64(localNative)/float64(localNative+localRescued), localRescued)
	}
	if frontierCost > 0 {
		lg.Printf("cost (units): always-frontier=%.0f  perfect-router=%.0f  saved=%.0f (%.0f%%)",
			frontierCost, perfectCost, frontierCost-perfectCost,
			100*(frontierCost-perfectCost)/frontierCost)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
