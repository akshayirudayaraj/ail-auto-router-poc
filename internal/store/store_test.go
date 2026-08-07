package store

import (
	"encoding/json"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileStore(dir)

	// empty history is not an error
	runs, err := fs.ListEvalRuns()
	if err != nil {
		t.Fatalf("ListEvalRuns on empty: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("want 0 runs, got %d", len(runs))
	}

	// save two runs; newer CreatedAt should sort first
	for _, r := range []EvalRun{
		{ID: "a", CreatedAt: "2026-08-07T10:00:00Z", NGold: 20, Payload: json.RawMessage(`{"leaderboard":[]}`)},
		{ID: "b", CreatedAt: "2026-08-07T12:00:00Z", NGold: 20, Payload: json.RawMessage(`{"leaderboard":[]}`)},
	} {
		if err := fs.SaveEvalRun(r); err != nil {
			t.Fatalf("SaveEvalRun: %v", err)
		}
	}

	runs, err = fs.ListEvalRuns()
	if err != nil {
		t.Fatalf("ListEvalRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("want 2 runs, got %d", len(runs))
	}
	if runs[0].ID != "b" {
		t.Fatalf("want newest-first (b), got %q", runs[0].ID)
	}
	if string(runs[0].Payload) != `{"leaderboard":[]}` {
		t.Fatalf("payload not preserved: %s", runs[0].Payload)
	}
}

func TestHashDatasetStableAndSensitive(t *testing.T) {
	a := HashDataset([]byte("gold"), []byte("pw=10;pr=5"))
	b := HashDataset([]byte("gold"), []byte("pw=10;pr=5"))
	c := HashDataset([]byte("gold"), []byte("pw=11;pr=5"))
	if a != b {
		t.Fatalf("hash not stable: %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("hash insensitive to content change")
	}
	// domain separator: ["ab","c"] must not collide with ["a","bc"]
	if HashDataset([]byte("ab"), []byte("c")) == HashDataset([]byte("a"), []byte("bc")) {
		t.Fatalf("missing domain separation between parts")
	}
}
