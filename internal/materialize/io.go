package materialize

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/akshayirudayaraj/ail-routing-test/internal/resultsfs"
)

// LoadSessions reads the runner result JSONs under resultsDir and returns the
// billable token counts keyed by session_id, for gold cost. Non-run-record files
// (logs, manifests, prediction files) are skipped.
func LoadSessions(resultsDir string) (map[string]Session, error) {
	paths := resultsfs.Records(resultsDir) // layout-agnostic (flat or type subdirs)
	out := map[string]Session{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var rr struct {
			SessionID    string `json:"session_id"`
			TaskID       string `json:"task_id"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
		}
		if json.Unmarshal(b, &rr) != nil || rr.TaskID == "" || rr.SessionID == "" {
			continue
		}
		out[rr.SessionID] = Session{InputTokens: rr.InputTokens, OutputTokens: rr.OutputTokens}
	}
	return out, nil
}

// Save writes pointwise.jsonl, pairwise.jsonl, gold.jsonl (always created, even
// when empty, so cmd/train + eval can load) plus gold_meta.json into dataDir.
func Save(dataDir string, ds Datasets, meta Meta) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(dataDir, "pointwise.jsonl"), ds.Pointwise); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(dataDir, "pairwise.jsonl"), ds.Pairwise); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(dataDir, "gold.jsonl"), ds.Gold); err != nil {
		return err
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(dataDir, "gold_meta.json"), mb, 0o644)
}

func writeJSONL[T any](path string, rows []T) error {
	f, err := os.Create(path)
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
	return w.Flush()
}
