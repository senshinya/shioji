package ui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestHistoryToolGroupsIgnoreSummaryAcrossVisibleActivity(t *testing.T) {
	v := toolTailFixture(1)
	v.Model.Preferences.CollapseLatestTools = true
	v.Model.FoldToolHistory["s"] = true
	d := v.Model.Details["s"]
	d.Items = []domain.Item{
		{ID: "prompt", TurnID: "t", Order: 1, Kind: "user", Text: "检查工具记录", Final: true},
		{ID: "first", TurnID: "t", Order: 10, Kind: "tool", Name: "first command", Status: "completed", Final: true},
		{ID: "status", TurnID: "t", Order: 11, Kind: "activity", Status: "completed", Final: true},
		{ID: "second", TurnID: "t", Order: 12, Kind: "tool", Name: "second command", Status: "completed", Final: true},
		{ID: "third", TurnID: "t", Order: 13, Kind: "tool", Name: "third command", Status: "running"},
	}
	d.History = &domain.HistoryWindow{First: 10, Last: 13, Older: true, ContextID: "prompt", Groups: []domain.HistoryToolGroup{
		{ID: "first", TurnID: "t", First: 10, Last: 13, Count: 3},
	}}
	v.Model.Details["s"] = d
	tester := n.NewTester(v.View, 1120, 680)
	tester.Advance(time.Second)
	saveNativeSkillImage(t, tester, "tool-group-identity.png")
	if !tester.HasText("1 次工具调用") || !tester.HasText("2 次工具调用") || tester.HasText("3 次工具调用") {
		t.Fatalf("expected distinct tool counts after loading history: %v", tester.Texts())
	}
	if err := tester.Click("1 次工具调用"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(time.Second)
	if !tester.HasText("first command") || tester.HasText("second command") {
		t.Fatalf("opening the first tool group also changed the second: %v", tester.Texts())
	}
}
