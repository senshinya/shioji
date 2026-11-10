package domain

import (
	"encoding/json"
	"time"
)

// WorkActivity is an independently executing child agent or background task.
// IDs are opaque, session-scoped references supplied by the owning adapter.
type WorkActivity struct {
	Reference    string     `json:"reference,omitempty"` // opaque adapter reference
	OriginRef    string     `json:"originRef,omitempty"` // opaque source execution context
	CanStop      bool       `json:"canStop,omitempty"`
	CanSteer     bool       `json:"canSteer,omitempty"`
	CanContinue  bool       `json:"canContinue,omitempty"`
	ID           string     `json:"id"`
	Kind         string     `json:"kind"` // agent or background
	TurnID       string     `json:"turnId"`
	ParentID     string     `json:"parentId,omitempty"`
	SourceItemID string     `json:"sourceItemId,omitempty"`
	Title        string     `json:"title"`
	Brief        string     `json:"brief,omitempty"`
	Model        string     `json:"model,omitempty"`
	Effort       string     `json:"effort,omitempty"`
	State        string     `json:"state"`
	Latest       string     `json:"latest,omitempty"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
	DurationMS   *int64     `json:"durationMs,omitempty"`
	Revision     int64      `json:"revision"`
}

func (w WorkActivity) Active() bool {
	return w.State == "running" || w.State == "starting" || w.State == "waiting"
}

type WorkItem struct {
	Item     Item  `json:"item"`
	Revision int64 `json:"revision"`
}
type WorkDetail struct {
	PendingInput *WorkInputOperation `json:"pendingInput,omitempty"`
	SessionID    string              `json:"sessionId"`
	Work         WorkActivity        `json:"work"`
	Items        []WorkItem          `json:"items"`
	Truncated    bool                `json:"truncated,omitempty"`
	Sequence     int64               `json:"sequence"`
}

// WorkInput targets one observed independent execution. Mode is explicit so a
// finished turn cannot silently turn an intended correction into a new task.
type WorkInput struct {
	OperationID string `json:"operationId"`
	SessionID   string `json:"sessionId"`
	WorkID      string `json:"workId"`
	Mode        string `json:"mode"` // steer or continue
	Text        string `json:"text"`
}

type WorkInputOperation struct {
	ExecutionStopped bool           `json:"executionStopped,omitempty"`
	Order            int64          `json:"order"`
	Input            WorkInput      `json:"input"`
	State            OperationState `json:"state"`
	Error            string         `json:"error,omitempty"`
	SentAt           time.Time      `json:"sentAt"`
}

// SessionWorks keeps saved, older summary-only agent observations visible.
// Rich adapter records win by opaque reference, without parsing source IDs.
func SessionWorks(d SessionDetail) []WorkActivity {
	out := append([]WorkActivity(nil), d.Works...)
	known := map[string]bool{}
	for _, w := range out {
		if w.Kind == "agent" {
			known[w.Reference] = true
		}
	}
	positions := map[string]int{}
	for _, item := range d.Items {
		for _, agent := range item.Agents {
			if known[agent.ID] {
				continue
			}
			state := agent.Status
			if state == "interrupted" {
				state = "stopped"
			}
			if state == "running" || state == "starting" {
				state = "unknown"
			}
			brief := ""
			for _, part := range item.Parts {
				if part.Kind == "text" {
					brief = part.Text
					break
				}
			}
			title := brief
			if title == "" {
				title = "子 Agent"
			}
			for i, r := range title {
				if r == '\n' || i >= 160 {
					title = title[:i]
					break
				}
			}
			w := WorkActivity{ID: agent.ID, Reference: agent.ID, Kind: "agent", TurnID: item.TurnID, SourceItemID: item.ID, Title: title, Brief: brief, Latest: agent.Message, State: state}
			if p, ok := positions[agent.ID]; ok {
				old := out[p]
				if w.Brief == "" {
					w.Brief, w.Title = old.Brief, old.Title
				}
				out[p] = w
			} else {
				positions[agent.ID] = len(out)
				out = append(out, w)
			}
		}
	}
	bounded, _ := LimitWorkRoster(out)
	return bounded
}

// Bound the read projection, never execution or persisted history. Active work
// wins over recent terminal records; large briefs stay in the detail endpoint.
func LimitWorkRoster(in []WorkActivity) ([]WorkActivity, bool) {
	keep := make([]bool, len(in))
	count, size := 0, 0
	truncated := false
	for _, active := range []bool{true, false} {
		for i := len(in) - 1; i >= 0; i-- {
			w := in[i]
			if w.Active() != active {
				continue
			}
			w.Brief = ""
			body, _ := json.Marshal(w)
			if count >= 128 || size+len(body) > 96<<10 {
				truncated = true
				continue
			}
			keep[i] = true
			count++
			size += len(body)
		}
	}
	out := make([]WorkActivity, 0, count)
	for i, w := range in {
		if keep[i] {
			w.Brief = ""
			out = append(out, w)
		}
	}
	return out, truncated
}
