package app

import (
	"slices"

	"shioji/internal/domain"
)

// ToolSpan is a consecutive run of tool calls and contiguous public reasoning
// summaries from one turn, including leading summaries immediately before the
// first tool call (or continuing an unloaded tool group). Count includes calls
// outside the loaded window.
type ToolSpan struct {
	ID         string
	End        int
	Count      int
	Loaded     int
	Unfinished bool
}

func ToolSpans(items []domain.Item, history *domain.HistoryWindow, spacer func(domain.Item) bool) map[int]ToolSpan {
	var groups []domain.HistoryToolGroup
	if history != nil {
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
		if spacer(first) || !domain.ToolGroupMember(first) {
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
		if span.ID != "" && span.Count > 0 {
			if spans == nil {
				spans = make(map[int]ToolSpan)
			}
			spans[i] = span
		}
		i = span.End
	}
	return spans
}
