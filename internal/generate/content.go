package generate

import (
	"fmt"
	"strings"
)

// signal is the implicit outcome cue a user turn expresses. Failure signals
// follow an inadequate answer; success signals follow an adequate one.
type signal string

const (
	sigRetry    signal = "retry"       // user rephrases/re-asks the same thing
	sigPasteErr signal = "paste_error" // user pastes an error/output back
	sigNegative signal = "negative"    // explicit correction language
	sigSwitch   signal = "switch"      // user-driven local->frontier escalation (STRONGEST)
	sigMoveOn   signal = "moveon"      // conversation advances to a new sub-task
)

// chooseSignal picks a plausible signal given the prior answer's adequacy.
// Failure signals are weighted toward the stronger "switch" only sometimes, so
// most failures look like ordinary retries/errors (realistic class balance).
func (g *Generator) chooseSignal(adequate bool) signal {
	if adequate {
		return sigMoveOn
	}
	r := g.rng.Float64()
	switch {
	case r < 0.35:
		return sigPasteErr
	case r < 0.60:
		return sigRetry
	case r < 0.80:
		return sigNegative
	default:
		return sigSwitch
	}
}

// renderResponse produces synthetic assistant content whose surface quality
// reflects the planted adequacy. NOTE (see DECISIONS): because this text is
// templated, the frontier judge grades a synthetic answer, so judge-vs-truth
// agreement in Pillar 1c is optimistic relative to real responses.
func (g *Generator) renderResponse(tk task, adequate bool, subIdx int) string {
	if adequate {
		tmpl := []string{
			"Here's a working implementation:\n```go\n%s\n```\nIt handles the edge cases you mentioned and passes the tests.",
			"Done. The key idea:\n```go\n%s\n```\nThis is correct and runs cleanly under -race.",
			"```go\n%s\n```\nThat should do it — I verified the behaviour on a couple of cases.",
		}
		return fmt.Sprintf(pick(g.rng, tmpl), goodSnippet(tk, subIdx))
	}
	// inadequate: truncated / wrong / hand-wavy
	tmpl := []string{
		"Here's a rough start:\n```go\n// TODO: not sure this is right\n%s\n```\nYou may need to adjust it.",
		"I think something like this could work, but I haven't tested it:\n```go\n%s\n```",
		"This is tricky. One approach:\n```go\n%s\n```\n(this might not compile)",
		"I'm not fully sure, but maybe restructure the whole thing?",
	}
	return fmt.Sprintf(pick(g.rng, tmpl), badSnippet(tk, subIdx))
}

// renderFollowup produces the user turn text for a given signal. moveon
// advances subIdx to the next sub-task.
func (g *Generator) renderFollowup(tk task, sig signal, subIdx *int) string {
	switch sig {
	case sigPasteErr:
		return fmt.Sprintf("I ran it and got:\n```\n%s\n```\nCan you fix it?", tk.ErrorSnippet)
	case sigRetry:
		return fmt.Sprintf("That didn't work. Let me rephrase: I need %s.", tk.Restate)
	case sigNegative:
		return fmt.Sprintf("No, that's wrong — it's still broken. %s doesn't behave as asked.", tk.Title)
	case sigSwitch:
		return fmt.Sprintf("This model isn't getting it. Let me switch to the stronger model. Again: %s.", tk.Restate)
	case sigMoveOn:
		st := tk.Subtasks[*subIdx%len(tk.Subtasks)]
		*subIdx++
		return fmt.Sprintf("Great, that works. Now: %s", st)
	default:
		return "Continue."
	}
}

func pick(rng interface{ Intn(int) int }, xs []string) string {
	return xs[rng.Intn(len(xs))]
}

// goodSnippet / badSnippet return short code-like blobs so features (code
// fences, length) look realistic. They are not meant to compile.
func goodSnippet(tk task, subIdx int) string {
	base := strings.ReplaceAll(strings.ToLower(tk.Title), "-", "_")
	return fmt.Sprintf("func %s(in Input) (Output, error) {\n    // handles v%d correctly\n    return process(in), nil\n}", base, subIdx)
}

func badSnippet(tk task, subIdx int) string {
	base := strings.ReplaceAll(strings.ToLower(tk.Title), "-", "_")
	return fmt.Sprintf("func %s(in Input) Output {\n    panic(\"unimplemented\") // v%d\n}", base, subIdx)
}
