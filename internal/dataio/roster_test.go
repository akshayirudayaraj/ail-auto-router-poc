package dataio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// writeFixture lays down pointwise.jsonl + gold_meta.json in a temp data dir.
func writeFixture(t *testing.T, models []string, frontier string) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "pointwise.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for i, m := range models {
		_ = enc.Encode(schema.PointwiseRow{PromptID: "t", Model: m, Outcome: i % 2})
	}
	f.Close()
	if frontier != "" {
		_ = os.WriteFile(filepath.Join(dir, "gold_meta.json"),
			[]byte(`{"frontier_model":"`+frontier+`"}`), 0o644)
	}
	return dir
}

func TestResolveRosterAgentic(t *testing.T) {
	// agentic: served models gpt-oss:20b (x2) + opus; meta names opus as frontier.
	dir := writeFixture(t, []string{"gpt-oss:20b", "opus", "gpt-oss:20b"}, "opus")
	got := ResolveRoster(config.Config{DataDir: dir,
		LocalModels: []string{"qwen2.5-coder:14b"}, FrontierModel: "claude-sonnet-5"})

	if got.FrontierModel != "opus" {
		t.Errorf("frontier = %q, want opus", got.FrontierModel)
	}
	if len(got.LocalModels) != 1 || got.LocalModels[0] != "gpt-oss:20b" {
		t.Errorf("locals = %v, want [gpt-oss:20b]", got.LocalModels)
	}
}

func TestResolveRosterSyntheticNoOp(t *testing.T) {
	// synthetic: two locals + a claude frontier; resolver must recover exactly the
	// same roster (deriving locals as distinct-minus-frontier, sorted).
	dir := writeFixture(t,
		[]string{"llama3.1:8b", "qwen2.5-coder:14b", "claude-sonnet-5"}, "claude-sonnet-5")
	got := ResolveRoster(config.Config{DataDir: dir,
		LocalModels: []string{"llama3.1:8b", "qwen2.5-coder:14b"}, FrontierModel: "claude-sonnet-5"})

	if got.FrontierModel != "claude-sonnet-5" {
		t.Errorf("frontier = %q, want claude-sonnet-5", got.FrontierModel)
	}
	want := []string{"llama3.1:8b", "qwen2.5-coder:14b"}
	if len(got.LocalModels) != 2 || got.LocalModels[0] != want[0] || got.LocalModels[1] != want[1] {
		t.Errorf("locals = %v, want %v", got.LocalModels, want)
	}
}

func TestResolveRosterNoDataUnchanged(t *testing.T) {
	// no pointwise, no meta -> roster returned unchanged.
	dir := t.TempDir()
	in := config.Config{DataDir: dir,
		LocalModels: []string{"llama3.1:8b"}, FrontierModel: "claude-sonnet-5"}
	got := ResolveRoster(in)
	if got.FrontierModel != "claude-sonnet-5" || len(got.LocalModels) != 1 {
		t.Errorf("expected unchanged roster, got local=%v frontier=%s", got.LocalModels, got.FrontierModel)
	}
}

func TestResolveRosterMetaOnlyFrontier(t *testing.T) {
	// gold_meta names a frontier but pointwise is empty (gold not yet populated) —
	// still adopt the frontier so the backtest classifies correctly.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "gold_meta.json"), []byte(`{"frontier_model":"opus"}`), 0o644)
	got := ResolveRoster(config.Config{DataDir: dir,
		LocalModels: []string{"gpt-oss:20b"}, FrontierModel: "claude-sonnet-5"})
	if got.FrontierModel != "opus" {
		t.Errorf("frontier = %q, want opus", got.FrontierModel)
	}
}
