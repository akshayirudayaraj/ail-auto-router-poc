package agentic

import (
	"context"
	"testing"

	"github.com/akshayirudayaraj/ail-routing-test/internal/config"
)

// TestBuildGoldPairsArms verifies that local+frontier results for a task collapse
// into one dual-arm GoldRow with executed outcomes, billable-token costs, and
// Executable=true — and that a frontier-only task still yields a row.
func TestBuildGoldPairsArms(t *testing.T) {
	results := []Result{
		// task A: local FAIL, frontier PASS -> the cell-B case
		{TaskID: "A", Arm: "local", Resolved: false, InputTokens: 100, OutputTokens: 20},
		{TaskID: "A", Arm: "frontier", Resolved: true, InputTokens: 200, OutputTokens: 40},
		// task B: both PASS
		{TaskID: "B", Arm: "local", Resolved: true, InputTokens: 50, OutputTokens: 10},
		{TaskID: "B", Arm: "frontier", Resolved: true, InputTokens: 60, OutputTokens: 10},
		// task C: frontier only (local arm blocked)
		{TaskID: "C", Arm: "frontier", Resolved: true, InputTokens: 30, OutputTokens: 5},
	}
	tasks := map[string]Task{
		"A": {ID: "A", Tier: "hard", Issue: "fix the parser precedence bug"},
		"B": {ID: "B", Tier: "easy", Issue: "fix a typo"},
		"C": {ID: "C", Tier: "medium", Issue: "merge intervals"},
	}
	rows, meta, err := BuildGold(context.Background(), config.Default(), results, tasks, nil)
	if err != nil {
		t.Fatalf("BuildGold: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if !meta.Executable {
		t.Errorf("meta.Executable should be true")
	}
	if meta.LocalArmMissing != 1 {
		t.Errorf("want LocalArmMissing=1 (task C), got %d", meta.LocalArmMissing)
	}

	byID := map[string]int{}
	for i, r := range rows {
		byID[r.PromptID] = i
	}
	a := rows[byID["A"]]
	if a.OutcomeLocal != 0 || a.OutcomeFrontier != 1 {
		t.Errorf("task A should be local=0 frontier=1 (cell-B), got %d/%d", a.OutcomeLocal, a.OutcomeFrontier)
	}
	if !a.Executable {
		t.Errorf("row A should be Executable")
	}
	// cost = billable(input+output) * price, frontier 15x
	if got := a.CostLocal; got != float64(120)*priceLocal {
		t.Errorf("A CostLocal = %v, want %v", got, float64(120)*priceLocal)
	}
	if got := a.CostFrontier; got != float64(240)*priceFrontier {
		t.Errorf("A CostFrontier = %v, want %v", got, float64(240)*priceFrontier)
	}
	if a.PromptText != tasks["A"].Issue {
		t.Errorf("PromptText should be the issue text")
	}

	b := rows[byID["B"]]
	if b.OutcomeLocal != 1 || b.OutcomeFrontier != 1 {
		t.Errorf("task B should be both-pass 1/1, got %d/%d", b.OutcomeLocal, b.OutcomeFrontier)
	}

	// frontier-only task C: local missing -> OutcomeLocal defaults to 0
	c := rows[byID["C"]]
	if c.OutcomeLocal != 0 || c.OutcomeFrontier != 1 {
		t.Errorf("task C (frontier only) should be 0/1, got %d/%d", c.OutcomeLocal, c.OutcomeFrontier)
	}
}

// TestBuildGoldRequiresFrontier verifies a local-only task is skipped (a dual-arm
// row needs at least the frontier outcome).
func TestBuildGoldRequiresFrontier(t *testing.T) {
	results := []Result{{TaskID: "X", Arm: "local", Resolved: true, InputTokens: 10, OutputTokens: 2}}
	tasks := map[string]Task{"X": {ID: "X", Issue: "y"}}
	_, _, err := BuildGold(context.Background(), config.Default(), results, tasks, nil)
	if err == nil {
		t.Fatalf("expected error when no frontier arm present")
	}
}
