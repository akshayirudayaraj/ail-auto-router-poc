// Command eval runs the full evaluation harness (Pillar 3) over the trained
// routers and assembles RESULTS.md.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/dataio"
	"github.com/akshayirudayaraj/ail-routing-test/internal/eval"
	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func main() {
	lg := log.New(os.Stderr, "[eval] ", 0)
	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}
	// Adapt the roster to the served models in this data dir (no-op on synthetic;
	// picks up gpt-oss:20b / opus on the agentic set) — otherwise the backtest
	// misclassifies frontier rows as local and IRT fits phantom models.
	cfg = dataio.ResolveRoster(cfg)
	lg.Printf("roster: local=%v frontier=%s (data dir %s)", cfg.LocalModels, cfg.FrontierModel, cfg.DataDir)
	pw, err := dataio.LoadPointwise(cfg)
	if err != nil {
		lg.Fatalf("load pointwise: %v (run `make extract` first)", err)
	}
	pairs, _ := dataio.LoadPairwise(cfg)
	gold, err := dataio.LoadGold(cfg)
	if err != nil {
		lg.Fatalf("load gold: %v", err)
	}
	be := backend.New(cfg, lg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	data := eval.Data{
		Cfg: cfg, Pointwise: pw, Pairwise: pairs, Gold: gold,
		Embedder: be, Ctx: ctx,
	}

	methods := []eval.EvalMethod{
		eval.NewGoldEval(),
		eval.NewTemporalBacktest(),
		eval.NewOffPolicy(),
		eval.NewGuardrailSuite(),
	}

	var body strings.Builder
	reports := map[string]eval.Report{}
	for _, m := range methods {
		routers := router.Registry() // fresh, unfit instances per method
		rep, err := m.Run(routers, data)
		if err != nil {
			lg.Printf("method %s: %v", m.Name(), err)
			fmt.Fprintf(&body, "### %s\n\n> ⚠️ %s\n\n", m.Name(), err.Error())
			continue
		}
		reports[m.Name()] = rep
		body.WriteString(rep.Markdown())
		body.WriteString("\n")
		// persist structured detail
		if jb, e := json.MarshalIndent(rep, "", "  "); e == nil {
			_ = os.WriteFile(filepath.Join(cfg.DataDir, "eval_"+m.Name()+".json"), jb, 0o644)
		}
		lg.Printf("ran %s (%d routers)", m.Name(), len(rep.Rows))
	}

	// ---- policy layer demo on the best learned router by gold AIQ ----
	body.WriteString(policyDemo(cfg, data, lg))

	_ = os.WriteFile(filepath.Join(cfg.DataDir, "eval_report.md"), []byte(body.String()), 0o644)
	be.LogStats()

	// assemble RESULTS.md
	if err := assembleResults(cfg, body.String(), reports); err != nil {
		lg.Printf("assemble RESULTS.md: %v", err)
	}
	lg.Printf("wrote eval_report.md and RESULTS.md")
}

// policyDemo calibrates a deployable threshold + quota gate on the best learned
// router (by gold AIQ) and reports the resulting operating points.
func policyDemo(cfg config.Config, data eval.Data, lg *log.Logger) string {
	// The policy layer calibrates thresholds against the dual-arm gold set; with
	// no gold rows yet (executed holdout not populated) there is nothing to
	// calibrate against, and CalibrateForQuality/Operating would divide by zero.
	if len(data.Gold) == 0 {
		lg.Printf("policy layer skipped: no dual-arm gold rows")
		return "### policy layer\n\n> Skipped: no dual-arm gold rows yet. " +
			"Populate executed holdout gold (run grading, then `make agentic-materialize`) to calibrate a deployable threshold.\n\n"
	}
	td := eval.TrainDataFrom(data, schema.LabelImplicit)
	type cand struct {
		r      router.Router
		scores []float64
		aiq    float64
	}
	var best *cand
	for _, r := range router.Registry() {
		name := r.Name()
		if name == "always-local" || name == "always-frontier" {
			continue
		}
		_ = r.Fit(td)
		scores := make([]float64, len(data.Gold))
		for i, row := range data.Gold {
			scores[i] = r.Score(router.InstanceFromGold(row))
		}
		aiq := eval.AIQ(eval.CostQualityCurve(scores, data.Gold))
		if best == nil || aiq > best.aiq {
			best = &cand{r: r, scores: scores, aiq: aiq}
		}
	}
	if best == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### policy layer (deployed router: %s, gold AIQ=%.3f)\n\n", best.r.Name(), best.aiq)

	// calibrate to a target escalation rate (log-only calibration is fine)
	thrRate := eval.CalibrateEscalationRate(best.scores, 0.30)
	opRate := eval.Operating(best.scores, data.Gold, thrRate)
	// calibrate to a target quality retention (needs dual-arm gold)
	thrQual := eval.CalibrateForQuality(best.scores, data.Gold, 0.95)
	opQual := eval.Operating(best.scores, data.Gold, thrQual)

	fmt.Fprintf(&b, "| calibration | threshold | escalation | quality_retention | cost_vs_local | under_escal(cellB) |\n")
	fmt.Fprintf(&b, "|---|--:|--:|--:|--:|--:|\n")
	fmt.Fprintf(&b, "| target escalation 30%% | %.3f | %.3f | %.3f | %.2f | %.3f |\n",
		thrRate, opRate.EscalationRate, opRate.QualityRetention, opRate.CostVsLocal, opRate.Cells.UnderEscalation)
	fmt.Fprintf(&b, "| target quality 95%% | %.3f | %.3f | %.3f | %.2f | %.3f |\n",
		thrQual, opQual.EscalationRate, opQual.QualityRetention, opQual.CostVsLocal, opQual.Cells.UnderEscalation)

	// quota gate demo: cap escalations at 20% of traffic even at the rate threshold
	gate := eval.NewQuotaGate(thrRate, 0.20)
	for _, s := range best.scores {
		gate.Decide(s)
	}
	tot, esc := gate.Stats()
	fmt.Fprintf(&b, "\nQuota gate (threshold %.3f, cap 20%%): escalated %d/%d = %.1f%% of traffic.\n\n",
		thrRate, esc, tot, 100*float64(esc)/float64(max1(tot)))
	b.WriteString("> Target-escalation-rate calibration is safe on logs; target-QUALITY calibration is only trustworthy on the dual-arm gold set (or online A/B).\n\n")
	return b.String()
}

func max1(x int) int {
	if x < 1 {
		return 1
	}
	return x
}

const howToRead = "## How to read this report\n\n" +
	"- **Pillar 1 (label engine).** `implicit` precision/recall of catching the *inadequate* answers that need escalation, graded vs planted truth. High precision + partial recall is the intended profile: implicit signals are trustworthy when present but miss quietly-abandoned failures.\n" +
	"- **Pillar 2 (routers).** IRT ability recovery: recovered θ ordering/sign should match planted (magnitudes compress under noisy labels — that's fine, routing only needs the ordering).\n" +
	"- **dual-arm-gold** is the only ABSOLUTE anchor. Read it as: **AIQ** (higher = more quality per unit cost; a good learned router beats both baselines), **qual_retention** vs **cost_vs_local** (e.g. matching frontier quality at a fraction of frontier cost is the win), and **under_escal_cellB** (the costly miss — lower is better).\n" +
	"- **temporal-backtest** only RANKS (observational censoring). It enforces eval labels be a strictly-stronger source than train; at this tiny scale the held-out judge set can be single-class, making AUC uninformative (see its warning) — that is a data-scale limit, not a router verdict.\n" +
	"- **off-policy-ips-dr** estimates the reward of *deploying* each router from logs with propensities; `uplift_dr` > 0 means it beats the logging policy. Watch **ess** (low ⇒ high-variance IPS).\n" +
	"- **guardrail-suite**: `difficulty_monotonicity` should be ~1.0 and `topic_flip_rate` ~0.0 (routes on difficulty, not topic). Baselines score 0 monotonicity by design (constant scores).\n" +
	"- **policy layer** shows a deployable threshold calibrated on the best-AIQ router, plus a frontier quota gate.\n\n"

func assembleResults(cfg config.Config, evalBody string, reports map[string]eval.Report) error {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(cfg.DataDir, name))
		if err != nil {
			return ""
		}
		return string(b)
	}
	goldMeta := read("gold_meta.json")

	var b strings.Builder
	b.WriteString("# Results\n\n")
	// Provenance banner so each RESULTS.md self-identifies its label source. The
	// templated `data/` run is a never-signal CI fixture; the agentic data dir
	// carries execution-grounded gold.
	if strings.Contains(strings.ReplaceAll(goldMeta, " ", ""), "\"executable\":true") {
		b.WriteString("> **Execution-grounded** — agentic dual-arm gold (executed unit-test outcomes).\n\n")
	} else {
		b.WriteString("> **Templated-synthetic baseline** — data from `internal/generate`, a never-signal CI fixture (see DECISIONS: quarantined generator). For real execution-grounded results see `data_agentic/RESULTS.md`.\n\n")
	}
	fmt.Fprintf(&b, "End-to-end run on the small default config (seed=%d, %d local models + frontier `%s`).\n\n",
		cfg.Seed, len(cfg.LocalModels), cfg.FrontierModel)
	b.WriteString("_Absolute cost/quality numbers come from the dual-arm gold set only; backtests rank routers; off-policy estimates the counterfactual reward from logged propensities._\n\n")
	b.WriteString(howToRead)
	b.WriteString("---\n\n")

	if s := read("extractor_report.md"); s != "" {
		b.WriteString("## Pillar 1 — label engine\n\n")
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	if s := read("train_summary.md"); s != "" {
		b.WriteString("## Pillar 2 — routers\n\n")
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString("## Pillar 3 — evaluation harness\n\n")
	if goldMeta != "" {
		fmt.Fprintf(&b, "Gold set meta:\n```json\n%s\n```\n\n", strings.TrimSpace(goldMeta))
	}
	b.WriteString(evalBody)
	b.WriteString("\n---\n\nSee DECISIONS.md for choices and README.md to reproduce (`make all`).\n")

	return os.WriteFile("RESULTS.md", []byte(b.String()), 0o644)
}
