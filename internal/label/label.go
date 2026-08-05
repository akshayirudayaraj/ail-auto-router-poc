// Package label is the offline scoring engine: it turns the pre-outcome session
// artifacts that log-first generation produces (see agentic/SAMPLE_CORPUS.md) into
// outcomes. Generation assigns NO outcome; this package is where outcomes come
// from — the executed oracle, an LLM judge (over a distilled evidence pack), or
// signal heuristics. See OFFLINE_ENGINE_PLAN.md.
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

// LoadLabels reads all labels/*.jsonl records under dir (any source present).
func LoadLabels(dir string) ([]LabelRecord, error) {
	paths, _ := filepath.Glob(filepath.Join(dir, "labels", "*.jsonl"))
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
