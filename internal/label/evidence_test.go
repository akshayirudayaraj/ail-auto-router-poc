package label

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// repoRoot is two levels up from internal/label.
func repoRoot() string { return filepath.Join("..", "..") }

// realSampleKey resolves the committed gpt-oss requests-2931 local sample by
// PREFIX rather than a fixed session hash. The hash changes every time the
// corpus is regenerated, so pinning one (e.g. the old 963462c0) makes these
// tests rot; globbing the current sample keeps them stable across re-runs.
func realSampleKey(t *testing.T) string {
	t.Helper()
	const prefix = "swe-psf__requests-2931__local__"
	matches, err := filepath.Glob(filepath.Join(repoRoot(), "agentic", "results", prefix+"*.json"))
	if err != nil {
		t.Fatalf("glob sample: %v", err)
	}
	if len(matches) == 0 {
		t.Skipf("no committed %s* sample in agentic/results", prefix)
	}
	return strings.TrimSuffix(filepath.Base(matches[0]), ".json")
}

// TestEvidencePack_RealSample builds the pack for a committed real gpt-oss sample
// and asserts the STABLE properties: it loads the issue and renders every section.
// It deliberately does NOT assert on the diff/verification content — that is
// regenerated whenever the corpus is re-run, so the behavioral assertions live in
// TestEvidencePack_DetectsTestEditAndFailedVerify against a controlled input.
func TestEvidencePack_RealSample(t *testing.T) {
	results := filepath.Join(repoRoot(), "agentic", "results")
	tasks := filepath.Join(repoRoot(), "agentic", "tasks")
	key := realSampleKey(t)

	p, err := BuildFromResults(results, tasks, key)
	if err != nil {
		t.Fatalf("BuildFromResults: %v", err)
	}
	if !strings.Contains(strings.ToLower(p.Issue), "binary payload") {
		t.Errorf("issue not loaded; got %q", truncate(p.Issue, 80))
	}
	rendered := p.Render()
	for _, want := range []string{"ISSUE:", "CHANGED FILES", "VERIFICATION RUNS", "RUN FLAGS:"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered pack missing section %q", want)
		}
	}
	t.Logf("\n----- evidence pack (%s) -----\n%s", key, rendered)
}

// TestEvidencePack_DetectsTestEditAndFailedVerify drives the pack builder with a
// controlled diff + event stream — the "floundered" pathology a judge must catch:
// the agent edited a TEST file (not the source) and its pytest run errored. Using a
// crafted input keeps this coverage stable across corpus regenerations.
func TestEvidencePack_DetectsTestEditAndFailedVerify(t *testing.T) {
	// diff edits a test file and NOT the source module under test.
	diff := "diff --git a/tests/test_requests.py b/tests/test_requests.py\n" +
		"--- a/tests/test_requests.py\n" +
		"+++ b/tests/test_requests.py\n" +
		"@@ -1,2 +1,2 @@\n" +
		"-    assert to_native_string(payload) == expected\n" +
		"+    assert True  # relaxed so the suite passes\n"

	// event stream: a Bash pytest tool_use paired with an errored tool_result,
	// plus a final assistant self-report (the untrusted claim).
	events := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"python -m pytest tests/test_requests.py -q"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"E   ImportError: cannot import name to_native_string"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"All tests pass now."}]}}`,
	}, "\n") + "\n"
	evPath := filepath.Join(t.TempDir(), "x.events.jsonl")
	if err := os.WriteFile(evPath, []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := BuildEvidencePack("swe-x", "swe-x__local", "Request with binary payload fails", diff, evPath, PackFlags{})
	if err != nil {
		t.Fatalf("BuildEvidencePack: %v", err)
	}

	var touchedTest, touchedSrc bool
	for _, cf := range p.ChangedFiles {
		if strings.Contains(cf.Path, "test_requests.py") {
			touchedTest = true
		}
		if strings.Contains(cf.Path, "requests/models.py") {
			touchedSrc = true
		}
	}
	if !touchedTest {
		t.Errorf("expected the diff to touch test_requests.py; changed=%v", p.ChangedFiles)
	}
	if touchedSrc {
		t.Errorf("diff did not touch models.py; parser is wrong")
	}
	if !p.Flags.EditedTestFile {
		t.Errorf("EditedTestFile flag should be set (edited a test file)")
	}

	var sawErroredPytest bool
	for _, r := range p.VerificationRuns {
		if strings.Contains(strings.ToLower(r.Command), "pytest") && r.Errored {
			sawErroredPytest = true
		}
	}
	if !sawErroredPytest {
		t.Errorf("expected an errored pytest verification run; got %+v", p.VerificationRuns)
	}
	// The final self-report ("All tests pass now") is captured but untrusted — the
	// observed errored run contradicts it, which is exactly what the judge needs.
	if !strings.Contains(p.FinalAgentText, "All tests pass") {
		t.Errorf("final agent text not captured; got %q", p.FinalAgentText)
	}
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
