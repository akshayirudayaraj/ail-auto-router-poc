package extract

import "strings"

// SignalKind is the implicit outcome cue mined from the following user turn.
type SignalKind string

const (
	SigNone      SignalKind = "none"
	SigRetry     SignalKind = "retry"
	SigPasteErr  SignalKind = "paste_error"
	SigNegative  SignalKind = "negative"
	SigSwitch    SignalKind = "switch"   // local->frontier escalation: STRONGEST
	SigMoveOn    SignalKind = "moveon"
	SigComplete  SignalKind = "complete" // session ended cleanly after the answer
)

// ImplicitLabel is a weak, noisy outcome label derived from user behavior.
// Per the brief it should be treated as a NOISY FEATURE anchored by the judge,
// not a clean label — the confidence reflects that.
type ImplicitLabel struct {
	Outcome    int        // 1 = adequate, 0 = inadequate
	Confidence float64    // [0,1]
	Signal     SignalKind
}

// confidence table per signal. The local->frontier switch is deliberately the
// most trusted signal; ordinary retries are the least.
var signalConfidence = map[SignalKind]float64{
	SigSwitch:   0.90,
	SigPasteErr: 0.80,
	SigNegative: 0.75,
	SigRetry:    0.65,
	SigMoveOn:   0.70,
	SigComplete: 0.55,
	SigNone:     0.50,
}

// InferSignal mines the implicit outcome for one served observation.
// isFrontier reports whether a model name is the (more expensive) frontier
// rung — a real gateway knows this from its own cost tiers.
func InferSignal(o ServedObs, isFrontier func(string) bool) ImplicitLabel {
	// No following user turn: the user moved on / left after the answer. Weak
	// success (they didn't push back), but low confidence — they may have just
	// given up.
	if o.NextUser == nil {
		return ImplicitLabel{Outcome: 1, Confidence: signalConfidence[SigComplete], Signal: SigComplete}
	}

	text := strings.ToLower(o.NextUser.Content)

	// STRONGEST signal: a user-driven escalation from a local model to the
	// frontier right after this (local) turn. Detected structurally from the
	// known cost tiers, not from wording.
	escalation := !isFrontier(o.Model) && o.NextModel != "" && isFrontier(o.NextModel)
	if escalation && !looksPositive(text) {
		return label(SigSwitch, 0)
	}

	switch {
	case looksLikePastedError(text):
		return label(SigPasteErr, 0)
	case looksNegative(text):
		return label(SigNegative, 0)
	case looksLikeRetry(text):
		return label(SigRetry, 0)
	case looksPositive(text):
		return label(SigMoveOn, 1)
	default:
		// ambiguous continuation: lean weak-success but flag low confidence
		return ImplicitLabel{Outcome: 1, Confidence: signalConfidence[SigNone], Signal: SigNone}
	}
}

func label(k SignalKind, outcome int) ImplicitLabel {
	return ImplicitLabel{Outcome: outcome, Confidence: signalConfidence[k], Signal: k}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func looksLikePastedError(s string) bool {
	// error-ish tokens, usually inside a pasted block
	return containsAny(s,
		"error", "panic", "exception", "traceback", "sqlstate", "data race",
		"undefined:", "cannot ", "failed", "timed out", "out of memory",
		"stack overflow", "not defined", "goroutine leak",
	) && (strings.Contains(s, "```") || containsAny(s, "i ran", "got:", "output"))
}

func looksNegative(s string) bool {
	return containsAny(s,
		"that's wrong", "thats wrong", "no, ", "still broken", "doesn't",
		"does not", "not working", "isn't", "is not", "incorrect", "nope",
	)
}

func looksLikeRetry(s string) bool {
	return containsAny(s,
		"didn't work", "didnt work", "rephrase", "try again", "again:",
		"let me try", "not what i", "same request",
	)
}

func looksPositive(s string) bool {
	return containsAny(s,
		"great", "works", "thanks", "perfect", "now:", "next", "awesome",
		"looks good", "that works",
	)
}

// SignalIsFailure reports whether a signal encodes an inadequate outcome.
func SignalIsFailure(k SignalKind) bool {
	switch k {
	case SigSwitch, SigPasteErr, SigNegative, SigRetry:
		return true
	default:
		return false
	}
}
