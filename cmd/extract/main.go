// Command extract runs the offline label engine (Pillar 1b), the extractor
// quality report (Pillar 1c), and builds the dual-arm gold benchmark.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/extract"
	"github.com/akshayirudayaraj/ail-routing-test/internal/gold"
)

func main() {
	lg := log.New(os.Stderr, "[extract] ", 0)
	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}
	be := backend.New(cfg, lg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	// Pillar 1b: structured dataset.
	res, err := extract.Run(ctx, cfg, be)
	if err != nil {
		lg.Fatalf("extract: %v", err)
	}
	lg.Printf("dataset: obs=%d implicit=%d judge=%d pairwise=%d (embed_fail=%d judge_fail=%d)",
		res.Observations, res.ImplicitRows, res.JudgeRows, res.PairwiseRows, res.EmbedFail, res.JudgeFail)
	lg.Printf("wrote %s and %s", res.PointwisePath, res.PairwisePath)

	// Pillar 1c: extractor quality report (grades against hidden truth).
	rep, err := extract.BuildReport(cfg)
	if err != nil {
		lg.Fatalf("report: %v", err)
	}
	if err := rep.Save(cfg); err != nil {
		lg.Fatalf("save report: %v", err)
	}
	lg.Printf("extractor quality: implicit acc=%.3f P=%.3f R=%.3f | judge acc=%.3f (n=%d)",
		rep.Implicit.Accuracy, rep.Implicit.Precision, rep.Implicit.Recall,
		rep.Judge.Accuracy, rep.Judge.N)

	// Dual-arm gold benchmark.
	rows, meta, err := gold.Generate(ctx, cfg, be)
	if err != nil {
		lg.Fatalf("gold: %v", err)
	}
	if err := gold.Save(cfg, rows, meta); err != nil {
		lg.Fatalf("save gold: %v", err)
	}
	lg.Printf("gold set: n=%d synthetic=%v local=%s frontier=%s",
		len(rows), meta.Synthetic, meta.LocalModel, meta.FrontierModel)

	be.LogStats()
}
