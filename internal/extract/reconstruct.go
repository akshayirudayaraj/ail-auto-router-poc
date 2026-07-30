// Package extract is the offline label engine (Pillar 1b) plus its quality
// report (Pillar 1c). It turns raw session logs into the structured pointwise
// and pairwise datasets every router consumes.
//
// Hard rule: extraction runs ONLY on logs passed through
// schema.RawTurn.StripHidden(). It never reads a "_"-prefixed ground-truth
// field. The quality report (report.go) is the only code that reads truth, and
// it does so from a separate load of the original logs.
package extract

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/akshayirudayaraj/ail-routing-test/internal/schema"
)

// LoadRaw reads a JSONL log file into RawTurns. If strip is true every hidden
// ground-truth field is cleared immediately on load — this is how extraction
// guarantees it never sees truth.
func LoadRaw(path string, strip bool) ([]schema.RawTurn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []schema.RawTurn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var t schema.RawTurn
		if err := json.Unmarshal(line, &t); err != nil {
			return nil, fmt.Errorf("parse log line: %w", err)
		}
		if strip {
			t = t.StripHidden()
		}
		out = append(out, t)
	}
	return out, sc.Err()
}

// Session is one reconstructed conversation.
type Session struct {
	ID    string
	Turns []schema.RawTurn // ordered by turn index
}

// Reconstruct groups turns by session and orders them by turn index (never
// trusting input order). This is the "order calls within a session" step.
func Reconstruct(turns []schema.RawTurn) []Session {
	byID := map[string][]schema.RawTurn{}
	var order []string
	for _, t := range turns {
		if _, ok := byID[t.SessionID]; !ok {
			order = append(order, t.SessionID)
		}
		byID[t.SessionID] = append(byID[t.SessionID], t)
	}
	sessions := make([]Session, 0, len(order))
	for _, id := range order {
		ts := byID[id]
		sort.SliceStable(ts, func(i, j int) bool { return ts[i].TurnIndex < ts[j].TurnIndex })
		sessions = append(sessions, Session{ID: id, Turns: ts})
	}
	return sessions
}

// ServedObs is one observed (model, prompt) serving event: an assistant turn,
// the user prompt that preceded it, and the next user turn (which carries the
// implicit outcome signal). It carries NO ground truth.
type ServedObs struct {
	PromptID   string
	Prompt     string // preceding user turn content
	TurnType   string // "open" | "followup"
	Model      string
	Response   string
	Propensity *float64
	SessionID  string
	TurnIndex  int
	Timestamp  int64
	NextUser   *schema.RawTurn // following user turn, nil if session ended
	// PrevModel/NextModel let signal heuristics detect escalation (switch).
	NextModel string // model that served the NEXT assistant turn ("" if none)
}

// Observations flattens reconstructed sessions into served observations.
func Observations(sessions []Session) []ServedObs {
	var obs []ServedObs
	for _, s := range sessions {
		ts := s.Turns
		for i := 0; i < len(ts); i++ {
			if ts[i].Role != schema.RoleAssistant {
				continue
			}
			o := ServedObs{
				PromptID:   fmt.Sprintf("%s-t%02d", s.ID, ts[i].TurnIndex),
				Model:      ts[i].ServedModel,
				Response:   ts[i].Content,
				Propensity: ts[i].Propensity,
				SessionID:  s.ID,
				TurnIndex:  ts[i].TurnIndex,
				Timestamp:  ts[i].Timestamp,
			}
			// preceding user turn = the prompt served
			if i > 0 && ts[i-1].Role == schema.RoleUser {
				o.Prompt = ts[i-1].Content
				if ts[i-1].TurnIndex == 0 {
					o.TurnType = "open"
				} else {
					o.TurnType = "followup"
				}
			} else {
				o.TurnType = "open"
			}
			// following user turn = implicit signal carrier
			if i+1 < len(ts) && ts[i+1].Role == schema.RoleUser {
				nu := ts[i+1]
				o.NextUser = &nu
			}
			// next assistant model (for escalation/switch detection)
			for j := i + 1; j < len(ts); j++ {
				if ts[j].Role == schema.RoleAssistant {
					o.NextModel = ts[j].ServedModel
					break
				}
			}
			obs = append(obs, o)
		}
	}
	return obs
}
