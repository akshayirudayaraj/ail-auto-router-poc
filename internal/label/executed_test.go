package label

import (
	"path/filepath"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// TestExecutedLabels_CrossLanguageContract loads the executed LabelRecords written
// by the Python oracle branch (agentic/runner/grade_offline.py) and confirms the Go
// side parses them and Resolve treats them as the strongest source. Skips if the
// file hasn't been generated (grade_offline.py needs Docker), so it never blocks a
// clean checkout.
func TestExecutedLabels_CrossLanguageContract(t *testing.T) {
	dir := filepath.Join(repoRoot(), "agentic", "results")
	recs, err := LoadLabels(dir)
	if err != nil {
		t.Fatalf("LoadLabels: %v", err)
	}
	if len(recs) == 0 {
		t.Skip("no labels/*.jsonl (run agentic/runner/grade_offline.py first)")
	}

	var executed int
	for _, r := range recs {
		if r.LabelSource != schema.LabelExecuted {
			continue
		}
		executed++
		if r.LabelConfidence != 1.0 {
			t.Errorf("%s: executed label confidence should be 1.0, got %v", r.SessionID, r.LabelConfidence)
		}
		if !r.HasExecutableOracle {
			t.Errorf("%s: executed label must have has_executable_oracle=true", r.SessionID)
		}
		if r.Model == "" || r.TaskID == "" {
			t.Errorf("%s: identity fields missing (model=%q task=%q)", r.SessionID, r.Model, r.TaskID)
		}
	}
	if executed == 0 {
		t.Skip("labels present but none executed-sourced")
	}

	// Resolve must select executed over any weaker source on the same session.
	resolved := Resolve(recs)
	if err := AssertEvalStrongerThanTrain(resolved); err != nil {
		t.Errorf("unexpected circularity across resolved executed labels: %v", err)
	}
	t.Logf("cross-language contract ok: %d records, %d executed-sourced", len(recs), executed)
}
