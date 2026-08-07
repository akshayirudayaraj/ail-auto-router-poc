// Package resultsfs makes the runner results directory layout-agnostic. Session
// artifacts may live flat under results/ or partitioned into type subdirectories
// (synthetic/, semi-synthetic/, logs/). Readers use Records() and Find() so they
// work regardless of layout, and run records can keep storing artifact paths as
// bare basenames (Find resolves them wherever they landed).
package resultsfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// nonRecordDirs are subdirectories under results/ that never hold run records.
var nonRecordDirs = map[string]bool{"labels": true, "calibration": true}

// Records returns every run-record *.json path under dir (recursively), skipping
// the labels/ and calibration/ subtrees, the split/selection manifests, and the
// pred_*.jsonl / *.session.jsonl / *.events.jsonl side files.
func Records(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && nonRecordDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		if !strings.HasSuffix(base, ".json") {
			return nil
		}
		if strings.HasSuffix(base, ".session.jsonl") || strings.HasSuffix(base, ".events.jsonl") {
			return nil
		}
		switch base {
		case "split_manifest.json", "swe_selection.json", "gold_meta.json":
			return nil
		}
		if strings.HasPrefix(base, "pred_") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	return out
}

// Find resolves an artifact basename (e.g. "task__arm__hash.patch") to its full
// path anywhere under dir — flat or in a type subdirectory. Returns the direct
// join if it exists (fast path), else walks. Empty string if not found.
func Find(dir, name string) string {
	name = filepath.Base(name)
	if direct := filepath.Join(dir, name); fileExists(direct) {
		return direct
	}
	var hit string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == name {
			hit = p
			return filepath.SkipAll
		}
		return nil
	})
	return hit
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
