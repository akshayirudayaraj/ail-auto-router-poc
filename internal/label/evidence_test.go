package label

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// repoRoot is two levels up from internal/label.
func repoRoot() string { return filepath.Join("..", "..") }

// TestEvidencePack_GptOssSWE builds the pack for the committed gpt-oss sample on
// psf__requests-2931 (where the local arm floundered — edited a test file, never
// fixed models.py) and asserts the pack captures the signals a judge needs to rule
// it inadequate. This is the offline engine's first real, non-circular fixture.
func TestEvidencePack_GptOssSWE(t *testing.T) {
	results := filepath.Join(repoRoot(), "agentic", "results")
	tasks := filepath.Join(repoRoot(), "agentic", "tasks")
	const key = "swe-psf__requests-2931__local__963462c0"

	p, err := BuildFromResults(results, tasks, key)
	if err != nil {
		t.Fatalf("BuildFromResults: %v", err)
	}

	if !strings.Contains(strings.ToLower(p.Issue), "binary payload") {
		t.Errorf("issue not loaded; got %q", truncate(p.Issue, 80))
	}

	// It edited the test harness and never touched requests/models.py.
	var touchedTest, touchedModels bool
	for _, cf := range p.ChangedFiles {
		if strings.Contains(cf.Path, "test_requests.py") {
			touchedTest = true
		}
		if strings.Contains(cf.Path, "requests/models.py") {
			touchedModels = true
		}
	}
	if !touchedTest {
		t.Errorf("expected the diff to touch test_requests.py; changed=%v", p.ChangedFiles)
	}
	if touchedModels {
		t.Errorf("gpt-oss did NOT touch models.py in this sample; parser is wrong")
	}
	if !p.Flags.EditedTestFile {
		t.Errorf("EditedTestFile flag should be set (edited test_requests.py)")
	}

	// It ran pytest and it errored.
	var sawErroredPytest bool
	for _, r := range p.VerificationRuns {
		if strings.Contains(strings.ToLower(r.Command), "pytest") && r.Errored {
			sawErroredPytest = true
		}
	}
	if !sawErroredPytest {
		t.Errorf("expected an errored pytest verification run; got %+v", p.VerificationRuns)
	}

	rendered := p.Render()
	for _, want := range []string{"ISSUE:", "CHANGED FILES", "VERIFICATION RUNS", "RUN FLAGS:"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered pack missing section %q", want)
		}
	}
	t.Logf("\n----- evidence pack (%s) -----\n%s", key, rendered)
}

// TestResolve_StrengthOrdering checks executed beats judge beats implicit, and
// confidence breaks ties within a source.
func TestResolve_StrengthOrdering(t *testing.T) {
	recs := []LabelRecord{
		{TaskID: "t1", Model: "m", LabelSource: schema.LabelImplicit, Outcome: 1, LabelConfidence: 0.9},
		{TaskID: "t1", Model: "m", LabelSource: schema.LabelJudge, Outcome: 0, LabelConfidence: 0.5},
		{TaskID: "t1", Model: "m", LabelSource: schema.LabelExecuted, Outcome: 0, LabelConfidence: 1.0},
		{TaskID: "t2", Model: "m", LabelSource: schema.LabelJudge, Outcome: 1, LabelConfidence: 0.4},
		{TaskID: "t2", Model: "m", LabelSource: schema.LabelJudge, Outcome: 0, LabelConfidence: 0.8},
	}
	got := Resolve(recs)
	if r := got["t1|m"]; r.LabelSource != schema.LabelExecuted || r.Outcome != 0 {
		t.Errorf("t1 should resolve to executed/0; got %v/%d", r.LabelSource, r.Outcome)
	}
	if r := got["t2|m"]; r.LabelSource != schema.LabelJudge || r.Outcome != 0 {
		t.Errorf("t2 should resolve to higher-confidence judge (outcome 0); got %v/%d", r.LabelSource, r.Outcome)
	}
}

// TestAssertEvalStrongerThanTrain flags a holdout labeled weaker than train.
func TestAssertEvalStrongerThanTrain(t *testing.T) {
	ok := map[string]LabelRecord{
		"a|m": {Split: "holdout", LabelSource: schema.LabelExecuted},
		"b|m": {Split: "train", LabelSource: schema.LabelJudge},
	}
	if err := AssertEvalStrongerThanTrain(ok); err != nil {
		t.Errorf("expected no circularity, got %v", err)
	}
	bad := map[string]LabelRecord{
		"a|m": {Split: "holdout", LabelSource: schema.LabelImplicit},
		"b|m": {Split: "train", LabelSource: schema.LabelExecuted},
	}
	if err := AssertEvalStrongerThanTrain(bad); err == nil {
		t.Errorf("expected circularity error (holdout weaker than train)")
	}
}
