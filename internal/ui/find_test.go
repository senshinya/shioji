package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestGlobalSearchLocatesTheExactMessageAfterHistoryArrives(t *testing.T) {
	m := layoutModel()
	m.Drafts["session:s"] = "普通草稿保留"
	delete(m.Details, "s")
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Click("搜索")
	v.searchText, v.searchResultsQuery = "needle", "needle"
	json.Unmarshal([]byte(`[{"sessionId":"s","workspaceId":"w","title":"Layout","itemId":"target","matchStart":7,"matchEnd":13,"snippet":"Target needle"}]`), &v.searchResults)
	ui.Frame()
	if err := ui.Click("搜索结果：Layout，Example"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(200 * time.Millisecond)
	if ui.HasText("Target needle") {
		t.Fatal("unloaded history was fabricated")
	}
	d := domain.SessionDetail{Session: m.Snapshot.Sessions[0]}
	for i := 0; i < 60; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("earlier", i), Kind: "assistant", Text: strings.Repeat("Earlier history.\n\n", 3), Final: true})
	}
	d.Items = append(d.Items, domain.Item{ID: "target", Kind: "user", Text: "Target needle", Status: "sent", Final: true}, domain.Item{ID: "tail", Kind: "assistant", Text: strings.Repeat("Later history\n\n", 30), Final: true})
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(900 * time.Millisecond)
	text, ok := ui.Find("Target needle")
	if !ok || text.Y < 95 || text.Y > 200 || ui.HasText("在会话中查找") || !ui.Focused("消息输入") || m.Drafts["session:s"] != "普通草稿保留" {
		t.Fatal("late history did not locate the actual match", text, v.findState().target, v.findState().geometry, v.findState().placement, v.historyRowBounds["target"])
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(200 * time.Millisecond)
	if !ui.Focused("消息输入") {
		t.Fatal("search close did not restore input")
	}
}

func TestConversationFindRevealsOnlyTheTargetToolGroup(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "tool-a", Kind: "tool", Text: "Target marker", Name: "First tool", Final: true}, {ID: "between", Kind: "assistant", Text: "First reply", Final: true}, {ID: "tool-b", Kind: "tool", Text: "Other output", Name: "Second tool", Final: true}, {ID: "end", Kind: "assistant", Text: "Final reply", Final: true}}
	m.Details["s"] = d
	m.FoldToolHistory["s"] = true
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Key(n.Cmd, n.KeyF)
	ui.Type("marker")
	ui.Advance(800 * time.Millisecond)
	if !ui.HasText("Target marker") || !ui.HasText("1/1") || !m.Collapsed["tool-group:s/tool-b"] {
		t.Fatal("find did not reveal just the matching tool group", ui.Texts())
	}
	m.FoldToolHistory["s"] = true
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("Target marker") {
		t.Fatal("late history hydration folded away the current search match")
	}
}

func TestConversationFindIsIndependentInParallelPanes(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "a", Kind: "assistant", Text: "Alpha answer", Final: true}}
	m.Details["s"] = d
	other := d
	other.Session.ID, other.Session.Title = "other", "Other session"
	other.Items = []domain.Item{{ID: "b", Kind: "assistant", Text: "Beta answer", Final: true}}
	m.Details["other"] = other
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, other.Session)
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1600, 900)
	ui.Key(n.Cmd, n.KeyF)
	ui.Type("Alpha")
	ui.Advance(400 * time.Millisecond)
	v.openParallel("other")
	ui.Frame()
	ui.Advance(350 * time.Millisecond)
	ui.Key(n.Cmd, n.KeyF)
	ui.Type("Beta")
	ui.Advance(400 * time.Millisecond)
	if v.finds["s"].query != "Alpha" || v.secondary.findState().query != "Beta" || v.finds["s"].target.id != "a" || v.secondary.findState().target.id != "b" {
		t.Fatal("find state crossed pane/session ownership")
	}
	v.closeParallel()
	ui.Frame()
	v.openParallel("other")
	ui.Frame()
	if v.secondary.findState().query != "Beta" {
		t.Fatal("closing the second pane discarded its query")
	}
}

func TestGlobalFindKeepsItsToolTargetVisibleThroughHistoryHydration(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "command", Kind: "tool", Name: "Run command", Text: "Target marker", Final: true}, {ID: "reply", Kind: "assistant", Text: "Done", Final: true}}
	m.Details["s"] = d
	m.FoldToolHistory["s"] = true
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Click("搜索")
	v.searchText, v.searchResultsQuery = "marker", "marker"
	v.searchResults = []domain.SearchResult{{SessionID: "s", WorkspaceID: "w", Title: "Layout", ItemID: "command", MatchStart: 7, MatchEnd: 13}}
	ui.Frame()
	if err := ui.Click("搜索结果：Layout，Example"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(600 * time.Millisecond)
	m.FoldToolHistory["s"] = true
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("Target marker") || ui.HasText("在会话中查找") {
		t.Fatal("history hydration hid the global search target or opened local find")
	}
	ui.Advance(3 * time.Second)
	m.FoldToolHistory["s"] = true
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if ui.HasText("Target marker") {
		t.Fatal("expired global search kept overriding normal history folding")
	}
}

func TestConversationFindKeepsOriginalUTF8OffsetsAfterLowercasing(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "unicode", Kind: "user", Text: "😀İx", Status: "sent", Final: true}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Key(n.Cmd, n.KeyF)
	ui.Type("ix")
	ui.Advance(600 * time.Millisecond)
	target := v.findState().target
	if !ui.HasText("1/1") || target.start != 4 || target.end != 7 {
		t.Fatal("case mapping changed the source byte offsets", target, ui.Texts())
	}
}

func TestPreviousFindAfterTheSelectedMessageDisappearsChoosesTheLastMatch(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "removed", Kind: "assistant", Text: "marker one", Final: true}, {ID: "earlier", Kind: "assistant", Text: "marker two", Final: true}, {ID: "last", Kind: "assistant", Text: "marker three", Final: true}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Key(n.Cmd, n.KeyF)
	ui.Type("marker")
	ui.Advance(500 * time.Millisecond)
	d.Items = d.Items[1:]
	m.Details["s"] = d
	ui.Frame()
	ui.Key(n.Shift, n.KeyEnter)
	ui.Advance(500 * time.Millisecond)
	if v.findState().target.id != "last" {
		t.Fatal("previous skipped the last surviving match", v.findState().target)
	}
}

func TestFindTrimsQueryConsistentlyForCountingAndLocating(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "space", Kind: "assistant", Text: "needle", Final: true}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Key(n.Cmd, n.KeyF)
	ui.Type(" needle ")
	ui.Advance(600 * time.Millisecond)
	if !ui.HasText("1/1") || v.findState().target.id != "space" || v.findState().placement != findPositioned {
		t.Fatal("counted a trimmed query without locating it", v.findState().target)
	}
}

func TestFindRevealsDeepCommandOutputInsideItsScrollport(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "command", Kind: "tool", Presentation: "command", Name: "Run command", Text: strings.Repeat("Earlier output\n", 80) + "Unique needle", Final: true}, {ID: "reply", Kind: "assistant", Text: "Done", Final: true}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	m.Collapsed[v.commandOutputKey("command", "")] = true
	ui.Key(n.Cmd, n.KeyF)
	ui.Type("needle")
	ui.Advance(1200 * time.Millisecond)
	if v.findState().placement != findPositioned || v.findState().geometry.H == 0 {
		t.Fatal("command output did not expose match geometry", v.findState().target)
	}
	count := 0
	im := ui.Image()
	for y := 95; y < 450; y++ {
		for x := 350; x < 1150; x++ {
			p := im.RGBAAt(x, y)
			if p.R > 180 && p.G > 140 && p.B < 150 {
				count++
			}
		}
	}
	if count < 30 {
		t.Fatal("matching output stayed clipped inside its inner scrollport")
	}
}

func TestConversationFindKeepsTheKeywordMarkedUntilClosed(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "reply", Kind: "assistant", Text: "先看 **旋转** 的含义。\n\n```go\n// 旋转只改变局部结构\n```\n\n再解释一次旋转。", Final: true}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "保留普通草稿"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Click("消息输入")
	ui.Key(n.Cmd, n.KeyF)
	if !ui.Focused("在会话中查找") {
		t.Fatal("Cmd+F did not focus the conversation find field")
	}
	ui.Type("旋转")
	ui.Advance(600 * time.Millisecond)
	if !ui.HasText("1/3") {
		t.Fatal("find did not count repeated matches across prose and code", v.findState(), ui.Texts())
	}
	yellow := func() int {
		im := ui.Image()
		count := 0
		for y := 85; y < 650; y++ {
			for x := 350; x < 1150; x++ {
				p := im.RGBAAt(x, y)
				if p.R > 180 && p.G > 140 && p.B < 150 {
					count++
				}
			}
		}
		return count
	}
	if yellow() < 30 {
		t.Fatal("selected keyword has no yellow mark after its entrance")
	}
	ui.Advance(time.Second)
	if yellow() < 30 {
		t.Fatal("keyword mark disappeared before its reading pause")
	}
	ui.Advance(10 * time.Second)
	if yellow() < 30 {
		t.Fatal("keyword mark disappeared while find remained open")
	}
	ui.Key(0, n.KeyEnter)
	ui.Advance(600 * time.Millisecond)
	if !ui.HasText("2/3") || yellow() < 30 {
		t.Fatal("next match did not navigate into the code block and mark it")
	}
	ui.Key(n.Shift, n.KeyEnter)
	ui.Advance(600 * time.Millisecond)
	if !ui.HasText("1/3") {
		t.Fatal("Shift+Enter did not find the previous match")
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(250 * time.Millisecond)
	if ui.HasText("在会话中查找") || yellow() != 0 || !ui.Focused("消息输入") || m.Drafts["session:s"] != "保留普通草稿" {
		t.Fatal("closing find lost focus or the ordinary draft")
	}
}

func TestGlobalFindTargetsNewSecondarySession(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	other := d
	other.Session.ID, other.Session.Title = "other", "Other"
	target := d
	target.Session.ID, target.Session.Title = "target", "Target session"
	target.Items = []domain.Item{{ID: "target-message", Kind: "assistant", Text: "needle", Final: true}}
	m.Details["other"], m.Details["target"] = other, target
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, other.Session, target.Session)
	v := New(m, nil, "")
	tester := n.NewTester(v.View, 1600, 900)
	v.openParallel("other")
	tester.Frame()
	tester.Click("搜索")
	v.searchText, v.searchResultsQuery = "needle", "needle"
	v.searchResults = []domain.SearchResult{{SessionID: "target", WorkspaceID: "w", Title: "Target session", ItemID: "target-message", MatchStart: 0, MatchEnd: 6}}
	tester.Frame()
	if err := tester.Click("搜索结果：Target session，Example"); err != nil {
		t.Fatal(err)
	}
	tester.Advance(800 * time.Millisecond)
	if m.SecondarySessionID != "target" || v.secondary.findState().target.id != "target-message" {
		t.Fatalf("secondary=%s secondaryfind=%+v rootfind=%+v", m.SecondarySessionID, v.secondary.findState().target, v.findState().target)
	}
}
