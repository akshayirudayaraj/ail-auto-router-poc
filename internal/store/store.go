// Package store is the persistence seam for evaluation results. The whole point
// is that the concrete backend is swappable behind one small interface: today a
// FileStore appends JSONL under the data dir; later a PostgresStore can drop in
// with zero changes to callers. That is what lets "post an eval via the API"
// migrate from files to a real DB without rewriting the server.
//
// An EvalRun is modelled DB-first: scalar, index-friendly columns (id,
// created_at, git_sha, dataset_hash, …) plus a Payload JSON blob for the full
// leaderboard/anchors/notes (a jsonb column in Postgres). This package has NO
// dependency on internal/eval — the caller marshals the report into Payload — so
// the storage layer stays a leaf with no risk of an import cycle.
package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// EvalRun is one persisted evaluation of the router set against a dataset
// snapshot. The scalar fields map to real DB columns; Payload holds the full
// report JSON. DatasetHash records exactly which data the run scored, so a
// stored run is reproducible rather than a free-floating pile of numbers.
type EvalRun struct {
	ID          string          `json:"id"`
	CreatedAt   string          `json:"created_at"` // RFC3339 UTC
	GitSHA      string          `json:"git_sha,omitempty"`
	DatasetHash string          `json:"dataset_hash"`
	Method      string          `json:"method"`
	TrainSource string          `json:"train_source"`
	Threshold   float64         `json:"threshold"`
	NGold       int             `json:"n_gold"`
	Payload     json.RawMessage `json:"payload"` // leaderboard + anchors + notes
}

// Store persists and lists evaluation runs. Kept intentionally small: the
// backend (file today, Postgres later) is swappable behind this interface.
type Store interface {
	SaveEvalRun(run EvalRun) error
	ListEvalRuns() ([]EvalRun, error)
}

// HashDataset returns a short content version for a dataset snapshot, so an
// eval run records exactly which data it was computed on. Order-sensitive; the
// caller passes stable byte slices (e.g. marshalled gold rows + train sizes).
func HashDataset(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
		h.Write([]byte{0}) // domain separator so concatenations can't collide
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// FileStore is the file-backed Store: eval runs are one JSON object per line in
// <dir>/eval_runs.jsonl. Append-only writes and a whole-file read keep it simple
// and crash-safe enough for a POC; the shape (one row per run) is exactly what a
// Postgres table would hold.
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore returns a FileStore writing under dir (typically the data dir).
func NewFileStore(dir string) *FileStore { return &FileStore{dir: dir} }

func (f *FileStore) path() string { return filepath.Join(f.dir, "eval_runs.jsonl") }

// SaveEvalRun appends one run. Concurrent callers are serialized by the mutex.
func (f *FileStore) SaveEvalRun(run EvalRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		return err
	}
	fh, err := os.OpenFile(f.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer fh.Close()
	b, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = fh.Write(append(b, '\n'))
	return err
}

// ListEvalRuns returns all runs, newest first. A missing file is an empty
// history, not an error.
func (f *FileStore) ListEvalRuns() ([]EvalRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fh, err := os.Open(f.path())
	if err != nil {
		if os.IsNotExist(err) {
			return []EvalRun{}, nil
		}
		return nil, err
	}
	defer fh.Close()
	var out []EvalRun
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var r EvalRun
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// compile-time check that FileStore satisfies Store.
var _ Store = (*FileStore)(nil)
