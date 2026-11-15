package ui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestHistoryRoundCountCanBeConfiguredInConversationSettings(t *testing.T) {
	v := New(layoutModel(), nil, "")
	s := NewSettings(v)
	ui := n.NewTester(s.View, 968, 756)
	ui.Advance(time.Second)
	if v.Model.Preferences.HistoryPageRounds != 20 {
		t.Fatal("initial history count", v.Model.Preferences.HistoryPageRounds)
	}
	if err := ui.Click("设置每次加载轮次"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("50 轮"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.Model.Preferences.HistoryPageRounds != 50 || !ui.HasText("50 轮") {
		t.Fatal("history round selector did not update preference", v.Model.Preferences.HistoryPageRounds, ui.Texts())
	}
	saveNativeSkillImage(t, ui, "history-round-settings.png")
	ui.Click("搜索设置")
	ui.Type("轮次")
	ui.Advance(time.Second)
	if !ui.HasText("每次加载轮次") {
		t.Fatal("history round setting is missing from search", ui.Texts())
	}
}

func TestHistoryPageRequestsUseTheConfiguredRoundCount(t *testing.T) {
	v := toolTailFixture(2)
	v.Controller = app.NewController("")
	v.Model.WorkerMinor = 37
	v.Model.Preferences.HistoryPageRounds = 50
	d := v.Model.Details["s"]
	d.History = &domain.HistoryWindow{First: 100, Last: 101, Older: true}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	v.requestHistory(domain.HistoryQuery{Before: 100}, false)
	j := historyJob(t, v, "session.history")
	q := j.Input.(domain.HistoryQuery)
	if q.Rounds != 50 || q.Limit != 0 || q.Before != 100 {
		t.Fatal("history page ignored configured round count", q)
	}
}
