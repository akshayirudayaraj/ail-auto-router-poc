package label

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/akshayirudayaraj/ail-routing-test/internal/resultsfs"
)

// runRecord is the subset of a generation run record (…​.json) the engine reads.
// Generation writes NO outcome here (that's this package's job).
type runRecord struct {
	TaskID        string `json:"task_id"`
	Arm           string `json:"arm"`
	ServedModel   string `json:"served_model"`
	Provenance    string `json:"provenance"`
	SessionID     string `json:"session_id"`
	PatchPath     string `json:"patch_path"`
	EventsLogPath string `json:"events_log_path"`
	HasOracle     bool   `json:"has_executable_oracle"`

	AssistantTurns   int  `json:"assistant_turns"`
	ToolCalls        int  `json:"tool_calls_attempted"`
	ToolErrors       int  `json:"tool_calls_errored"`
	NativeToolCalls  int  `json:"native_tool_calls"`
	RescuedToolCalls int  `json:"rescued_tool_calls"`
	EmptyPatch       bool `json:"empty_patch"`
	TimedOut         bool `json:"timed_out"`
	HitTurnCap       bool `json:"hit_turn_cap"`
}

// taskMeta is the subset of a task.json the engine reads.
type taskMeta struct {
	Issue string `json:"issue"`
}

// BuildFromResults assembles an EvidencePack for one session from the on-disk
// artifacts: the run record (resultsDir/<sessionKey>.json), the task issue
// (tasksDir/<task_id>/task.json), the diff (.patch), and the event stream
// (.events.jsonl). This is the convenience entry the judge branch calls per log.
func BuildFromResults(resultsDir, tasksDir, sessionKey string) (EvidencePack, error) {
	// Artifacts may be flat or in a type subdir; resolve basenames wherever they landed.
	rr, err := loadRunRecord(resultsfs.Find(resultsDir, sessionKey+".json"))
	if err != nil {
		return EvidencePack{}, err
	}
	issue, err := loadIssue(filepath.Join(tasksDir, rr.TaskID, "task.json"))
	if err != nil {
		return EvidencePack{}, err
	}
	diff, err := os.ReadFile(resultsfs.Find(resultsDir, rr.PatchPath))
	if err != nil {
		return EvidencePack{}, fmt.Errorf("read patch: %w", err)
	}
	flags := PackFlags{
		NumTurns:     rr.AssistantTurns,
		ToolCalls:    rr.ToolCalls,
		ToolErrors:   rr.ToolErrors,
		NativeCalls:  rr.NativeToolCalls,
		RescuedCalls: rr.RescuedToolCalls,
		EmptyPatch:   rr.EmptyPatch,
		TimedOut:     rr.TimedOut,
		HitTurnCap:   rr.HitTurnCap,
	}
	return BuildEvidencePack(rr.TaskID, rr.SessionID, issue, string(diff),
		resultsfs.Find(resultsDir, rr.EventsLogPath), flags)
}

func loadRunRecord(path string) (runRecord, error) {
	var rr runRecord
	b, err := os.ReadFile(path)
	if err != nil {
		return rr, fmt.Errorf("read run record: %w", err)
	}
	if err := json.Unmarshal(b, &rr); err != nil {
		return rr, fmt.Errorf("parse run record: %w", err)
	}
	return rr, nil
}

func loadIssue(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read task.json: %w", err)
	}
	var t taskMeta
	if err := json.Unmarshal(b, &t); err != nil {
		return "", fmt.Errorf("parse task.json: %w", err)
	}
	return t.Issue, nil
}
