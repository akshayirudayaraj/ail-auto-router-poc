package extract

import (
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

func b(v bool) *bool { return &v }

func TestReconstructOrders(t *testing.T) {
	// deliberately out of order
	turns := []schema.RawTurn{
		{SessionID: "s1", TurnIndex: 2, Role: schema.RoleUser, Content: "b"},
		{SessionID: "s1", TurnIndex: 0, Role: schema.RoleUser, Content: "a"},
		{SessionID: "s1", TurnIndex: 1, Role: schema.RoleAssistant, Content: "resp", ServedModel: "m"},
	}
	s := Reconstruct(turns)
	if len(s) != 1 || len(s[0].Turns) != 3 {
		t.Fatalf("bad reconstruction: %+v", s)
	}
	if s[0].Turns[0].TurnIndex != 0 || s[0].Turns[2].TurnIndex != 2 {
		t.Fatal("turns not ordered by index")
	}
}

func TestObservationsExposeNextUser(t *testing.T) {
	turns := []schema.RawTurn{
		{SessionID: "s1", TurnIndex: 0, Role: schema.RoleUser, Content: "do X"},
		{SessionID: "s1", TurnIndex: 1, Role: schema.RoleAssistant, Content: "ok", ServedModel: "llama"},
		{SessionID: "s1", TurnIndex: 2, Role: schema.RoleUser, Content: "great, now Y", TrueSignal: "moveon"},
		{SessionID: "s1", TurnIndex: 3, Role: schema.RoleAssistant, Content: "ok2", ServedModel: "claude-sonnet-5"},
	}
	obs := Observations(Reconstruct(turns))
	if len(obs) != 2 {
		t.Fatalf("want 2 assistant obs, got %d", len(obs))
	}
	if obs[0].Prompt != "do X" || obs[0].TurnType != "open" {
		t.Fatalf("bad prompt/turntype: %+v", obs[0])
	}
	if obs[0].NextUser == nil || obs[0].NextModel != "claude-sonnet-5" {
		t.Fatalf("next user/model not exposed: %+v", obs[0])
	}
}

func TestInferSignalKinds(t *testing.T) {
	isFrontier := func(m string) bool { return m == "claude-sonnet-5" }
	mk := func(nextContent, curModel, nextModel string) ServedObs {
		var nu *schema.RawTurn
		if nextContent != "" {
			nu = &schema.RawTurn{Role: schema.RoleUser, Content: nextContent}
		}
		return ServedObs{Model: curModel, NextModel: nextModel, NextUser: nu}
	}
	cases := []struct {
		name    string
		o       ServedObs
		wantSig SignalKind
		wantOut int
	}{
		{"switch", mk("This model isn't getting it, switching to the stronger model. Again: X", "llama", "claude-sonnet-5"), SigSwitch, 0},
		{"paste_error", mk("I ran it and got:\n```\npanic: nil pointer\n```", "llama", "llama"), SigPasteErr, 0},
		{"negative", mk("No, that's wrong — it's still broken.", "llama", "llama"), SigNegative, 0},
		{"retry", mk("That didn't work. Let me rephrase: I need X.", "llama", "llama"), SigRetry, 0},
		{"moveon", mk("Great, that works. Now: add tests.", "llama", "llama"), SigMoveOn, 1},
		{"complete_end", mk("", "llama", ""), SigComplete, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lab := InferSignal(c.o, isFrontier)
			if lab.Signal != c.wantSig {
				t.Fatalf("signal=%s want %s", lab.Signal, c.wantSig)
			}
			if lab.Outcome != c.wantOut {
				t.Fatalf("outcome=%d want %d", lab.Outcome, c.wantOut)
			}
		})
	}
}

// TestStripHiddenGuard confirms extraction's ground-truth firewall: a stripped
// turn reports no hidden fields, an un-stripped one does.
func TestStripHiddenGuard(t *testing.T) {
	raw := schema.RawTurn{TrueAdequate: b(true), TrueDifficulty: fptr(1.0), TrueSignal: "switch"}
	if !raw.HasHidden() {
		t.Fatal("expected hidden fields present")
	}
	if raw.StripHidden().HasHidden() {
		t.Fatal("StripHidden left hidden fields")
	}
}

func fptr(f float64) *float64 { return &f }
