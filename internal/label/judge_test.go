package label

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// fakeAssessor returns scripted verdicts in order, so the consensus/escalation
// logic can be tested with no model call.
type fakeAssessor struct {
	verdicts []Verdict
	calls    int
}

func (f *fakeAssessor) Assess(_ context.Context, _ string) (Verdict, error) {
	v := f.verdicts[f.calls%len(f.verdicts)]
	f.calls++
	return v, nil
}

func TestJudge_HighConfidenceNoEscalation(t *testing.T) {
	fa := &fakeAssessor{verdicts: []Verdict{{Adequate: false, Confidence: 0.95, Rationale: "only edited tests"}}}
	rec, votes, err := JudgeSession(context.Background(), fa,
		EvidencePack{TaskID: "t", SessionID: "s"}, LabelRecord{Arm: "local"}, DefaultJudgeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if fa.calls != 1 {
		t.Errorf("high-confidence verdict should NOT escalate; got %d calls", fa.calls)
	}
	if len(votes) != 1 || rec.Outcome != 0 || rec.LabelSource != schema.LabelJudge {
		t.Errorf("unexpected record: outcome=%d source=%s votes=%d", rec.Outcome, rec.LabelSource, len(votes))
	}
}

func TestJudge_LowConfidenceEscalatesAndVotes(t *testing.T) {
	// First verdict low-confidence -> escalate to K=3; majority is inadequate.
	fa := &fakeAssessor{verdicts: []Verdict{
		{Adequate: true, Confidence: 0.55, Rationale: "unsure"},
		{Adequate: false, Confidence: 0.8, Rationale: "no source change"},
		{Adequate: false, Confidence: 0.7, Rationale: "tests only"},
	}}
	rec, votes, err := JudgeSession(context.Background(), fa,
		EvidencePack{TaskID: "t", SessionID: "s"}, LabelRecord{}, DefaultJudgeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if fa.calls != 3 || len(votes) != 3 {
		t.Errorf("low-confidence should escalate to 3 votes; got calls=%d votes=%d", fa.calls, len(votes))
	}
	if rec.Outcome != 0 {
		t.Errorf("majority (2/3) inadequate should win; got outcome=%d", rec.Outcome)
	}
	if rec.LabelConfidence <= 0 || rec.LabelConfidence > 1 {
		t.Errorf("confidence out of range: %v", rec.LabelConfidence)
	}
}

func TestParseVerdict(t *testing.T) {
	cases := []string{
		`{"adequate": false, "confidence": 0.9, "rationale": "x"}`,
		"```json\n{\"adequate\": true, \"confidence\": 0.8, \"rationale\": \"ok\"}\n```",
		`sure — here: {"adequate": false, "confidence": 1.2, "rationale": "y"} done`,
	}
	for i, c := range cases {
		v, err := parseVerdict(c)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if v.Confidence < 0 || v.Confidence > 1 {
			t.Errorf("case %d: confidence not clamped: %v", i, v.Confidence)
		}
	}
	if _, err := parseVerdict("no json here"); err == nil {
		t.Errorf("expected error on non-JSON reply")
	}
}

// TestJudge_OnRealSampleWithScriptedVerdict wires the DETERMINISTIC evidence pack
// from the real gpt-oss psf-2931 sample into JudgeSession with a scripted verdict,
// proving the end-to-end path (pack -> rendered -> assessor -> LabelRecord) holds
// without needing the subscription. The scripted verdict mimics what a judge should
// say about this session (inadequate: edited a test file, no source change).
func TestJudge_OnRealSampleWithScriptedVerdict(t *testing.T) {
	results := filepath.Join(repoRoot(), "agentic", "results")
	tasks := filepath.Join(repoRoot(), "agentic", "tasks")
	pack, err := BuildFromResults(results, tasks, realSampleKey(t))
	if err != nil {
		t.Fatal(err)
	}
	fa := &fakeAssessor{verdicts: []Verdict{{Adequate: false, Confidence: 0.9,
		Rationale: "only test_requests.py changed; requests/models.py untouched; own pytest errored"}}}
	rec, _, err := JudgeSession(context.Background(), fa, pack,
		LabelRecord{Arm: "local", Model: "gpt-oss:20b", Split: "train",
			Provenance: "swe_verified", HasExecutableOracle: true}, DefaultJudgeOptions())
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != 0 || rec.TaskID != "swe-psf__requests-2931" {
		t.Errorf("unexpected: outcome=%d task=%s", rec.Outcome, rec.TaskID)
	}
	if rec.Evidence["rubric_version"] != RubricVersion {
		t.Errorf("rubric version not stamped")
	}
}
