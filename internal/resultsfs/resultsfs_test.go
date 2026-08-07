package resultsfs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRecordsAndFind covers both layouts: Records skips labels/calibration and
// side files; Find resolves a basename whether it's flat or in a type subdir.
func TestRecordsAndFind(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("flat__local__h.json")                 // flat record
	write("semi-synthetic/t__frontier__h.json")  // nested record
	write("semi-synthetic/t__frontier__h.patch") // side file (ignored by Records)
	write("synthetic/s__local__h.session.jsonl") // side file
	write("labels/resolved.jsonl")               // excluded subtree
	write("calibration/report.json")             // excluded subtree
	write("split_manifest.json")                 // excluded manifest
	write("pred_x.jsonl")                        // excluded

	recs := Records(dir)
	if len(recs) != 2 {
		t.Fatalf("Records = %d, want 2 (%v)", len(recs), recs)
	}
	if got := Find(dir, "t__frontier__h.patch"); filepath.Base(got) != "t__frontier__h.patch" {
		t.Errorf("Find nested side file failed: %q", got)
	}
	if got := Find(dir, "flat__local__h.json"); filepath.Base(got) != "flat__local__h.json" {
		t.Errorf("Find flat record failed: %q", got)
	}
	if got := Find(dir, "does-not-exist.json"); got != "" {
		t.Errorf("Find missing = %q, want empty", got)
	}
}
