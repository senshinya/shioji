package app

import "shioji/internal/domain"

// ToolSpan is a consecutive run containing tool calls and public summaries
// from one turn. Count includes calls outside the loaded history window.
type ToolSpan struct {
	ID         string
	End        int
	Count      int
	Loaded     int
	Unfinished bool
}

func ToolSpans(items []domain.Item, history *domain.HistoryWindow, spacer func(domain.Item) bool) map[int]ToolSpan {
	var spans map[int]ToolSpan
	for i := 0; i < len(items); {
		first := items[i]
		if !domain.ToolGroupMember(first) || spacer(first) {
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
		if history != nil && first.Order > 0 {
			identified := false
			for _, hint := range history.Groups {
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
