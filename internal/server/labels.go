package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// labels.go exposes the offline scoring engine's outputs (internal/label +
// agentic/runner/grade_offline.py) to the console: per-session label records from
// each branch (executed / judge / implicit), the fused canonical (resolved) label,
// and the calibration report. Read-only; missing files degrade to empty.

func labelsDir() string { return filepath.Join(agenticResultsDir(), "labels") }

// loadLabelFile parses one labels/<name>.jsonl into records (nil if absent).
func loadLabelFile(name string) []map[string]any {
	b, err := os.ReadFile(filepath.Join(labelsDir(), name))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// resolvedBySession maps session_id -> the fused canonical label record.
func resolvedBySession() map[string]map[string]any {
	m := map[string]map[string]any{}
	for _, r := range loadLabelFile("resolved.jsonl") {
		if sid, _ := r["session_id"].(string); sid != "" {
			m[sid] = r
		}
	}
	return m
}

// calibrationReport reads calibration/report.json (nil if not yet computed).
func calibrationReport() any {
	b, err := os.ReadFile(filepath.Join(agenticResultsDir(), "calibration", "report.json"))
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return v
}

// handleLabels: /api/labels -> per-session label breakdown + calibration report.
func (s *Server) handleLabels(w http.ResponseWriter, r *http.Request) {
	bySession := map[string]map[string]any{}
	add := func(src string, recs []map[string]any) {
		for _, rec := range recs {
			sid, _ := rec["session_id"].(string)
			if sid == "" {
				continue
			}
			e := bySession[sid]
			if e == nil {
				e = map[string]any{}
				bySession[sid] = e
			}
			e[src] = map[string]any{
				"outcome":    rec["outcome"],
				"confidence": rec["label_confidence"],
				"source":     rec["label_source"],
				"evidence":   rec["evidence"],
			}
		}
	}
	add("executed", loadLabelFile("executed.jsonl"))
	add("judge", loadLabelFile("judge.jsonl"))
	add("implicit", loadLabelFile("implicit.jsonl"))
	add("resolved", loadLabelFile("resolved.jsonl"))

	writeJSON(w, 200, map[string]any{
		"by_session":  bySession,
		"calibration": calibrationReport(),
		"n_sessions":  len(bySession),
	})
}
