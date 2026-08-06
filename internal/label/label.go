// Package label is the offline scoring engine: it turns the pre-outcome session
// artifacts that log-first generation produces (see agentic/SAMPLE_CORPUS.md) into
// outcomes. Generation assigns NO outcome; this package is where outcomes come
// from — the executed oracle, an LLM judge (over a distilled evidence pack), or
// signal heuristics. See docs/OFFLINE_ENGINE_PLAN.md.
//
// Design contracts honored here:
//   - Logs are immutable; labels are an append-only layer (one LabelRecord per
//     labeler per session; all retained for calibration).
//   - Label strength ordering executed > human > judge > implicit
//     (schema.LabelStrength) drives Resolve; eval/gold must be >= train strength.
//   - stdlib-only (this is portable-core Go).
package label

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// LabelRecord is one labeler's outcome verdict for a (task, model) session.
// The three branches (executed / judge / implicit) each emit these into their own
// labels/<source>.jsonl; multiple records per session coexist.
type LabelRecord struct {
	SessionID           string             `json:"session_id"`
	TaskID              string             `json:"task_id"`
	Model               string             `json:"model"`
	Arm                 string             `json:"arm"`
	Split               string             `json:"split,omitempty"`
	Provenance          string             `json:"provenance,omitempty"`
	HasExecutableOracle bool               `json:"has_executable_oracle"`
	Outcome             int                `json:"outcome"` // 1 = adequate, 0 = inadequate
	LabelSource         schema.LabelSource `json:"label_source"`
	LabelConfidence     float64            `json:"label_confidence"` // [0,1], calibrated later
	LabelerVersion      string             `json:"labeler_version"`
	Evidence            map[string]any     `json:"evidence,omitempty"`
	Timestamp           int64              `json:"timestamp"`
}

// key identifies the session a label is about, for Resolve.
func (r LabelRecord) key() string { return r.TaskID + "|" + r.Model }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// AppendLabels appends records to <dir>/labels/<source>.jsonl (created if needed).
// Append-only by design: re-labeling never rewrites history.
func AppendLabels(dir string, src schema.LabelSource, recs []LabelRecord) error {
	ldir := filepath.Join(dir, "labels")
	if err := os.MkdirAll(ldir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(ldir, string(src)+".jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

// sourceFiles are the append-only per-branch label files LoadLabels reads. The
// fusion OUTPUT (resolved.jsonl) lives alongside them but is deliberately excluded
// so it is never re-ingested as a source.
var sourceFiles = []string{"executed.jsonl", "judge.jsonl", "implicit.jsonl"}

// WriteLabels (over)writes <dir>/labels/<source>.jsonl with exactly recs. Use for
// deterministic sources (heuristics) that are recomputed from the logs each run, so
// re-runs don't accumulate duplicates. (Append is for the judge, whose calls are
// expensive/cached.)
func WriteLabels(dir string, src schema.LabelSource, recs []LabelRecord) error {
	ldir := filepath.Join(dir, "labels")
	if err := os.MkdirAll(ldir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(ldir, string(src)+".jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

// LoadLabels reads the append-only source label files under <dir>/labels
// (executed/judge/implicit), skipping the resolved output.
func LoadLabels(dir string) ([]LabelRecord, error) {
	var paths []string
	for _, name := range sourceFiles {
		if p := filepath.Join(dir, "labels", name); fileExists(p) {
			paths = append(paths, p)
		}
	}
	var out []LabelRecord
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var r LabelRecord
			if json.Unmarshal(line, &r) == nil && r.SessionID != "" {
				out = append(out, r)
			}
		}
		f.Close()
	}
	return out, nil
}

// LoadResolved reads the fusion OUTPUT (<dir>/labels/resolved.jsonl) — the
// canonical one-per-(task,model) labels the offline engine produces. This is the
// materializer's input; it is deliberately NOT part of LoadLabels' source set.
func LoadResolved(dir string) ([]LabelRecord, error) {
	p := filepath.Join(dir, "labels", "resolved.jsonl")
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []LabelRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var r LabelRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.SessionID != "" {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

// Resolve picks the single strongest label per (task, model) as the canonical
// outcome, using schema.LabelStrength (executed > human > judge > implicit). Ties
// on strength keep the higher-confidence record. All input records are retained by
// the caller; this only selects the canonical one for materialization.
func Resolve(recs []LabelRecord) map[string]LabelRecord {
	best := map[string]LabelRecord{}
	for _, r := range recs {
		cur, ok := best[r.key()]
		if !ok {
			best[r.key()] = r
			continue
		}
		rs, cs := schema.LabelStrength(r.LabelSource), schema.LabelStrength(cur.LabelSource)
		if rs > cs || (rs == cs && r.LabelConfidence > cur.LabelConfidence) {
			best[r.key()] = r
		}
	}
	return best
}

// AssertEvalStrongerThanTrain enforces the no-circularity invariant: every
// held-out (eval/gold) canonical label must come from a source at least as strong
// as every train canonical label. Returns an error naming the first violation.
func AssertEvalStrongerThanTrain(resolved map[string]LabelRecord) error {
	minEval := 1 << 30
	maxTrain := -1
	for _, r := range resolved {
		s := schema.LabelStrength(r.LabelSource)
		switch r.Split {
		case "holdout":
			if s < minEval {
				minEval = s
			}
		case "train":
			if s > maxTrain {
				maxTrain = s
			}
		}
	}
	if minEval != 1<<30 && maxTrain != -1 && minEval < maxTrain {
		return fmt.Errorf("label circularity: weakest holdout source (%d) < strongest train source (%d)", minEval, maxTrain)
	}
	return nil
}
