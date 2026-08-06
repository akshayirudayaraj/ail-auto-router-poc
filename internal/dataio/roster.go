package dataio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
)

// ResolveRoster adapts the model roster in cfg to whatever the dataset in
// cfg.DataDir actually contains, so `train`/`eval` work on the agentic data
// (local `gpt-oss:20b`, frontier `opus`) without hand-setting AIL_LOCAL_MODELS /
// AIL_FRONTIER_MODEL — and stay a no-op on the synthetic set.
//
// Why this matters: every router keys on cfg.LocalModels / cfg.FrontierModel, and
// the temporal backtest decides "is this row a frontier row?" by comparing the
// served model to cfg.FrontierModel. If cfg's roster (default
// qwen2.5-coder:14b / claude-sonnet-5) doesn't match the served models in the
// data, frontier rows are silently misclassified as local and IRT abilities are
// fit against phantom models. Deriving the roster FROM the data removes that trap.
//
// Resolution:
//   - frontier = gold_meta.json "frontier_model" (the materializer/BuildGold
//     writes it from the actual frontier arm). Fallback: cfg.FrontierModel.
//   - locals   = the distinct served models in pointwise.jsonl minus the frontier
//     (sorted for a deterministic IRT reference = LocalModels[0]). Fallback: the
//     existing cfg.LocalModels.
//
// If neither source is usable (no pointwise, no meta), cfg is returned unchanged.
// For the synthetic set this recovers exactly {llama3.1:8b, qwen2.5-coder:14b} /
// claude-sonnet-5 — i.e. no behavioral change.
func ResolveRoster(cfg config.Config) config.Config {
	frontier := goldMetaFrontier(cfg.DataDir)
	if frontier == "" {
		frontier = cfg.FrontierModel
	}

	pw, err := LoadPointwise(cfg)
	if err != nil || len(pw) == 0 {
		// Can't see the served models; only trust an explicit meta frontier.
		if frontier != "" {
			cfg.FrontierModel = frontier
		}
		return cfg
	}

	seen := map[string]bool{}
	var locals []string
	for _, r := range pw {
		if r.Model == "" || r.Model == frontier || seen[r.Model] {
			continue
		}
		seen[r.Model] = true
		locals = append(locals, r.Model)
	}
	sort.Strings(locals)

	if frontier != "" {
		cfg.FrontierModel = frontier
	}
	if len(locals) > 0 {
		cfg.LocalModels = locals
	}
	return cfg
}

// goldMetaFrontier reads the frontier_model field from <dir>/gold_meta.json.
// Returns "" if the file/field is absent.
func goldMetaFrontier(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "gold_meta.json"))
	if err != nil {
		return ""
	}
	var m struct {
		FrontierModel string `json:"frontier_model"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.FrontierModel
}
