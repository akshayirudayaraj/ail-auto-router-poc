package label

import (
	"sort"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/extract"
	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// HeuristicVersion stamps implicit LabelRecords.
const HeuristicVersion = "heuristics-v1"

// HeuristicLabels mines implicit outcome labels from ONE session's RawTurn log —
// the simulated user's reactions (OFFLINE_ENGINE_PLAN §5). It reuses the existing
// signal miner (internal/extract) and returns one implicit LabelRecord per served
// model, keyed on that model's LAST turn's reaction: a local→frontier escalation
// lands on the local model as a failure (switch), while the model that served the
// final turn gets the session's closing verdict.
//
// Multi-turn sim-user sessions are where this has signal. A single-shot session
// (no following user turn) yields only the weak "complete" default (outcome 1) —
// which is exactly why heuristics are the weakest source and fusion is judge-primary.
func HeuristicLabels(turns []schema.RawTurn, isFrontier func(string) bool, ident LabelRecord, ts int64) []LabelRecord {
	sessions := extract.Reconstruct(turns)
	obs := extract.Observations(sessions)

	// Keep each served model's LAST observation (its final reaction is the verdict).
	last := map[string]extract.ServedObs{}
	for _, o := range obs {
		if o.Model == "" {
			continue
		}
		if cur, ok := last[o.Model]; !ok || o.TurnIndex > cur.TurnIndex {
			last[o.Model] = o
		}
	}

	models := make([]string, 0, len(last))
	for m := range last {
		models = append(models, m)
	}
	sort.Strings(models)

	out := make([]LabelRecord, 0, len(models))
	for _, m := range models {
		o := last[m]
		lab := extract.InferSignal(o, isFrontier)
		rec := ident
		rec.SessionID = o.SessionID
		rec.Model = m
		rec.LabelSource = schema.LabelImplicit
		rec.Outcome = lab.Outcome
		rec.LabelConfidence = lab.Confidence
		rec.LabelerVersion = HeuristicVersion
		rec.Timestamp = ts
		rec.Evidence = map[string]any{
			"signal":            string(lab.Signal),
			"turn_index":        o.TurnIndex,
			"had_user_reaction": o.NextUser != nil, // false => weak default, not a real reaction
		}
		out = append(out, rec)
	}
	return out
}

// HeuristicLabelsFromSession reads a session's .session.jsonl and mines it.
func HeuristicLabelsFromSession(sessionPath string, isFrontier func(string) bool, ident LabelRecord, ts int64) ([]LabelRecord, error) {
	turns, err := extract.LoadRaw(sessionPath, false)
	if err != nil {
		return nil, err
	}
	return HeuristicLabels(turns, isFrontier, ident, ts), nil
}

// FrontierPredicate returns an isFrontier func recognizing the configured frontier
// model plus the usual Claude/opus names (so escalation/switch is detectable).
func FrontierPredicate(frontierModel string) func(string) bool {
	return func(m string) bool {
		return m == frontierModel || m == "opus" || strings.HasPrefix(m, "claude")
	}
}
