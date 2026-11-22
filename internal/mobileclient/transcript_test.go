package mobileclient

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestToolDisclosureWaitsForVisibleContentAndHonorsManualReopen(t *testing.T) {
	var timeline ToolTimeline
	detail := domain.SessionDetail{Turns: []domain.Turn{
		{ID: "closed", Status: domain.ExecutionCompleted}, {ID: "current", Status: domain.ExecutionRunning},
	}}
	detail.Items = []domain.Item{
		{ID: "old-tool", TurnID: "closed", Kind: "tool", Final: true},
		{ID: "prompt", TurnID: "current", Kind: "user", Text: "verify"},
		{ID: "first", TurnID: "current", Kind: "tool", ActivityOrder: 5},
	}
	groups := func() map[int]ToolGroup {
		items := ConversationItems(detail)
		return timeline.Groups(items, TranscriptRows(items, detail.Turns, true), detail, true, false)
	}
	initial := groups()
	if initial[0].Open || !initial[2].Open || !initial[2].Live {
		t.Fatalf("opening a session did not fold history and expand only current tools: %+v", initial)
	}
	key := initial[2].Key
	detail.Items[2].Final = true
	detail.Items = append(detail.Items,
		domain.Item{ID: "empty-reasoning", TurnID: "current", Kind: "activity", Presentation: "reasoning"},
		domain.Item{ID: "empty-output", TurnID: "current", Kind: "assistant"},
		domain.Item{ID: "next", TurnID: "current", Kind: "tool", ActivityOrder: 6},
	)
	if got := groups()[2]; !got.Open || got.Key != key || got.Count != 2 || !got.Live {
		t.Fatalf("completion, invisible entries and append changed the group: %+v", got)
	}
	detail.Items = append(detail.Items, domain.Item{ID: "late", TurnID: "closed", Kind: "tool", ActivityOrder: 10, Final: true})
	if got := groups()[2]; !got.Open || !got.Live {
		t.Fatalf("old output reclaimed active status or folded the tools: %+v", got)
	}
	detail.Items = append(detail.Items, domain.Item{ID: "reply", TurnID: "current", Kind: "assistant", ActivityOrder: 7, Text: "here it is"})
	if got := groups()[2]; got.Open || got.Live {
		t.Fatalf("visible reply did not take over and fold the group: %+v", got)
	}
	timeline.Toggle(key)
	detail.Items[len(detail.Items)-1].Text += " with details"
	if got := groups()[2]; !got.Open || got.Live {
		t.Fatalf("reply updates overwrote manual disclosure: %+v", got)
	}
}

func TestToolDisclosurePreservesHistoryGroupIdentityAcrossLoadedPages(t *testing.T) {
	var timeline ToolTimeline
	turn := domain.Turn{ID: "turn", Status: domain.ExecutionCompleted}
	tail := domain.Item{ID: "tail", Kind: "tool", TurnID: turn.ID, Order: 4, Final: true}
	detail := domain.SessionDetail{
		Turns: []domain.Turn{turn}, Items: []domain.Item{tail},
		History: &domain.HistoryWindow{First: 4, Last: 4, Older: true, Groups: []domain.HistoryToolGroup{{ID: "scope", TurnID: turn.ID, First: 1, Last: 4, Count: 2, Issues: 1}}},
	}
	groups := func() map[int]ToolGroup {
		return timeline.Groups(detail.Items, TranscriptRows(detail.Items, detail.Turns, false), detail, false, false)
	}
	group := groups()[0]
	if group.Open || !group.Ended || group.Count != 2 {
		t.Fatalf("loaded group did not use its whole-history metadata: %+v", group)
	}
	timeline.Toggle(group.Key)
	detail.Items = append([]domain.Item{{ID: "earlier", Kind: "tool", TurnID: turn.ID, Order: 1, Final: true}}, tail)
	detail.History.First, detail.History.Older = 1, false
	if got := groups()[0]; got.Key != group.Key || !got.Open {
		t.Fatalf("loading earlier calls reset the disclosure: %+v", got)
	}
}

func TestToolGroupOutcomeUsesOnlyKnownCalls(t *testing.T) {
	var timeline ToolTimeline
	turn := domain.Turn{ID: "turn", Status: domain.ExecutionCompleted}
	items := []domain.Item{
		{ID: "first", TurnID: turn.ID, Kind: "tool", Status: "completed", Order: 1, Final: true},
		{ID: "last", TurnID: turn.ID, Kind: "tool", Status: "completed", Order: 2, Final: true},
	}
	groups := func(window *domain.HistoryWindow) ToolGroup {
		detail := domain.SessionDetail{Items: items, Turns: []domain.Turn{turn}, History: window}
		return timeline.Groups(items, TranscriptRows(items, detail.Turns, false), detail, false, false)[0]
	}
	if got := groups(nil); got.Outcome != ToolGroupCompleted {
		t.Fatalf("confirmed calls did not report completion: %+v", got)
	}
	turn.Status = domain.ExecutionRunning
	if got := groups(nil); got.Outcome != "" {
		t.Fatalf("the unsealed tool tail reported completion before the turn moved on: %+v", got)
	}
	items = append(items, domain.Item{ID: "reply", TurnID: turn.ID, Kind: "assistant", Text: "done"})
	if got := groups(nil); got.Outcome != ToolGroupCompleted {
		t.Fatalf("visible reply did not seal the completed group: %+v", got)
	}
	items = items[:2]
	turn.Status = domain.ExecutionCompleted
	items[1].Status = "unknown"
	if got := groups(nil); got.Outcome != ToolGroupEnded {
		t.Fatalf("unknown result was treated as success: %+v", got)
	}
	items[1].Final = false
	if got := groups(nil); got.Outcome != "" {
		t.Fatalf("unfinished call was treated as closed: %+v", got)
	}
	items = items[:1]
	if got := groups(&domain.HistoryWindow{First: 1, Last: 1, Newer: true,
		Groups: []domain.HistoryToolGroup{{ID: "group", TurnID: "turn", First: 1, Last: 2, Count: 2}}}); got.Outcome != "" {
		t.Fatalf("partial history asserted a result for unavailable calls: %+v", got)
	}
}

func TestConversationItemsHideWorkButKeepConversationHistory(t *testing.T) {
	detail := domain.SessionDetail{
		Works: []domain.WorkActivity{{ID: "task", Kind: "background", SourceItemID: "task-start"}},
		Items: []domain.Item{
			{ID: "question", Kind: "user", Text: "检查后台任务"},
			{ID: "spawn", Kind: "tool", Presentation: "agents"},
			{ID: "old-agent", Kind: "tool", Agents: []domain.AgentActivity{{ID: "older"}}},
			{ID: "task-start", Kind: "tool"},
			{ID: "child-approval", Kind: "request", WorkID: "child"},
			{ID: "task-output", Kind: "tool", WorkID: "task"},
			{ID: "plan", Kind: "plan"},
			{ID: "read", Kind: "tool"},
			{ID: "approval", Kind: "request"},
			{ID: "approved", Kind: "request_result"},
			{ID: "answer", Kind: "assistant", Text: "已检查。"},
			{ID: "async-question", Kind: "assistant", Final: true, Questions: []domain.Question{{ID: "q", Text: "何时继续？"}}},
		},
	}
	var got []string
	for _, item := range ConversationItems(detail) {
		got = append(got, item.ID)
	}
	if want := []string{"question", "read", "approval", "approved", "answer", "async-question"}; !slices.Equal(got, want) {
		t.Fatalf("conversation items = %v, want %v", got, want)
	}
}

func TestConversationReceiptsKeepTheAnchorAndLegacyFallback(t *testing.T) {
	text := func(value string) []domain.ContentPart { return []domain.ContentPart{{Text: value}} }
	answered := string(domain.RequestAnswered)
	detail := domain.SessionDetail{Session: domain.Session{ID: "session"}, Items: []domain.Item{
		{ID: "request:lone", Kind: "request", TurnID: "first", Status: answered},
		{ID: "request:approval", Kind: "request", TurnID: "first", Status: answered},
		{ID: "request:unanswered", Kind: "request", TurnID: "first", Status: "pending"},
		{ID: "reply", Kind: "assistant", TurnID: "second", Text: "Later message"},
		{ID: "request-result:approval", Kind: "request_result", TurnID: "first", Presentation: "approval", Parts: text("stale duplicate")},
		{ID: "request-result:unanswered", Kind: "request_result", TurnID: "first", Presentation: "approval", Parts: text("keep until the anchor is confirmed")},
		{ID: "request-result:legacy", Kind: "request_result", TurnID: "old", Presentation: "questions", Parts: text("older anchor outside window")},
		{ID: "request-result:form", Kind: "request_result", TurnID: "second", Presentation: "form", Parts: text("form result")},
		{ID: "request-result:lone", Kind: "request_result", WorkID: "child", TurnID: "first", Presentation: "questions", Parts: text("wrong work")},
	}, Requests: []domain.PendingRequest{
		{ID: "lone", SessionID: "session", TurnID: "first", WorkID: "child", Kind: "questions", State: domain.RequestAnswered, Receipt: text("wrong scope")},
		{ID: "approval", SessionID: "session", TurnID: "first", Kind: "approval", State: domain.RequestAnswered, Receipt: text("current decision")},
	}}
	items := ConversationItems(detail)
	var ids []string
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	want := []string{"request:lone", "request:approval", "request:unanswered", "reply", "request-result:unanswered", "request-result:legacy", "request-result:form"}
	if !slices.Equal(ids, want) || len(items[0].Parts) != 0 || items[1].Presentation != "approval" ||
		len(items[1].Parts) != 1 || items[1].Parts[0].Text != "current decision" {
		t.Fatalf("unexpected projected receipts: %+v", items)
	}
	if len(detail.Items[1].Parts) != 0 || detail.Items[4].Parts[0].Text != "stale duplicate" {
		t.Fatal("rendering changed the accepted history")
	}
	// Older Workers can provide a journal receipt without the request payload.
	detail.Requests = nil
	if fallback := ConversationItems(detail)[1]; len(fallback.Parts) != 1 || fallback.Parts[0].Text != "stale duplicate" {
		t.Fatalf("legacy reply did not project onto its anchor: %+v", fallback)
	}
}

func TestConversationMovesLateGoalMarkerToItsTurnWithoutChangingHistory(t *testing.T) {
	detail := domain.SessionDetail{Items: []domain.Item{
		{ID: "prior", TurnID: "previous", Kind: "assistant", Text: "Previous result"},
		{ID: "hook", TurnID: "goal", Kind: "activity", Presentation: "context"},
		{ID: "late", TurnID: "previous", Kind: "tool", Final: true},
		{ID: "tool", TurnID: "goal", Kind: "tool", Final: true},
		{ID: "continue", TurnID: "goal", Kind: "activity", Presentation: "goal-continuation", Final: true},
		{ID: "reply", TurnID: "goal", Kind: "assistant", Text: "New result"},
	}}
	ids := func(items []domain.Item) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.ID)
		}
		return out
	}
	want := []string{"prior", "continue", "hook", "late", "tool", "reply"}
	if got := ids(ConversationItems(detail)); !slices.Equal(got, want) {
		t.Fatalf("rendered continuation at the wrong position: got %v, want %v", got, want)
	}
	if got := ids(detail.Items); !slices.Equal(got, []string{"prior", "hook", "late", "tool", "continue", "reply"}) {
		t.Fatalf("presentation changed authoritative history: %v", got)
	}
}

func TestTranscriptRowsKeepOneReceiptOutsideDisclosuresAndActiveAtEnd(t *testing.T) {
	detail := domain.SessionDetail{
		Turns: []domain.Turn{
			{ID: "done", Status: domain.ExecutionCompleted},
			{ID: "active", Status: domain.ExecutionRunning},
			{ID: "compact", Status: domain.ExecutionCompleted, Presentation: "compaction"},
		},
		Items: []domain.Item{
			{ID: "command-a", Kind: "tool", TurnID: "done"},
			{ID: "invisible", Kind: "assistant", TurnID: "done"},
			{ID: "command-b", Kind: "tool", TurnID: "done"},
			{ID: "sent", Kind: "user", TurnID: "active"},
			{ID: "late-result", Kind: "tool", TurnID: "done"},
			{ID: "compaction", Kind: "activity", TurnID: "compact"},
		},
	}
	items := ConversationItems(detail)
	want := []TranscriptRow{
		{First: 0, End: 2},
		{First: 2, End: 3},
		{First: 3, End: 4},
		{Footer: "done"},
		{First: 4, End: 5},
		{Footer: "active"},
	}
	if got := TranscriptRows(items, detail.Turns, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
	if got := TranscriptRows(items, detail.Turns, false); !reflect.DeepEqual(got, want[:len(want)-1]) {
		t.Fatalf("historical window shows current live receipt: %+v", got)
	}
}

func TestAcceptedInsertedInputRetainsEarlierReplyFooter(t *testing.T) {
	start := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	inserted := start.Add(80 * time.Second)
	turn := domain.Turn{ID: "turn", Status: domain.ExecutionRunning, StartedAt: &start}
	items := []domain.Item{
		{ID: "prompt", Kind: "user", TurnID: turn.ID, Status: "sent", SentAt: &start},
		{ID: "initial", Kind: "assistant", TurnID: turn.ID, Text: "**初步结果**"},
		{ID: "command", Kind: "tool", TurnID: turn.ID, Text: "命令输出"},
		{ID: "inserted", Kind: "user", TurnID: turn.ID, Status: "dispatching", Text: "补充说明", SentAt: &inserted},
	}
	count := func(rows []TranscriptRow) int {
		footers := 0
		for _, row := range rows {
			if row.Footer == turn.ID {
				footers++
			}
		}
		return footers
	}
	if got := count(TranscriptRows(items, []domain.Turn{turn}, true)); got != 1 {
		t.Fatalf("dispatching insertion created %d footers, want only the live footer", got)
	}
	items[3].Status = "sent"
	rows := TranscriptRows(items, []domain.Turn{turn}, true)
	if got := count(rows); got != 2 {
		t.Fatalf("accepted insertion left %d footers, want an earlier reply receipt and the live footer", got)
	}
	if rows[3].Footer != turn.ID || rows[4].First != 3 || rows[5].Footer != turn.ID {
		t.Fatalf("footers must stay on either side of the inserted message: %+v", rows)
	}
	receipt := rows[3].Receipt
	if receipt.BeforeInput != "inserted" || !slices.Equal(receipt.Reply, []string{"**初步结果**"}) ||
		receipt.CompletedAt == nil || !receipt.CompletedAt.Equal(inserted) || receipt.DurationMS == nil || *receipt.DurationMS != 80000 {
		t.Fatalf("earlier receipt did not retain its own accepted time and reply: %+v", receipt)
	}
	items = append(items, domain.Item{ID: "later", Kind: "assistant", TurnID: turn.ID, Text: "复查结果"})
	turn.Status = domain.ExecutionCompleted
	rows = TranscriptRows(items, []domain.Turn{turn}, true)
	if got := rows[len(rows)-1].Receipt; got.BeforeInput != "" || got.AfterInput != "inserted" || !slices.Equal(got.Reply, []string{"复查结果"}) || len(got.ToolOutput) != 0 {
		t.Fatalf("completed receipt crossed the accepted input: %+v", got)
	}
	items[3].SentAt = &start // An imported thread can only supply the turn start.
	rows = TranscriptRows(items, []domain.Turn{turn}, true)
	if got := rows[3].Receipt; got.CompletedAt != nil || got.DurationMS != nil {
		t.Fatalf("unavailable insertion times must stay unknown: %+v", got)
	}
}

func TestSentMessageRunsPreserveFileAndSkillOrderingWithUnicodeOffsets(t *testing.T) {
	item := domain.Item{
		Text: "查🙂 并核对。",
		Attachments: []domain.Attachment{
			{ID: "snap", Name: "连接.go", SourcePath: "/work/连接.go"},
			{ID: "file", Name: "ordinary.png"},
		},
		Skills: []domain.SkillReference{
			{ID: "skill", Name: "review-check"},
			{ID: "old", Name: "legacy-review"},
		},
		ReferencePositions: []domain.ReferencePosition{
			{SkillID: "skill", Position: 2},
			{AttachmentID: "snap", Position: 6},
		},
	}
	runs := SentMessageRuns(item)
	var words []string
	for _, run := range runs {
		words = append(words, run.Text)
	}
	want := []string{"查", "review-check", "🙂 并", "连接.go", "核对。", " ", "legacy-review"}
	if !slices.Equal(words, want) || runs[1].Skill == nil || runs[3].Attachment == nil || runs[3].Attachment.ID != "snap" {
		t.Fatalf("sent content = %+v, want %v with the original file identity", runs, want)
	}
}
