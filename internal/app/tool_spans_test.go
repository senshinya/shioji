package app

import (
	"shioji/internal/domain"
	"testing"
)

func TestToolSpansMergeLeadingAndFollowingReasoningSummaries(t *testing.T) {
	items := []domain.Item{
		{ID: "plan", TurnID: "t", Kind: "activity", Presentation: "reasoning", Text: "先分析范围", Final: true},
		{ID: "read", TurnID: "t", Kind: "tool", Presentation: "files", Final: true},
		{ID: "think", TurnID: "t", Kind: "activity", Presentation: "reasoning", Text: "核对调用顺序", Final: true},
		{ID: "test", TurnID: "t", Kind: "tool", Presentation: "command", Final: true},
		{ID: "wrapup", TurnID: "t", Kind: "activity", Presentation: "reasoning", Text: "整理结论"},
		{ID: "reply", TurnID: "t", Kind: "assistant", Text: "完成", Final: true},
		{ID: "next-plan", TurnID: "t", Kind: "activity", Presentation: "reasoning", Text: "准备下一组", Final: true},
		{ID: "next-tool", TurnID: "t", Kind: "tool", Presentation: "command", Final: true},
		{ID: "next-think", TurnID: "t", Kind: "activity", Presentation: "reasoning", Text: "确认结果", Final: true},
		{ID: "other-turn-thought", TurnID: "t2", Kind: "activity", Presentation: "reasoning", Text: "单独思考", Final: true},
	}
	spans := ToolSpans(items, nil, domain.HistorySpacer)
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(spans), spans)
	}
	first := spans[0]
	if first.ID != "read" || first.End != 5 || first.Count != 2 || first.Loaded != 2 || !first.Unfinished {
		t.Fatalf("first span = %+v, want ID=read End=5 Count=2 Loaded=2 Unfinished=true", first)
	}
	second := spans[6]
	if second.ID != "next-tool" || second.End != 9 || second.Count != 1 || second.Loaded != 1 || second.Unfinished {
		t.Fatalf("second span = %+v, want ID=next-tool End=9 Count=1 Loaded=1 Unfinished=false", second)
	}
	if _, merged := spans[9]; merged {
		t.Fatalf("standalone reasoning without tools was merged: %+v", spans)
	}
}

func TestToolSpansContinueUnloadedGroupOnLeadingReasoning(t *testing.T) {
	// Window starts at a reasoning summary that follows an unloaded tool call.
	partial := []domain.Item{
		{ID: "think", TurnID: "t", Order: 4, Kind: "activity", Presentation: "reasoning", Text: "继续检查", Final: true},
		{ID: "test", TurnID: "t", Order: 5, Kind: "tool", Final: true},
	}
	history := &domain.HistoryWindow{
		First:  4,
		Last:   5,
		Older:  true,
		Groups: []domain.HistoryToolGroup{{ID: "read", TurnID: "t", First: 3, Last: 5, Count: 2}},
	}
	spans := ToolSpans(partial, history, domain.HistorySpacer)
	if span := spans[0]; len(spans) != 1 || span.ID != "read" || span.End != 2 || span.Count != 2 || span.Loaded != 1 {
		t.Fatalf("paginated continuation lost its group: %+v", spans)
	}

	// Summary-only tail page at the end of a tool group still belongs to that group.
	tailOnly := []domain.Item{
		{ID: "wrapup", TurnID: "t", Order: 6, Kind: "activity", Presentation: "reasoning", Text: "整理结果", Final: true},
	}
	tailHistory := &domain.HistoryWindow{
		First:  6,
		Last:   6,
		Older:  true,
		Groups: []domain.HistoryToolGroup{{ID: "read", TurnID: "t", First: 3, Last: 6, Count: 2}},
	}
	spans = ToolSpans(tailOnly, tailHistory, domain.HistorySpacer)
	if span := spans[0]; len(spans) != 1 || span.ID != "read" || span.End != 1 || span.Count != 2 || span.Loaded != 0 {
		t.Fatalf("trailing summary page lost its group: %+v", spans)
	}

	// Leading reasoning summary before the first tool merges into the tool group
	// while keeping the group's ID anchored to the first tool call.
	withLeading := []domain.Item{
		{ID: "prompt", TurnID: "t", Order: 1, Kind: "user", Text: "check"},
		{ID: "plan", TurnID: "t", Order: 2, Kind: "activity", Presentation: "reasoning", Text: "先分析", Final: true},
		{ID: "read", TurnID: "t", Order: 3, Kind: "tool", Final: true},
		{ID: "think", TurnID: "t", Order: 4, Kind: "activity", Presentation: "reasoning", Text: "再核对", Final: true},
	}
	withLeadingHistory := &domain.HistoryWindow{
		First:  1,
		Last:   4,
		Groups: []domain.HistoryToolGroup{{ID: "read", TurnID: "t", First: 2, Last: 4, Count: 1}},
	}
	spans = ToolSpans(withLeading, withLeadingHistory, domain.HistorySpacer)
	if span := spans[1]; len(spans) != 1 || span.ID != "read" || span.End != 4 || span.Count != 1 || span.Loaded != 1 {
		t.Fatalf("tool span with leading reasoning = %+v", spans)
	}
}
