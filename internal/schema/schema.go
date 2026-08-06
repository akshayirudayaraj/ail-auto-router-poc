// Package schema defines the data contracts shared across every pillar of the
// framework: raw logs (Pillar 1a), the structured pointwise/pairwise datasets
// (Pillar 1b), and the dual-arm gold set (Pillar 3).
//
// These structs are deliberately close to what a real Go gateway could emit
// with minimal reshaping. See README.md ("Real production logging
// requirements") for exactly which fields production must provide.
//
// IMPORTANT — the hidden-field convention:
//
//	Any field whose JSON tag begins with an underscore ("_true_adequate",
//	"_difficulty", ...) is PLANTED GROUND TRUTH. It exists only so the
//	extractor-quality report (Pillar 1c) can grade the extraction. NO
//	extraction, training, or routing code may read an underscore field. The
//	extractor is handed logs with those fields stripped (see StripHidden).
package schema

import (
	"encoding/json"
	"strings"
)

// Role is the author of a turn in a session.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// LabelSource ranks how trustworthy an outcome label is. The ordering matters:
// the eval harness enforces that eval labels come from a STRICTLY STRONGER
// source than training labels (executed > human > frontier-judge > implicit).
type LabelSource string

const (
	LabelImplicit  LabelSource = "implicit"  // mined from user-behavior heuristics (weakest)
	LabelJudge     LabelSource = "judge"     // frontier-as-judge verdict
	LabelConsensus LabelSource = "consensus" // judge + implicit fused (no oracle); see internal/label/fuse.go
	LabelHuman     LabelSource = "human"     // human annotation
	LabelExecuted  LabelSource = "executed"  // executable ground truth, e.g. unit tests (strongest)
)

// LabelStrength returns an ordinal used to compare label sources. Higher is
// stronger. Unknown sources return -1. Consensus (judge+implicit fusion) sits
// above raw judge but below any oracle/human ground truth.
func LabelStrength(s LabelSource) int {
	switch s {
	case LabelImplicit:
		return 0
	case LabelJudge:
		return 1
	case LabelConsensus:
		return 2
	case LabelHuman:
		return 3
	case LabelExecuted:
		return 4
	default:
		return -1
	}
}

// ------------------------------------------------------------------------
// Pillar 1a — raw log record (JSONL, one object per line)
// ------------------------------------------------------------------------

// RawTurn is a single turn in a raw session log. Assistant turns carry a
// served model; user turns do not. Underscore fields are planted ground truth
// (see package doc) and must be stripped before extraction runs.
type RawTurn struct {
	SessionID string `json:"session_id"`
	TurnIndex int    `json:"turn_index"`
	Timestamp int64  `json:"timestamp"` // unix seconds
	Role      Role   `json:"role"`
	Content   string `json:"content"`

	// ServedModel is set on assistant turns: which model produced this turn.
	ServedModel string `json:"served_model,omitempty"`

	// Propensity is the probability the logging policy assigned to the served
	// model at decision time. Non-nil only when the log was produced by a
	// stochastic (epsilon-greedy) policy. Required for off-policy evaluation.
	Propensity *float64 `json:"propensity,omitempty"`

	// --- HIDDEN ground truth (prefix "_"): graders only, never extraction ---

	// TrueAdequate is the planted outcome: was this served model's answer
	// actually adequate for the prompt? Set on assistant turns.
	TrueAdequate *bool `json:"_true_adequate,omitempty"`
	// TrueDifficulty is the planted model-relative difficulty in [0,1].
	TrueDifficulty *float64 `json:"_true_difficulty,omitempty"`
	// TrueTopic is the planted task topic (all "code"-ish here by design).
	TrueTopic string `json:"_true_topic,omitempty"`
	// TrueSignal names the implicit signal the generator planted for the NEXT
	// user turn (e.g. "retry", "paste_error", "negative", "switch", "moveon").
	TrueSignal string `json:"_true_signal,omitempty"`
}

// StripHidden returns a copy of the turn with all underscore ground-truth
// fields cleared. Extraction is always run on stripped turns.
func (t RawTurn) StripHidden() RawTurn {
	t.TrueAdequate = nil
	t.TrueDifficulty = nil
	t.TrueTopic = ""
	t.TrueSignal = ""
	return t
}

// HasHidden reports whether any underscore field is populated. Used by a test
// guard that fails if extraction is ever handed un-stripped turns.
func (t RawTurn) HasHidden() bool {
	return t.TrueAdequate != nil || t.TrueDifficulty != nil || t.TrueTopic != "" || t.TrueSignal != ""
}

// ------------------------------------------------------------------------
// Pillar 1b — structured datasets
// ------------------------------------------------------------------------

// Features are the prompt-derived structural signals used by every router.
// They are deliberately cheap to compute in a real gateway (no model call).
type Features struct {
	PromptLen           int     `json:"prompt_len"`              // characters
	PromptTokensApprox  int     `json:"prompt_tokens_approx"`    // len/4 heuristic
	AttachedCtxTokens   int     `json:"attached_context_tokens"` // pasted code/files
	ToolCount           int     `json:"tool_count"`              // tools available/used
	TurnType            string  `json:"turn_type"`               // "open" | "followup"
	CodeFenceCount      int     `json:"code_fence_count"`
	QuestionCount       int     `json:"question_count"`
	ImperativeVerbCount int     `json:"imperative_verb_count"`
	HardKeywordScore    float64 `json:"hard_keyword_score"` // concurrency/refactor/etc.
	LineCount           int     `json:"line_count"`
	DigitRatio          float64 `json:"digit_ratio"`
}

// PointwiseRow is one observed (model, prompt) -> outcome record. This is the
// primary training/eval unit for IRT and kNN.
type PointwiseRow struct {
	PromptID   string    `json:"prompt_id"`
	PromptText string    `json:"prompt_text"`
	Features   Features  `json:"features"`
	Embedding  []float32 `json:"embedding,omitempty"`

	Model   string `json:"model"`   // which model served this prompt
	Outcome int    `json:"outcome"` // 1 = adequate, 0 = inadequate

	LabelSource     LabelSource `json:"label_source"`
	LabelConfidence float64     `json:"label_confidence"` // [0,1]

	SessionID  string   `json:"session_id"`
	TurnIndex  int      `json:"turn_index"`
	Timestamp  int64    `json:"timestamp"`
	Propensity *float64 `json:"propensity,omitempty"` // logging-policy prob, nullable
}

// PairwiseRow is a preference between two models on the same prompt. This is
// the training unit for the RouteLLM-style logistic router. It can be derived
// from two PointwiseRows on the same prompt (see extract.DerivePairwise).
type PairwiseRow struct {
	PromptID   string    `json:"prompt_id"`
	PromptText string    `json:"prompt_text"`
	Features   Features  `json:"features"`
	Embedding  []float32 `json:"embedding,omitempty"`

	ModelA    string      `json:"model_a"`
	ModelB    string      `json:"model_b"`
	Preferred string      `json:"preferred"` // "a" | "b" | "tie"
	Source    LabelSource `json:"source"`
}

// GoldRow is a dual-arm benchmark row where BOTH arms' outcomes are known.
// This is the only source that yields trustworthy ABSOLUTE cost/quality
// numbers. Executable=true leaves a seam for real unit-test ground truth.
type GoldRow struct {
	PromptID   string    `json:"prompt_id"`
	PromptText string    `json:"prompt_text"`
	Features   Features  `json:"features"`
	Embedding  []float32 `json:"embedding,omitempty"`

	OutcomeLocal    int `json:"outcome_local"`    // 1 = local adequate
	OutcomeFrontier int `json:"outcome_frontier"` // 1 = frontier adequate

	LocalModel    string `json:"local_model"`
	FrontierModel string `json:"frontier_model"`

	CostLocal    float64 `json:"cost_local"` // relative units (e.g. tokens*price)
	CostFrontier float64 `json:"cost_frontier"`

	Executable bool `json:"executable"` // true when outcomes come from executed tests
}

// ------------------------------------------------------------------------
// JSONL helpers
// ------------------------------------------------------------------------

// MarshalLine marshals v to a single JSON line (no trailing newline).
func MarshalLine(v any) ([]byte, error) { return json.Marshal(v) }

// IsHiddenField reports whether a JSON key is a planted ground-truth field.
func IsHiddenField(key string) bool { return strings.HasPrefix(key, "_") }
