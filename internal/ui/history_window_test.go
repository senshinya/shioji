package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestPagedHistoryShowsCompleteToolCountAndKeepsManualExpansion(t *testing.T) {
	v := toolTailFixture(2)
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.Items[1].Order = 100
	d.Items[2].Order = 101
	d.History = &domain.HistoryWindow{First: 100, Last: 101, Older: true, ContextID: d.Items[0].ID, Groups: []domain.HistoryToolGroup{{ID: "original-call", TurnID: "t", First: 2, Last: 101, Count: 100}}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if !ui.HasText("100 次工具调用") {
		t.Fatal("partial window only counted loaded calls", ui.Texts())
	}
	if e := ui.Click("执行工具"); e != nil {
		t.Fatal(e)
	}
	ui.Advance(time.Second)
	if e := ui.Click("100 次工具调用"); e != nil {
		t.Fatal(e)
	}
	ui.Advance(time.Second)
	d.Items = append(d.Items[:1], append([]domain.Item{{ID: "original-call", TurnID: "t", Kind: "tool", Order: 2, Name: "earlier command", Final: true}}, d.Items[1:]...)...)
	v.Model.Details["s"] = d
	v.historyBaseline = true
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("earlier command") || v.Model.ToolDisclosure("tool-group:s/original-call").Closed() {
		t.Fatal("paging changed the stable group identity or manual expansion")
	}

}

func TestPagedHistoryLoadsOlderAndOutlineWithoutFullHydration(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 100, Last: 100, Older: true}
	d.Items = []domain.Item{{ID: "latest", Kind: "assistant", Text: "latest body", Order: 100, Final: true}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if e := ui.Click("加载更早消息"); e != nil {
		t.Fatal(e)
	}
	var seenOutline, seenPage bool
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		switch j.Method {
		case "session.outline":
			seenOutline = true
			b, _ := json.Marshal(domain.HistoryOutline{SessionID: "s", Prompts: []domain.HistoryPrompt{{ID: "old-question", Order: 1, Text: "older question"}}})
			j.Apply(v.Model, b, nil)
		case "session.history":
			seenPage = true
			q, ok := j.Input.(domain.HistoryQuery)
			if !ok || q.Before != 100 || q.Rounds != 20 || q.Limit != 0 {
				t.Fatal("incorrect older page request", j.Input)
			}
			page := d
			page.Items = []domain.Item{{ID: "old-question", Kind: "user", Order: 1, Text: "older question", Final: true}}
			page.History = &domain.HistoryWindow{First: 1, Last: 99, Newer: true}
			b, _ := json.Marshal(page)
			j.Apply(v.Model, b, nil)
		}
	}
	ui.Frame()
	ui.Advance(time.Second)
	if !seenOutline || !seenPage || !ui.HasText("latest body") || !ui.HasText("older question") {
		t.Fatal("paging did not preserve tail and add older content", seenOutline, seenPage, ui.Texts())
	}
}

func TestPagedHistoryDoesNotDrainHiddenPagesWithoutAScrollGesture(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 100, Last: 100, Older: true, Newer: true}
	d.Items = []domain.Item{{ID: "middle", Kind: "assistant", Text: "middle window", Order: 100, Final: true}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.history" {
			t.Fatal("a short or folded window automatically drained unloaded history")
		}
	}
	ui.Wheel(900, 350, 0, -1)
	ui.Advance(20 * time.Millisecond)
	ui.Advance(time.Second)
	seen := false
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.history" {
			q := j.Input.(domain.HistoryQuery)
			seen = q.Before == 100
		}
	}
	if !seen {
		t.Fatal("scrolling above a short window did not request older history")
	}
}

func historyJob(t *testing.T, v *View, method string) app.Job {
	t.Helper()
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == method {
			return j
		}
	}
	t.Fatal("missing history job", method)
	return app.Job{}
}

func TestPagedHistoryOutlineJumpsDirectlyAndReturnsToLatest(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 800, Last: 800, Older: true}
	d.Items = []domain.Item{{ID: "latest", Kind: "user", Text: "latest question", Order: 800, Final: true}}
	v.Model.Details["s"] = d
	v.Model.HistoryOutlines["s"] = domain.HistoryOutline{SessionID: "s", Prompts: []domain.HistoryPrompt{{ID: "old", Order: 1, Text: "first question"}, {ID: "u2", Order: 200, Text: "second question"}, {ID: "u3", Order: 400, Text: "third question"}, {ID: "latest", Order: 800, Text: "latest question"}}}
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if e := ui.Click("跳转到消息 1"); e != nil {
		t.Fatal(e)
	}
	j := historyJob(t, v, "session.history")
	q := j.Input.(domain.HistoryQuery)
	if q.AroundID != "old" || q.Before != 0 || q.After != 0 || q.Rounds != 20 || q.Limit != 0 {
		t.Fatal("outline scanned the intervening history", q)
	}
	old := d
	old.History = &domain.HistoryWindow{First: 1, Last: 2, Newer: true}
	old.Items = []domain.Item{{ID: "old", Kind: "user", Text: "first question", Order: 1, Final: true}, {ID: "old-answer", Kind: "assistant", Text: "earlier answer", Order: 2, Final: true}}
	b, _ := json.Marshal(old)
	j.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("first question") || !ui.HasText("earlier answer") {
		t.Fatal("target window was not revealed", ui.Texts())
	}
	if e := ui.Click("回到最新"); e != nil {
		t.Fatal(e)
	}
	j = historyJob(t, v, "session.history")
	q = j.Input.(domain.HistoryQuery)
	if q.AroundID != "" || q.Before != 0 || q.After != 0 {
		t.Fatal("latest navigation paged through all missing history", q)
	}
	b, _ = json.Marshal(d)
	j.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("latest question") || ui.HasText("earlier answer") {
		t.Fatal("return to latest did not replace the old window")
	}
}

func TestPagedHistorySearchResolvesOldBodiesAndRepeatedMatches(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 800, Last: 800, Older: true}
	d.Items = []domain.Item{{ID: "latest", Kind: "assistant", Text: "latest answer", Order: 800, Final: true}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	v.openConversationFind()
	ui.Frame()
	ui.Advance(time.Second)
	if e := ui.Click("在会话中查找"); e != nil {
		t.Fatal(e)
	}
	ui.Type("needle")
	ui.Advance(time.Second)
	j := historyJob(t, v, "session.matches")
	b, _ := json.Marshal(domain.HistoryMatches{SessionID: "s", Query: "needle", Matches: []domain.HistoryMatch{{ID: "old", Order: 1, Count: 2, Start: 0, End: 6}}})
	j.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	j = historyJob(t, v, "session.history")
	if q := j.Input.(domain.HistoryQuery); q.AroundID != "old" {
		t.Fatal("find did not load only the matching window", q)
	}
	old := d
	old.History = &domain.HistoryWindow{First: 1, Last: 1, Newer: true}
	old.Items = []domain.Item{{ID: "old", Kind: "assistant", Order: 1, Text: "needle needle", Final: true}}
	b, _ = json.Marshal(old)
	j.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	s := v.findState()
	if s.total != 2 || s.target.id != "old" || s.target.start != 0 || s.target.end != 6 {
		t.Fatal("remote match did not resolve to native text", s.total, s.target)
	}
	if e := ui.Click("下一个匹配"); e != nil {
		t.Fatal(e)
	}
	ui.Advance(time.Second)
	if s.target.start != 7 || s.target.end != 13 {
		t.Fatal("second occurrence lost its original text offset", s.target)
	}
}

func TestPagedHistoryCanceledReplyCannotRepopulateTheSession(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 100, Last: 100, Older: true}
	d.Items = []domain.Item{{ID: "latest", Kind: "assistant", Text: "latest body", Order: 100, Final: true}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("加载更早消息")
	j := historyJob(t, v, "session.history")
	v.home()
	old := d
	old.History = &domain.HistoryWindow{First: 1, Last: 99}
	old.Items = []domain.Item{{ID: "abandoned", Kind: "user", Order: 1, Text: "abandoned body"}}
	b, _ := json.Marshal(old)
	j.Apply(v.Model, b, nil)
	if len(v.Model.Details["s"].Items) != 1 || v.Model.Details["s"].Items[0].ID != "latest" {
		t.Fatal("late canceled history replaced the current cache")
	}
}

func TestHistoryOutlineRetainsPromptArrivingBeforeItsReply(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 100, Last: 100, Older: true}
	d.Items = []domain.Item{{ID: "latest", Kind: "assistant", Order: 100, Text: "latest"}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	j := historyJob(t, v, "session.outline")
	if e := v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "new-question", Kind: "user", Order: 101, Text: "just arrived"}}); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(domain.HistoryOutline{SessionID: "s", Prompts: []domain.HistoryPrompt{{ID: "old-question", Order: 1, Text: "older"}}})
	j.Apply(v.Model, b, nil)
	if len(v.Model.HistoryOutlines["s"].Prompts) != 2 {
		t.Fatal("outline reply lost a newer prompt", v.Model.HistoryOutlines["s"])
	}
}

func TestHistoryCallbacksUseTheOwningDeviceEpoch(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	v.Model.ConnectionEpoch = "device-b"
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 100, Last: 100, Older: true}
	d.Items = []domain.Item{{ID: "latest", Kind: "assistant", Order: 100, Text: "latest"}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	key := v.Model.ConnectionID()
	v.Model.DeviceStates[key] = &app.DeviceState{ConnectionEpoch: "device-b"}
	v.Model.ConnectionEpoch = "device-a" // callback receives root, whose primary device differs
	j := historyJob(t, v, "session.outline")
	b, _ := json.Marshal(domain.HistoryOutline{SessionID: "s", Prompts: []domain.HistoryPrompt{{ID: "old", Order: 1, Text: "old question"}}})
	j.Apply(v.Model, b, nil)
	if len(v.Model.HistoryOutlines["s"].Prompts) != 1 {
		t.Fatal("owning-device outline callback was discarded")
	}
}

func TestPagedHistoryRefreshesOffWindowToolOutcomesWithoutLoadingBodies(t *testing.T) {
	v := toolTailFixture(2)
	v.Controller = app.NewController("")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.Items[1].Order = 100
	d.Items[2].Order = 101
	d.History = &domain.HistoryWindow{First: 100, Last: 101, Older: true, Groups: []domain.HistoryToolGroup{{ID: "original", TurnID: "t", First: 2, Last: 101, Count: 100}}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "off-window", Kind: "tool", TurnID: "t", Order: 20, Final: true, Status: "failed"}})
	ui.Frame()
	ui.Advance(time.Second)
	j := historyJob(t, v, "session.history")
	q := j.Input.(domain.HistoryQuery)
	if !q.SummaryOnly || q.AroundID != "original" {
		t.Fatal("off-window update hydrated bodies", q)
	}
	page := domain.SessionDetail{Session: d.Session, Sequence: 1, History: &domain.HistoryWindow{Groups: []domain.HistoryToolGroup{{ID: "original", TurnID: "t", First: 2, Last: 101, Count: 100, Issues: 1}}}}
	b, _ := json.Marshal(page)
	j.Apply(v.Model, b, nil)
	if got := v.Model.Details["s"]; len(got.Items) != len(d.Items) || got.History.Groups[0].Issues != 1 {
		t.Fatal("off-window completion did not update only metadata", got.History)
	}
}

func TestPrependingHistoryKeepsTheVisibleMessageStationary(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 100, Last: 119, Older: true}
	d.Items = nil
	for i := 100; i < 120; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Order: int64(i), Kind: "user", Text: fmt.Sprint("Question ", i), Final: true})
	}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	v.lists["s"].ScrollToRow(10, 160, n.Animation{Duration: time.Millisecond})
	ui.Frame()
	ui.Advance(time.Second)
	before, ok := ui.Find("Question 109")
	if !ok {
		t.Fatal("anchor not visible", ui.Texts())
	}
	v.requestHistory(domain.HistoryQuery{Before: 100}, false)
	j := historyJob(t, v, "session.history")
	page := d
	page.History = &domain.HistoryWindow{First: 80, Last: 99, Older: true, Newer: true}
	page.Items = nil
	for i := 80; i < 100; i++ {
		page.Items = append(page.Items, domain.Item{ID: fmt.Sprint(i), Order: int64(i), Kind: "user", Text: fmt.Sprint("Question ", i), Final: true})
	}
	b, _ := json.Marshal(page)
	j.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	after, ok := ui.Find("Question 109")
	if !ok || math.Abs(float64(after.Y-before.Y)) > 1 {
		t.Fatal("prepend moved the reader", before, after, ok)
	}
}

func TestDelayedHistorySummaryCannotOverwriteALaterLoadedToolCompletion(t *testing.T) {
	v := toolTailFixture(2)
	v.Controller = app.NewController("")
	v.Model.WorkerMinor = 37
	d := v.Model.Details["s"]
	d.Items[1].Order = 100
	d.Items[2].Order = 101
	d.History = &domain.HistoryWindow{First: 100, Last: 101, Older: true, Groups: []domain.HistoryToolGroup{{ID: "original", TurnID: "t", First: 2, Last: 101, Count: 100}}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "old", Kind: "tool", TurnID: "t", Order: 20, Final: true, Status: "failed"}})
	ui.Frame()
	ui.Advance(time.Second)
	j := historyJob(t, v, "session.history")
	loaded := d.Items[2]
	loaded.Final = true
	loaded.Status = "failed"
	v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "item.upsert", SessionID: "s", Item: &loaded})
	page := domain.SessionDetail{Session: d.Session, Sequence: 1, History: &domain.HistoryWindow{Groups: []domain.HistoryToolGroup{{ID: "original", TurnID: "t", First: 2, Last: 101, Count: 100, Issues: 1}}}}
	b, _ := json.Marshal(page)
	j.Apply(v.Model, b, nil)
	if v.Model.Details["s"].History.SummaryDirty != 2 {
		t.Fatal("stale summary was accepted without refreshing after a newer completion")
	}
	ui.Frame()
	ui.Advance(time.Second)
	j = historyJob(t, v, "session.history")
	page.Sequence = 2
	page.History.Groups[0].Issues = 2
	b, _ = json.Marshal(page)
	j.Apply(v.Model, b, nil)
	if v.Model.Details["s"].History.Groups[0].Issues != 2 {
		t.Fatal("metadata refresh lost a concurrent loaded completion")
	}
}
