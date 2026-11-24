package mobileclient

import (
	"slices"
	"testing"

	"shioji/internal/domain"
)

func TestCompactionProjectionKeepsJournalIdentityAndCompletedOrdinaryTurn(t *testing.T) {
	action := domain.ContextAction{OperationID: "compact-1", TurnID: "maintenance", State: domain.ExecutionCompleted}
	items := []domain.Item{
		{ID: "reply", TurnID: "ordinary", Kind: "assistant", Text: "Done", Final: true},
		{ID: "context-command:compact-1", Kind: "command", Presentation: "compact", Final: true},
		{ID: "source-progress", TurnID: "maintenance", Kind: "activity", Presentation: "compaction", Status: "running"},
	}
	afterOrder := int64(0)
	projected := CompactionItems(items, &action, &afterOrder, true)
	if len(projected) != 3 || projected[2].ID != items[2].ID || !projected[2].Final || projected[2].Name != "上下文已压缩" {
		t.Fatalf("projected activity lost its identity or state: %+v", projected)
	}
	if items[2].Final || items[2].Status != "running" {
		t.Fatalf("projection modified the authoritative item: %+v", items[2])
	}
	turns := []domain.Turn{{ID: "ordinary", Status: domain.ExecutionCompleted}, {ID: "maintenance", Status: domain.ExecutionCompleted, Presentation: "compaction"}}
	for _, row := range TranscriptRows(projected, turns, false, nil) {
		if row.Footer == "maintenance" {
			t.Fatal("maintenance acquired an ordinary reply footer")
		}
	}
	if old := CompactionItems(items[:1], &action, &afterOrder, false); len(old) != 1 {
		t.Fatalf("recent activity appeared in an earlier history window: %+v", old)
	}
}

func TestCompactionLiveStatusFollowsLatestActivityOfCurrentTurn(t *testing.T) {
	turns := []domain.Turn{{ID: "old", Status: domain.ExecutionCompleted}, {ID: "current", Status: domain.ExecutionRunning}}
	items := []domain.Item{
		{ID: "automatic", TurnID: "current", Presentation: "compaction", ActivityOrder: 5},
		{ID: "late", TurnID: "old", Presentation: "compaction", ActivityOrder: 20},
		{ID: "child", TurnID: "current", WorkID: "subagent", Kind: "tool", ActivityOrder: 22},
	}
	if got := LiveCompaction(items, turns); got != "automatic" {
		t.Fatalf("live compaction = %q", got)
	}
	items = append(items, domain.Item{ID: "reply", TurnID: "current", Kind: "assistant", Text: "Next", ActivityOrder: 6})
	if got := LiveCompaction(items, turns); got != "" {
		t.Fatalf("obsolete compaction retained live status: %q", got)
	}
}

func TestCompactionProjectionRespectsCommandAndHistoryOrder(t *testing.T) {
	action := domain.ContextAction{OperationID: "compact", TurnID: "maintenance", State: domain.ExecutionCompleted}
	command := domain.Item{ID: "context-command:compact", TurnID: "maintenance", Kind: "command", Presentation: "compact", Order: 11}
	earlier := domain.Item{ID: "earlier", TurnID: "before", Kind: "assistant", Order: 10}
	later := domain.Item{ID: "later", TurnID: "after", Kind: "assistant", Order: 12}
	if rows := CompactionItems([]domain.Item{later}, &action, nil, true); len(rows) != 1 {
		t.Errorf("old terminal compaction appeared in the latest window: %+v", rows)
	}
	if rows := CompactionItems([]domain.Item{command}, &action, nil, false); len(rows) != 1 {
		t.Errorf("older window synthesized a result from a newer window: %+v", rows)
	}
	for _, state := range []domain.ExecutionState{domain.ExecutionRunning, domain.ExecutionCompleted} {
		action.State = state
		rows := CompactionItems([]domain.Item{earlier, command, later}, &action, nil, true)
		var ids []string
		for _, item := range rows {
			ids = append(ids, item.ID)
		}
		if !slices.Equal(ids, []string{"earlier", command.ID, "context-compaction:compact", "later"}) {
			t.Errorf("%s: fallback status must follow its own command: %q", state, ids)
		}
	}
	action.TurnID, action.State = "", domain.ExecutionStarting
	rows := CompactionItems([]domain.Item{earlier, later}, &action, &earlier.Order, true)
	if len(rows) != 4 || rows[0].ID != earlier.ID || rows[1].ID != command.ID ||
		rows[2].ID != "context-compaction:compact" || rows[3].ID != later.ID {
		t.Errorf("local command moved after later output: %+v", rows)
	}
}
