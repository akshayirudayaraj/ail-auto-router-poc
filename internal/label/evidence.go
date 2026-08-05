package label

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EvidencePackVersion is stamped into judge LabelRecords so a change in what the
// judge sees is reproducible/attributable.
const EvidencePackVersion = "evpack-v1"

// EvidencePack is the DETERMINISTIC, LLM-free distillation of a session that the
// LLM judge scores for oracle-less tasks. Built from the task issue, the produced
// diff, the CC event stream, and the run metrics. No model call — a pure function
// so it is cheap, cacheable, reproducible, and adds no model error of its own.
// (OFFLINE_ENGINE_PLAN §4.2.)
type EvidencePack struct {
	TaskID           string            `json:"task_id"`
	SessionID        string            `json:"session_id"`
	Issue            string            `json:"issue"`
	ChangedFiles     []ChangedFile     `json:"changed_files"`
	VerificationRuns []VerificationRun `json:"verification_runs"`
	FinalAgentText   string            `json:"final_agent_text"` // agent_claim, UNTRUSTED
	Flags            PackFlags         `json:"flags"`
}

// ChangedFile is one file the diff touches, with its (truncated) hunk body.
type ChangedFile struct {
	Path string `json:"path"`
	Body string `json:"body"`
}

// VerificationRun is a test/build/repro command the agent itself ran, tagged
// `observed` (came from a real tool_result, not the agent's prose).
type VerificationRun struct {
	Command    string `json:"command"`
	Errored    bool   `json:"errored"`
	OutputTail string `json:"output_tail"`
}

// PackFlags are the degeneracy signals mined at generation time (from the run
// record). They catch "never ran anything" / "looped on errors".
type PackFlags struct {
	NumTurns       int  `json:"num_turns"`
	ToolCalls      int  `json:"tool_calls"`
	ToolErrors     int  `json:"tool_errors"`
	NativeCalls    int  `json:"native_tool_calls"`
	RescuedCalls   int  `json:"rescued_tool_calls"`
	EmptyPatch     bool `json:"empty_patch"`
	TimedOut       bool `json:"timed_out"`
	HitTurnCap     bool `json:"hit_turn_cap"`
	EditedTestFile bool `json:"edited_test_file"` // red flag: prompt forbids editing tests
}

// budgets — keep the pack compact so it fits the judge context and controls bias.
const (
	maxHunkBody   = 2000
	maxOutputTail = 240
	maxFinalText  = 800
)

// verificationPrefixes classify a Bash command as a verification run. Deterministic
// allowlist; the last-few-commands fallback (§4.2) is a future addition.
var verificationSubstrings = []string{
	"pytest", "python -m pytest", "go test", "npm test", "npm run test",
	"tox", "make test", "make check", "python -c", "unittest",
}

// BuildEvidencePack assembles the pack from raw artifacts. issue and diff are the
// strings; eventsPath is the .events.jsonl file; flags come from the run record.
func BuildEvidencePack(taskID, sessionID, issue, diff, eventsPath string, flags PackFlags) (EvidencePack, error) {
	p := EvidencePack{
		TaskID:       taskID,
		SessionID:    sessionID,
		Issue:        strings.TrimSpace(issue),
		ChangedFiles: parseChangedFiles(diff),
		Flags:        flags,
	}
	for _, cf := range p.ChangedFiles {
		if isTestPath(cf.Path) {
			p.Flags.EditedTestFile = true
		}
	}
	runs, finalText, err := mineEvents(eventsPath)
	if err != nil {
		return p, err
	}
	p.VerificationRuns = runs
	p.FinalAgentText = truncate(finalText, maxFinalText)
	return p, nil
}

// ---- diff parsing ----------------------------------------------------------

func parseChangedFiles(diff string) []ChangedFile {
	var out []ChangedFile
	var cur *ChangedFile
	var body strings.Builder
	flush := func() {
		if cur != nil {
			cur.Body = truncate(strings.TrimRight(body.String(), "\n"), maxHunkBody)
			out = append(out, *cur)
		}
		body.Reset()
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = &ChangedFile{Path: pathFromDiffGit(line)}
		case strings.HasPrefix(line, "+++ b/"):
			if cur != nil {
				cur.Path = strings.TrimPrefix(line, "+++ b/")
			}
		case strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"), strings.HasPrefix(line, " "):
			if cur != nil {
				body.WriteString(line)
				body.WriteString("\n")
			}
		}
	}
	flush()
	return out
}

func pathFromDiffGit(line string) string {
	// "diff --git a/foo b/foo"
	fs := strings.Fields(line)
	if len(fs) >= 4 {
		return strings.TrimPrefix(fs[3], "b/")
	}
	return ""
}

func isTestPath(p string) bool {
	base := filepath.Base(p)
	return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") ||
		strings.Contains(p, "/tests/") || strings.HasPrefix(p, "tests/") || base == "conftest.py"
}

// ---- event-stream mining ---------------------------------------------------

type ccEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []ccBlock `json:"content"`
	} `json:"message"`
}

type ccBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// mineEvents walks the CC stream: it collects Bash tool_use commands classified as
// verification, pairs each with its tool_result (by tool_use_id), and returns them
// plus the last assistant text block (the agent's final self-report).
func mineEvents(eventsPath string) ([]VerificationRun, string, error) {
	f, err := os.Open(eventsPath)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	type pending struct {
		cmd string
		idx int
	}
	var order []string                 // tool_use_ids of verification runs, in issue order
	byID := map[string]pending{}       // tool_use_id -> command
	results := map[string]ccBlock{}    // tool_use_id -> tool_result block
	finalText := ""

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e ccEvent
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "assistant":
			for _, b := range e.Message.Content {
				switch b.Type {
				case "tool_use":
					if b.Name == "Bash" {
						cmd := bashCommand(b.Input)
						if isVerification(cmd) {
							order = append(order, b.ID)
							byID[b.ID] = pending{cmd: cmd, idx: len(order)}
						}
					}
				case "text":
					if t := strings.TrimSpace(b.Text); t != "" {
						finalText = t // keep last non-empty
					}
				}
			}
		case "user":
			for _, b := range e.Message.Content {
				if b.Type == "tool_result" {
					results[b.ToolUseID] = b
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, "", err
	}

	var runs []VerificationRun
	for _, id := range order {
		p := byID[id]
		r := VerificationRun{Command: p.cmd}
		if res, ok := results[id]; ok {
			r.Errored = res.IsError
			r.OutputTail = truncate(oneLine(toolResultText(res.Content)), maxOutputTail)
		} else {
			r.Errored = true
			r.OutputTail = "(no result captured)"
		}
		runs = append(runs, r)
	}
	return runs, finalText, nil
}

func bashCommand(raw json.RawMessage) string {
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(raw, &in)
	return in.Command
}

func isVerification(cmd string) bool {
	lc := strings.ToLower(cmd)
	for _, s := range verificationSubstrings {
		if strings.Contains(lc, s) {
			return true
		}
	}
	return false
}

// toolResultText handles a tool_result content that is either a JSON string or a
// list of {type:"text", text:"..."} blocks.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			parts = append(parts, b.Text)
		}
		return strings.Join(parts, " ")
	}
	return string(raw)
}

// ---- rendering -------------------------------------------------------------

// Render produces the compact, sectioned text handed to the judge. Every fact is
// labeled observed vs agent-claim so the judge (and calibration) can separate
// corroborated from asserted success.
func (p EvidencePack) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ISSUE:\n%s\n\n", p.Issue)

	b.WriteString("CHANGED FILES (from diff):\n")
	if len(p.ChangedFiles) == 0 {
		b.WriteString("  (none — empty patch)\n")
	}
	for _, cf := range p.ChangedFiles {
		tag := ""
		if isTestPath(cf.Path) {
			tag = "   [TEST FILE — prompt forbids editing tests]"
		}
		fmt.Fprintf(&b, "  %s%s\n%s\n", cf.Path, tag, indent(cf.Body, "    "))
	}
	b.WriteString("\n")

	b.WriteString("VERIFICATION RUNS (observed):\n")
	if len(p.VerificationRuns) == 0 {
		b.WriteString("  (none — the agent ran no tests/build)\n")
	}
	for _, r := range p.VerificationRuns {
		status := "ok"
		if r.Errored {
			status = "ERROR"
		}
		fmt.Fprintf(&b, "  $ %s\n  -> [%s] %s\n", r.Command, status, r.OutputTail)
	}
	b.WriteString("\n")

	if p.FinalAgentText != "" {
		fmt.Fprintf(&b, "AGENT CLAIM (self-report, UNTRUSTED):\n  %s\n\n", oneLine(p.FinalAgentText))
	}

	fmt.Fprintf(&b, "RUN FLAGS: turns=%d tool_calls=%d tool_errors=%d native/rescued=%d/%d empty_patch=%v timed_out=%v hit_turn_cap=%v edited_test_file=%v\n",
		p.Flags.NumTurns, p.Flags.ToolCalls, p.Flags.ToolErrors, p.Flags.NativeCalls, p.Flags.RescuedCalls,
		p.Flags.EmptyPatch, p.Flags.TimedOut, p.Flags.HitTurnCap, p.Flags.EditedTestFile)
	return b.String()
}

// ---- helpers ---------------------------------------------------------------

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func indent(s, pad string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}
