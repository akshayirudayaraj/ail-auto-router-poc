package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Agentic corpus browser (DATA_PLAN Phase 5): read-only views over the log-first
// agentic run records + session/event logs + split manifest. No scoring — this
// shows WHAT HAPPENED (turns, tool calls, diff, metrics), never a pass/fail
// verdict; the offline engine's labels surface here once it exists. Stays
// stdlib-only (invariant 1).

// agenticDir is where the Python runner writes results/ + tasks/. Override with
// AIL_AGENTIC_DIR; defaults to ./agentic (make serve runs from the repo root).
func agenticDir() string {
	if d := os.Getenv("AIL_AGENTIC_DIR"); d != "" {
		return d
	}
	return "agentic"
}

func agenticResultsDir() string { return filepath.Join(agenticDir(), "results") }

// isRunRecord filters results/*.json down to run records (drops the log JSONLs,
// the split manifest, the SWE selection manifest, and prediction files).
func isRunRecord(name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	if strings.HasSuffix(name, ".session.jsonl") || strings.HasSuffix(name, ".events.jsonl") {
		return false
	}
	switch name {
	case "split_manifest.json", "swe_selection.json":
		return false
	}
	if strings.HasPrefix(name, "pred_") || strings.HasPrefix(name, "frontier.") {
		return false
	}
	return true
}

func loadSplitMap() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(filepath.Join(agenticResultsDir(), "split_manifest.json"))
	if err != nil {
		return out
	}
	var m struct {
		Items []struct {
			SessionID string `json:"session_id"`
			Split     string `json:"split"`
		} `json:"items"`
	}
	if json.Unmarshal(b, &m) == nil {
		for _, it := range m.Items {
			out[it.SessionID] = it.Split
		}
	}
	return out
}

// ---- /api/agentic : one row per (task, arm) session ----

func (s *Server) handleAgentic(w http.ResponseWriter, r *http.Request) {
	paths, _ := filepath.Glob(filepath.Join(agenticResultsDir(), "*.json"))
	splitMap := loadSplitMap()
	resolved := resolvedBySession() // fused canonical labels, joined per session
	sort.Strings(paths)

	rows := []map[string]any{}
	for _, p := range paths {
		if !isRunRecord(filepath.Base(p)) {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(b, &rec) != nil {
			continue
		}
		if _, ok := rec["task_id"]; !ok {
			continue
		}
		sid, _ := rec["session_id"].(string)
		split := ""
		if v, ok := rec["split"].(string); ok {
			split = v
		}
		if split == "" {
			split = splitMap[sid]
		}
		prov, _ := rec["provenance"].(string)
		row := map[string]any{
			"task_id":               rec["task_id"],
			"arm":                   rec["arm"],
			"served_model":          rec["served_model"],
			"source":                prov,
			"grounding":             rec["grounding"],
			"tier":                  rec["tier"],
			"split":                 split,
			"has_executable_oracle": rec["has_executable_oracle"],
			"num_turns":             num(rec["num_turns"]),
			"tool_calls":            num(rec["tool_calls_attempted"]),
			"native_tool_calls":     num(rec["native_tool_calls"]),
			"rescued_tool_calls":    num(rec["rescued_tool_calls"]),
			"tool_errors":           num(rec["tool_calls_errored"]),
			"total_tokens":          num(rec["total_tokens"]),
			"wall_s":                num(rec["wall_clock_s"]),
			"timed_out":             rec["timed_out"],
			"hit_turn_cap":          rec["hit_turn_cap"],
			"empty_patch":           rec["empty_patch"],
			"session_id":            sid,
		}
		// Join the fused canonical outcome (from the offline label engine) so the
		// corpus table shows outcome/source/confidence and flags disagreements.
		if rv, ok := resolved[sid]; ok {
			row["outcome"] = num(rv["outcome"])
			row["label_src"] = rv["label_source"]
			row["conf"] = num(rv["label_confidence"])
			if ev, ok := rv["evidence"].(map[string]any); ok {
				row["disagreement"] = ev["disagreement_flag"]
			}
		}
		rows = append(rows, row)
	}
	writeJSON(w, 200, map[string]any{"rows": rows, "count": len(rows)})
}

// ---- /api/agentic/session?id=<session_id>[&reveal=1] : full trace ----

func (s *Server) handleAgenticSession(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("id")
	if sid == "" {
		writeJSON(w, 400, map[string]any{"error": "missing id"})
		return
	}
	reveal := r.URL.Query().Get("reveal") == "1"

	// Find the run record whose session_id matches.
	paths, _ := filepath.Glob(filepath.Join(agenticResultsDir(), "*.json"))
	var rec map[string]any
	var base string
	for _, p := range paths {
		if !isRunRecord(filepath.Base(p)) {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if s2, _ := m["session_id"].(string); s2 == sid {
			rec = m
			base = strings.TrimSuffix(p, ".json")
			break
		}
	}
	if rec == nil {
		writeJSON(w, 404, map[string]any{"error": "session not found"})
		return
	}

	turns := readJSONL(base + ".session.jsonl")
	events := readJSONL(base + ".events.jsonl")
	patch, _ := os.ReadFile(base + ".patch")

	// Task issue (always) + oracle (only behind an explicit reveal, so the UI
	// itself can never cause a firewall slip in a screenshot/demo).
	issue, oracle := "", map[string]string{}
	if tid, ok := rec["task_id"].(string); ok {
		tdir := filepath.Join(agenticDir(), "tasks", tid)
		if b, err := os.ReadFile(filepath.Join(tdir, "task.json")); err == nil {
			var t map[string]any
			if json.Unmarshal(b, &t) == nil {
				issue, _ = t["issue"].(string)
			}
		}
		if reveal {
			for _, f := range []string{"test_patch.diff", "gold_patch.diff"} {
				if b, err := os.ReadFile(filepath.Join(tdir, "_oracle", f)); err == nil {
					oracle[f] = string(b)
				}
			}
		}
	}

	writeJSON(w, 200, map[string]any{
		"record": rec,
		"turns":  turns,
		"events": events,
		"patch":  string(patch),
		"issue":  issue,
		"oracle": oracle, // empty unless reveal=1
	})
}

// ---- small helpers ----

func readJSONL(path string) []any {
	b, err := os.ReadFile(path)
	if err != nil {
		return []any{}
	}
	out := []any{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v any
		if json.Unmarshal([]byte(line), &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

// num coerces a JSON number (float64) or nil to a plain value for the row table.
func num(v any) any {
	if v == nil {
		return 0
	}
	return v
}
