package label

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/backend"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// RubricVersion is stamped into judge LabelRecords; bump it when the rubric or the
// evidence-pack shape changes so labels are attributable/reproducible.
const RubricVersion = "judge-rubric-v1"

// Verdict is one judge assessment of a session's adequacy.
type Verdict struct {
	Adequate   bool    `json:"adequate"`
	Confidence float64 `json:"confidence"` // [0,1]
	Rationale  string  `json:"rationale"`
}

// Assessor turns a rendered evidence pack into a Verdict. The real one calls the
// frontier model; tests use a fake. Keeping the model call behind this interface
// leaves the consensus/escalation logic pure and testable with no API.
type Assessor interface {
	Assess(ctx context.Context, renderedPack string) (Verdict, error)
}

// JudgeOptions tune the targeted self-consistency (§4.4 / §1 of the plan): a single
// call by default, escalating to K votes only when the first verdict is
// low-confidence (near the decision threshold) or when forced (calibration sample).
type JudgeOptions struct {
	K             int     // votes when escalated (e.g. 3); <2 disables escalation
	LowConfidence float64 // escalate if the single verdict's confidence < this
	ForceK        bool    // always run K (used for the calibration sample)
}

// DefaultJudgeOptions are the everyday settings: one call, k=3 only near-threshold.
func DefaultJudgeOptions() JudgeOptions {
	return JudgeOptions{K: 3, LowConfidence: 0.67, ForceK: false}
}

// JudgeSession assesses one session's adequacy and returns a completed judge
// LabelRecord plus the raw votes. `ident` carries the identity fields
// (Model/Arm/Split/Provenance/HasExecutableOracle/Timestamp); TaskID/SessionID are
// taken from the pack. The rendered pack is what the judge actually sees.
func JudgeSession(ctx context.Context, a Assessor, pack EvidencePack, ident LabelRecord, opts JudgeOptions) (LabelRecord, []Verdict, error) {
	rendered := pack.Render()

	first, err := a.Assess(ctx, rendered)
	if err != nil {
		return LabelRecord{}, nil, err
	}
	votes := []Verdict{first}

	if opts.K >= 2 && (opts.ForceK || first.Confidence < opts.LowConfidence) {
		for i := 1; i < opts.K; i++ {
			v, err := a.Assess(ctx, rendered)
			if err != nil {
				return LabelRecord{}, votes, err
			}
			votes = append(votes, v)
		}
	}

	adequate, conf := consensus(votes)

	rec := ident
	rec.TaskID = pack.TaskID
	rec.SessionID = pack.SessionID
	rec.LabelSource = schema.LabelJudge
	rec.LabelConfidence = conf
	rec.LabelerVersion = RubricVersion
	if adequate {
		rec.Outcome = 1
	} else {
		rec.Outcome = 0
	}
	rec.Evidence = map[string]any{
		"adequate":          adequate,
		"confidence":        conf,
		"rationale":         votes[len(votes)-1].Rationale,
		"k_votes":           len(votes),
		"evidence_pack_ref": pack.SessionID,
		"evidence_pack_ver": EvidencePackVersion,
		"rubric_version":    RubricVersion,
	}
	return rec, votes, nil
}

// consensus reduces votes to (adequate, confidence). One vote → its own values.
// Multiple → majority label, confidence = agreement fraction × mean confidence of
// the winning side (so a 2/3 split with lukewarm votes lands low).
func consensus(votes []Verdict) (bool, float64) {
	if len(votes) == 0 {
		return false, 0
	}
	if len(votes) == 1 {
		return votes[0].Adequate, votes[0].Confidence
	}
	var yes, no int
	var yesConf, noConf float64
	for _, v := range votes {
		if v.Adequate {
			yes++
			yesConf += v.Confidence
		} else {
			no++
			noConf += v.Confidence
		}
	}
	total := float64(len(votes))
	if yes >= no {
		agree := float64(yes) / total
		mean := yesConf / float64(max1(yes))
		return true, agree * mean
	}
	agree := float64(no) / total
	mean := noConf / float64(max1(no))
	return false, agree * mean
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// ---- the real, backend-backed Assessor -------------------------------------

// BackendAssessor calls the frontier judge model via the shared backend. The
// evidence pack is delivered as a single user message (robust across backends that
// don't take a system role), and the model is asked for a strict JSON verdict.
type BackendAssessor struct {
	Backend backend.Backend
	Model   string // the judge model (e.g. the frontier/judge model id)
}

func (b BackendAssessor) Assess(ctx context.Context, renderedPack string) (Verdict, error) {
	msgs := []backend.Message{{Role: "user", Content: judgePrompt(renderedPack)}}
	out, err := b.Backend.Generate(ctx, b.Model, msgs)
	if err != nil {
		return Verdict{}, err
	}
	return parseVerdict(out)
}

// judgeRubric is the instruction the judge follows. It is deliberately explicit
// about the two failure modes this corpus surfaces: touching test files instead of
// source, and believing the agent's self-report over observed test output.
const judgeRubric = `You are grading whether an AI coding agent ADEQUATELY resolved a software issue,
from a distilled evidence pack. You cannot run code; assess from the evidence.

Rules:
- A fix must change the SOURCE to resolve the issue. Editing or deleting TEST files
  is NOT a fix — if the only changes are to test files, it is inadequate.
- Trust OBSERVED verification output (real test/build runs) over the AGENT CLAIM,
  which is self-reported and may be wrong. If the agent's own tests errored/failed
  and nothing shows them passing, lean inadequate.
- An empty patch is inadequate. A change unrelated to the issue is inadequate.
- "adequate" means the change plausibly makes the issue's expected behavior pass.

Respond with ONLY a JSON object, no prose:
{"adequate": true|false, "confidence": 0.0-1.0, "rationale": "one sentence"}`

func judgePrompt(renderedPack string) string {
	return judgeRubric + "\n\nEVIDENCE PACK:\n" + renderedPack
}

// parseVerdict extracts the JSON verdict from the model's reply, tolerating code
// fences or surrounding prose by taking the outermost {...}.
func parseVerdict(text string) (Verdict, error) {
	s := strings.TrimSpace(text)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < 0 || j < i {
		return Verdict{}, fmt.Errorf("no JSON object in judge reply: %q", truncate(s, 120))
	}
	var raw struct {
		Adequate   *bool    `json:"adequate"`
		Confidence *float64 `json:"confidence"`
		Rationale  string   `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(s[i:j+1]), &raw); err != nil {
		return Verdict{}, fmt.Errorf("parse judge JSON: %w", err)
	}
	if raw.Adequate == nil {
		return Verdict{}, fmt.Errorf("judge reply missing 'adequate'")
	}
	v := Verdict{Adequate: *raw.Adequate, Rationale: raw.Rationale, Confidence: 0.5}
	if raw.Confidence != nil {
		v.Confidence = clamp01(*raw.Confidence)
	}
	return v, nil
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
