package app

import (
	"slices"

	"shioji/internal/domain"
)

// ToolSpan is a consecutive run starting at a tool call (or continuing an
// earlier unloaded tool group) and merging subsequent tool calls and public
// summaries from one turn. Count includes calls outside the loaded window.
type ToolSpan struct {
	ID         string
	End        int
	Count      int
	Loaded     int
	Unfinished bool
}

func ToolSpans(items []domain.Item, history *domain.HistoryWindow, spacer func(domain.Item) bool) map[int]ToolSpan {
	var groups []domain.HistoryToolGroup
	var windowFirst int64
	if history != nil {
		windowFirst = history.First
		for _, hint := range history.Groups {
			// An older worker can report one group across a loaded boundary.
			// Its ID and total cannot describe the distinct visible groups.
			if !slices.ContainsFunc(items, func(item domain.Item) bool {
				return item.Order >= hint.First && item.Order <= hint.Last &&
					(item.TurnID != hint.TurnID || !domain.ToolGroupMember(item) && !spacer(item))
			}) {
				groups = append(groups, hint)
			}
		}
	}
	var spans map[int]ToolSpan
	for i := 0; i < len(items); {
		first := items[i]
		if spacer(first) || first.Kind != "tool" && (!domain.ReasoningSummary(first) || !continuesHistoryToolGroup(first, groups, windowFirst)) {
			i++
			continue
		}
		span := ToolSpan{End: i}
		for span.End < len(items) {
			item := items[span.End]
			if item.TurnID != first.TurnID || !domain.ToolGroupMember(item) && !spacer(item) {
				break
			}
			if item.Kind == "tool" {
				if span.ID == "" {
					span.ID = item.ID
				}
				span.Loaded++
			}
			span.Unfinished = span.Unfinished || !spacer(item) && !item.Final
			span.End++
		}
		span.Count = span.Loaded
		if first.Order > 0 {
			identified := false
			for _, hint := range groups {
				if hint.TurnID != first.TurnID || hint.Last < first.Order || hint.First > items[span.End-1].Order {
					continue
				}
				loaded := 0
				for _, item := range items[i:span.End] {
					if item.Kind == "tool" && item.Order >= hint.First && item.Order <= hint.Last {
						loaded++
					}
				}
				span.Count += max(0, hint.Count-loaded)
				if !identified {
					span.ID = hint.ID
					identified = true
				}
			}
		}
		if span.ID != "" {
			if spans == nil {
				spans = make(map[int]ToolSpan)
			}
			spans[i] = span
		}
		i = span.End
	}
	return spans
}

func continuesHistoryToolGroup(item domain.Item, groups []domain.HistoryToolGroup, windowFirst int64) bool {
	if item.Order <= 0 {
		return false
	}
	for _, hint := range groups {
		if hint.TurnID == item.TurnID && hint.First < item.Order && hint.Last >= item.Order && (windowFirst == 0 || hint.First < windowFirst) {
			return true
		}
	}
	return false
}
