package backend

import (
	"path/filepath"
	"testing"
)

func TestCacheKeyDeterministicAndSeparated(t *testing.T) {
	k1 := cacheKey("embed", "m", "hello")
	k2 := cacheKey("embed", "m", "hello")
	if k1 != k2 {
		t.Fatal("cacheKey not deterministic")
	}
	// domain separation: ["a","b"] must differ from ["ab",""]
	if cacheKey("a", "b") == cacheKey("ab", "") {
		t.Fatal("cacheKey lacks domain separation")
	}
	if cacheKey("embed", "m", "hello") == cacheKey("gen", "m", "hello") {
		t.Fatal("capability tag should change the key")
	}
}

func TestDiskCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := newDiskCache(dir)
	key := cacheKey("embed", "m", "x")
	if _, ok := c.get("embed", key); ok {
		t.Fatal("unexpected hit on empty cache")
	}
	want := []float32{1, 2, 3}
	c.putJSON("embed", key, want)
	var got []float32
	if !c.getJSON("embed", key, &got) {
		t.Fatal("expected hit after put")
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("round-trip mismatch: %v", got)
	}
	// value persisted to disk
	if _, err := filepath.Glob(filepath.Join(dir, "embed", "*.json")); err != nil {
		t.Fatal(err)
	}
	// fresh cache reading same dir should hit
	c2 := newDiskCache(dir)
	var got2 []float32
	if !c2.getJSON("embed", key, &got2) {
		t.Fatal("expected disk hit from fresh cache")
	}
}

func TestParseJudge(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantOK  bool
		wantAdq bool
	}{
		{"plain", `{"adequate": true, "score": 0.9, "rationale": "good"}`, true, true},
		{"fenced", "```json\n{\"adequate\": false, \"score\": 0.1, \"rationale\": \"bad\"}\n```", true, false},
		{"prose_wrapped", `Sure! Here: {"adequate": true, "score": 1.2, "rationale":"x"} hope that helps`, true, true},
		{"no_json", "I think it's fine.", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jr, err := parseJudge(tc.raw)
			if tc.wantOK && err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("expected err")
			}
			if tc.wantOK {
				if jr.Adequate != tc.wantAdq {
					t.Fatalf("adequate=%v want %v", jr.Adequate, tc.wantAdq)
				}
				if jr.Score < 0 || jr.Score > 1 {
					t.Fatalf("score not clamped: %v", jr.Score)
				}
			}
		})
	}
}

func TestCLIModelAlias(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-5": "sonnet",
		"claude-opus-4-8": "opus",
		"claude-haiku-4-5": "haiku",
		"llama3.1:8b":     "",
	}
	for in, want := range cases {
		if got := cliModelAlias(in); got != want {
			t.Errorf("cliModelAlias(%q)=%q want %q", in, got, want)
		}
	}
}

func TestExtractJSONObjectBalanced(t *testing.T) {
	in := `noise {"a": {"b": 1}} trailing {"c":2}`
	got := extractJSONObject(in)
	if got != `{"a": {"b": 1}}` {
		t.Fatalf("got %q", got)
	}
}
