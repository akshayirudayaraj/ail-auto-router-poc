// Command train fits the candidate routers on the structured dataset (Pillar
// 2) and writes a summary — including how well the IRT router recovers the
// planted per-model abilities.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/dataio"
	"github.com/akshayirudayaraj/ail-routing-test/internal/router"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func main() {
	lg := log.New(os.Stderr, "[train] ", 0)
	cfg, err := config.Load()
	if err != nil {
		lg.Fatalf("config: %v", err)
	}
	// Adapt the roster to the served models in this data dir (no-op on synthetic;
	// picks up gpt-oss:20b / opus on the agentic set) so routers fit the right
	// models without hand-set env vars.
	cfg = dataio.ResolveRoster(cfg)
	lg.Printf("roster: local=%v frontier=%s (data dir %s)", cfg.LocalModels, cfg.FrontierModel, cfg.DataDir)
	pw, err := dataio.LoadPointwise(cfg)
	if err != nil {
		lg.Fatalf("load pointwise: %v (run `make extract` first)", err)
	}
	pairs, _ := dataio.LoadPairwise(cfg)

	td := router.TrainData{
		Pointwise: pw, Pairwise: pairs,
		LocalModels: cfg.LocalModels, FrontierModel: cfg.FrontierModel,
		TrainSource: schema.LabelImplicit,
	}

	summary := map[string]any{}
	var names []string
	for _, r := range router.Registry() {
		if err := r.Fit(td); err != nil {
			lg.Fatalf("fit %s: %v", r.Name(), err)
		}
		names = append(names, r.Name())
	}
	lg.Printf("fit %d routers on %d pointwise / %d pairwise rows: %s",
		len(names), len(pw), len(pairs), strings.Join(names, ", "))

	// IRT ability recovery vs planted truth
	irt := router.NewIRT()
	_ = irt.Fit(td)
	recovered := irt.Abilities()
	summary["irt_recovered_abilities"] = recovered

	var recoveryReport string
	if planted, ok := loadPlantedAbilities(cfg); ok {
		summary["planted_abilities"] = planted
		recoveryReport = compareAbilities(planted, recovered, cfg)
		lg.Printf("IRT ability recovery vs planted:\n%s", recoveryReport)
	}

	// write summary
	summary["n_pointwise"] = len(pw)
	summary["n_pairwise"] = len(pairs)
	summary["routers"] = names
	jb, _ := json.MarshalIndent(summary, "", "  ")
	_ = os.WriteFile(filepath.Join(cfg.DataDir, "train_summary.json"), jb, 0o644)

	md := "## Router training summary\n\n" +
		fmt.Sprintf("Fit on %d pointwise / %d pairwise rows (train source = implicit).\n\n", len(pw), len(pairs)) +
		"Routers: " + strings.Join(names, ", ") + "\n\n" + recoveryReport
	_ = os.WriteFile(filepath.Join(cfg.DataDir, "train_summary.md"), []byte(md), 0o644)
	lg.Printf("wrote %s", filepath.Join(cfg.DataDir, "train_summary.md"))
}

func loadPlantedAbilities(cfg config.Config) (map[string]float64, bool) {
	b, err := os.ReadFile(filepath.Join(cfg.DataDir, "_truth.json"))
	if err != nil {
		return nil, false
	}
	var t struct {
		Abilities map[string]float64 `json:"abilities"`
	}
	if json.Unmarshal(b, &t) != nil || len(t.Abilities) == 0 {
		return nil, false
	}
	return t.Abilities, true
}

// compareAbilities renders a table of planted vs recovered abilities, aligned
// to the reference model (planted abilities are re-centered so the reference =
// 0, matching the IRT identifiability pin).
func compareAbilities(planted, recovered map[string]float64, cfg config.Config) string {
	ref := cfg.LocalModels[0]
	shift := planted[ref] // re-center planted so ref = 0
	var b strings.Builder
	b.WriteString("### IRT ability recovery (θ, reference-centered)\n\n")
	b.WriteString("| model | planted θ | recovered θ |\n|---|--:|--:|\n")
	models := cfg.AllModels()
	sort.SliceStable(models, func(i, j int) bool { return planted[models[i]] < planted[models[j]] })
	for _, m := range models {
		fmt.Fprintf(&b, "| %s | %+.2f | %+.2f |\n", m, planted[m]-shift, recovered[m])
	}
	b.WriteString("\n> Recovery is approximate (noisy implicit labels, small data); the ordering and sign of the ability gaps are what matter for routing.\n")
	return b.String()
}
