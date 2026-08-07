package label

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/akshayirudayaraj/ail-routing-test/internal/resultsfs"
)

// SessionRef identifies one generated session on disk (one run record).
type SessionRef struct {
	Key         string // result filename without .json; == session_id
	TaskID      string
	Arm         string
	ServedModel string
	Provenance  string
	HasOracle   bool
}

// Ident builds the identity portion of a LabelRecord for this session; the labeler
// (judge/executed/implicit) fills outcome/source/confidence/evidence.
func (s SessionRef) Ident(split string, ts int64) LabelRecord {
	return LabelRecord{
		SessionID:           s.Key,
		TaskID:              s.TaskID,
		Model:               s.ServedModel,
		Arm:                 s.Arm,
		Split:               split,
		Provenance:          s.Provenance,
		HasExecutableOracle: s.HasOracle,
		Timestamp:           ts,
	}
}

// ListSessions returns every session (run record) in resultsDir, sorted by key.
func ListSessions(resultsDir string) ([]SessionRef, error) {
	paths := resultsfs.Records(resultsDir) // layout-agnostic (flat or type subdirs)
	var out []SessionRef
	for _, p := range paths {
		base := filepath.Base(p)
		rr, err := loadRunRecord(p)
		if err != nil || rr.TaskID == "" {
			continue // not a run record
		}
		out = append(out, SessionRef{
			Key:         strings.TrimSuffix(base, ".json"),
			TaskID:      rr.TaskID,
			Arm:         rr.Arm,
			ServedModel: rr.ServedModel,
			Provenance:  rr.Provenance,
			HasOracle:   rr.HasOracle,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// LoadSplitByTask reads split_manifest.json's task_split map (task_id -> split).
// Missing/absent manifest returns an empty map (split "" = unknown) rather than an
// error, so labeling can run before a split exists.
func LoadSplitByTask(resultsDir string) map[string]string {
	b, err := os.ReadFile(filepath.Join(resultsDir, "split_manifest.json"))
	if err != nil {
		return map[string]string{}
	}
	var m struct {
		TaskSplit map[string]string `json:"task_split"`
	}
	if json.Unmarshal(b, &m) != nil || m.TaskSplit == nil {
		return map[string]string{}
	}
	return m.TaskSplit
}
