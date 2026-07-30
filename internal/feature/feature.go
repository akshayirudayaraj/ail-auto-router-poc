// Package feature turns a prompt (plus its turn type) into the cheap,
// model-free structural Features every router consumes. Everything here is
// derivable in a real Go gateway before any model is called — that is the
// whole point of a *predictive* router.
package feature

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

var codeFenceRe = regexp.MustCompile("(?s)```.*?```")

// hardKeywords correlate with model-relative difficulty for coding traffic.
// This is a difficulty prior, NOT a topic classifier (topic is ~always code).
var hardKeywords = []string{
	"concurren", "goroutine", "race condition", "deadlock", "mutex", "atomic",
	"distributed", "consensus", "async", "await", "thread-safe", "lock-free",
	"refactor", "optimize", "complexity", "algorithm", "dynamic programming",
	"recursion", "pointer", "memory leak", "garbage", "generics", "unsafe",
	"parser", "compiler", "interpreter", "regex", "cryptograph", "consistency",
	"migration", "transaction", "idempoten", "backpressure", "streaming",
	"reentrant", "invariant", "proof", "np-hard", "graph", "topological",
}

var imperativeVerbs = []string{
	"implement", "write", "fix", "refactor", "add", "create", "debug",
	"optimize", "design", "build", "remove", "update", "convert", "explain",
	"rewrite", "extend", "port", "migrate", "parse", "generate",
}

// Extract computes Features from prompt content and its turn type
// ("open" for the session's first task-stating prompt, else "followup").
func Extract(content, turnType string) schema.Features {
	lower := strings.ToLower(content)

	// Attached-context tokens = characters inside code fences / 4.
	var fenceChars int
	fences := codeFenceRe.FindAllString(content, -1)
	for _, f := range fences {
		fenceChars += len(f)
	}

	f := schema.Features{
		PromptLen:          len(content),
		PromptTokensApprox: approxTokens(content),
		AttachedCtxTokens:  fenceChars / 4,
		ToolCount:          countTools(content),
		TurnType:           turnType,
		CodeFenceCount:     len(fences),
		QuestionCount:      strings.Count(content, "?"),
		LineCount:          strings.Count(content, "\n") + 1,
	}
	f.ImperativeVerbCount = countAny(lower, imperativeVerbs)
	f.HardKeywordScore = hardScore(lower)
	f.DigitRatio = digitRatio(content)
	return f
}

func approxTokens(s string) int { return (len(s) + 3) / 4 }

// countTools looks for explicit tool-availability markers a gateway would know.
// The generator emits "[tools: a, b, c]" hints; absent that, it returns 0.
var toolsRe = regexp.MustCompile(`(?i)\[tools?:\s*([^\]]*)\]`)

func countTools(content string) int {
	m := toolsRe.FindStringSubmatch(content)
	if m == nil {
		return 0
	}
	parts := strings.Split(m[1], ",")
	n := 0
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			n++
		}
	}
	return n
}

func countAny(lowerText string, needles []string) int {
	n := 0
	for _, w := range needles {
		if strings.Contains(lowerText, w) {
			n++
		}
	}
	return n
}

// hardScore is a bounded [0,1] difficulty prior from keyword hits.
func hardScore(lowerText string) float64 {
	hits := 0
	for _, w := range hardKeywords {
		if strings.Contains(lowerText, w) {
			hits++
		}
	}
	// saturating: 0 -> 0, 4+ -> ~1
	s := float64(hits) / 4.0
	if s > 1 {
		s = 1
	}
	return s
}

func digitRatio(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	d := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			d++
		}
	}
	return float64(d) / float64(len(s))
}

// Vector flattens Features into a dense float64 slice for the linear models.
// Order is fixed and documented; changing it invalidates trained models.
func Vector(f schema.Features) []float64 {
	turnOpen := 0.0
	if f.TurnType == "open" {
		turnOpen = 1
	}
	return []float64{
		float64(f.PromptTokensApprox),
		float64(f.AttachedCtxTokens),
		float64(f.ToolCount),
		turnOpen,
		float64(f.CodeFenceCount),
		float64(f.QuestionCount),
		float64(f.ImperativeVerbCount),
		f.HardKeywordScore,
		float64(f.LineCount),
		f.DigitRatio,
	}
}

// VectorNames documents the Vector layout (for reports / debugging).
func VectorNames() []string {
	return []string{
		"prompt_tokens", "attached_ctx_tokens", "tool_count", "turn_open",
		"code_fences", "questions", "imperative_verbs", "hard_kw_score",
		"line_count", "digit_ratio",
	}
}
