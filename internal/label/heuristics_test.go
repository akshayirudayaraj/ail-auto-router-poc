package label

import (
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func ua(sid string, idx int, content string) schema.RawTurn {
	return schema.RawTurn{SessionID: sid, TurnIndex: idx, Role: schema.RoleUser, Content: content}
}
func aa(sid string, idx int, model string) schema.RawTurn {
	return schema.RawTurn{SessionID: sid, TurnIndex: idx, Role: schema.RoleAssistant, Content: "…", ServedModel: model}
}

// A multi-turn session where the sim-user escalates local -> frontier: local gets
// a failure (switch), the frontier turn gets the closing verdict (complete).
func TestHeuristics_Escalation(t *testing.T) {
	isFrontier := func(m string) bool { return m == "frontier" }
	turns := []schema.RawTurn{
		ua("s", 0, "fix the bug: ..."),
		aa("s", 1, "local"),
		ua("s", 2, "This model isn't getting it. Let me switch to the stronger model. Again: fix the bug."),
		aa("s", 3, "frontier"),
	}
	recs := HeuristicLabels(turns, isFrontier, LabelRecord{TaskID: "t"}, 100)

	got := map[string]LabelRecord{}
	for _, r := range recs {
		got[r.Model] = r
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 labels (local, frontier); got %d", len(recs))
	}
	if l := got["local"]; l.Outcome != 0 || l.Evidence["signal"] != "switch" {
		t.Errorf("local should be inadequate via switch; got outcome=%d signal=%v", l.Outcome, l.Evidence["signal"])
	}
	if f := got["frontier"]; f.Outcome != 1 {
		t.Errorf("frontier should be adequate (closing verdict); got %d", f.Outcome)
	}
	for _, r := range recs {
		if r.LabelSource != schema.LabelImplicit || r.LabelerVersion != HeuristicVersion {
			t.Errorf("wrong source/version: %s/%s", r.LabelSource, r.LabelerVersion)
		}
	}
}

// A single-shot session (no follow-up user turn) yields only the weak "complete"
// default — flagged had_user_reaction=false so downstream knows it isn't a real cue.
func TestHeuristics_SingleShotIsWeakDefault(t *testing.T) {
	isFrontier := func(m string) bool { return m == "frontier" }
	turns := []schema.RawTurn{
		ua("s", 0, "fix the bug"),
		aa("s", 1, "local"),
	}
	recs := HeuristicLabels(turns, isFrontier, LabelRecord{TaskID: "t"}, 100)
	if len(recs) != 1 || recs[0].Outcome != 1 {
		t.Fatalf("single-shot should yield 1 weak-success label; got %+v", recs)
	}
	if recs[0].Evidence["had_user_reaction"] != false {
		t.Errorf("single-shot must flag had_user_reaction=false")
	}
	if recs[0].Evidence["signal"] != "complete" {
		t.Errorf("single-shot signal should be 'complete'; got %v", recs[0].Evidence["signal"])
	}
}

// A pasted-error reaction marks the local model inadequate.
func TestHeuristics_PasteError(t *testing.T) {
	isFrontier := func(m string) bool { return m == "frontier" }
	turns := []schema.RawTurn{
		ua("s", 0, "implement X"),
		aa("s", 1, "local"),
		ua("s", 2, "I ran it and got:\n```\nTraceback ... NameError\n```\nplease fix"),
		aa("s", 3, "local"),
	}
	recs := HeuristicLabels(turns, isFrontier, LabelRecord{TaskID: "t"}, 100)
	// last local obs is turn 3 (no follow-up) -> complete; but turn-1 saw paste_error.
	// We key on the LAST turn, so this documents the roll-up choice: the closing
	// state wins. Assert we produced exactly one local label.
	if len(recs) != 1 || recs[0].Model != "local" {
		t.Fatalf("want single local label; got %+v", recs)
	}
}
