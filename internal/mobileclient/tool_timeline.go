package mobileclient

import (
	"slices"

	"shioji/internal/app"
	"shioji/internal/domain"
)

type ToolGroupOutcome string

const (
	ToolGroupCompleted ToolGroupOutcome = "completed"
	ToolGroupEnded     ToolGroupOutcome = "ended"
)

type ToolGroup struct {
	Key               string
	Count             int
	Open, Ended, Live bool
	Outcome           ToolGroupOutcome
}

type ToolTimeline struct {
	disclosures map[string]app.ToolDisclosure
}

func (t *ToolTimeline) Toggle(key string) {
	if state, ok := t.disclosures[key]; ok {
		t.disclosures[key] = state.Toggle()
	}
}

// Groups keeps disclosures outside virtual list rows. Completion changes the
// heading, while visible content seals a group and folds it only once.
func (t *ToolTimeline) Groups(items []domain.Item, rows []TranscriptRow, detail domain.SessionDetail, observeLatest, collapse bool) map[int]ToolGroup {
	if len(items) == 0 {
		return nil
	}
	initial := t.disclosures == nil
	if initial {
		t.disclosures = make(map[string]app.ToolDisclosure)
	}
	var activeTurn string
	turns := make(map[string]domain.ExecutionState, len(detail.Turns))
	for _, turn := range detail.Turns {
		turns[turn.ID] = turn.Status
		if turn.Status.Active() && observeLatest {
			activeTurn = turn.ID
		}
	}
	live := LatestTurnActivity(items, activeTurn)
	lastContent := -1
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if !domain.ToolGroupMember(item) && !transcriptSpacer(item) && (item.Kind != "user" || acceptedReplyInput(item)) {
			lastContent = i
			break
		}
	}
	groups := make(map[int]ToolGroup)
	for _, row := range rows {
		span := row.tool
		if span.ID == "" {
			continue
		}
		first := items[row.First]
		group := ToolGroup{Key: "tool-group:" + span.ID, Count: span.Count}
		switch turns[first.TurnID] {
		case domain.ExecutionCompleted, domain.ExecutionFailed, domain.ExecutionInterrupted, domain.ExecutionAbandoned:
			group.Ended = true
		}
		outcome := ToolGroupCompleted
		unfinished := false
		for _, item := range items[row.First:row.End] {
			if item.Kind == "tool" {
				unfinished = unfinished || !item.Final
				if item.Status != "completed" || item.ExitCode != nil && *item.ExitCode != 0 {
					outcome = ToolGroupEnded
				}
			}
			if live != nil && live.ID == item.ID && row.End > lastContent {
				group.Live = true
			}
		}
		// Final call records cannot seal a running tail. More calls may still
		// join it until later content arrives or the turn itself ends.
		if (group.Ended || row.End <= lastContent) && !unfinished && span.Count == span.Loaded {
			group.Outcome = outcome
		}
		state, known := t.disclosures[group.Key]
		if !known {
			state = app.ToolClosedBeforeProse
			if group.Live && !collapse && (!initial || span.Unfinished) {
				state = app.ToolOpenedBeforeProse
			}
		}
		// An input steered into the running turn is not a prose reply.
		steered := row.End < len(items) && items[row.End].TurnID == first.TurnID && items[row.End].Kind == "user"
		if row.End <= lastContent && !steered {
			state = state.ObserveProse()
		}
		t.disclosures[group.Key] = state
		group.Open = !state.Closed()
		groups[row.First] = group
	}
	return groups
}

func LatestTurnActivity(items []domain.Item, turnID string) *domain.Item {
	if turnID == "" {
		return nil
	}
	var latest *domain.Item
	for i, item := range slices.Backward(items) {
		if item.TurnID != turnID || item.WorkID != "" {
			continue
		}
		switch item.Kind {
		case "tool", "assistant", "reasoning", "activity":
			if latest == nil || item.ActivityOrder > latest.ActivityOrder {
				latest = &items[i]
			}
		}
	}
	return latest
}
