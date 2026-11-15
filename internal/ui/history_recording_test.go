package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

// Records production native components with deterministic owning-worker replies.
func TestPagedHistoryNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_HISTORY_RECORDING")
	if dir == "" {
		t.Skip("opt-in native history recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	m := layoutModel()
	m.WorkerMinor = 37
	m.Preferences.CollapseLatestTools = true
	d := m.Details["s"]
	d.Session.Title = "长会话 · 按需加载历史"
	m.Snapshot.Sessions[0] = d.Session
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	d.Items = nil
	d.History = &domain.HistoryWindow{Invalidated: true}
	m.Details["s"] = d
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	group := domain.HistoryToolGroup{ID: "tool-7800", TurnID: "t", First: 7800, Last: 7999, Count: 200}
	page := func(first, last int64) domain.SessionDetail {
		p := d
		p.History = &domain.HistoryWindow{First: first, Last: last, Older: true, Newer: last < 7999, ContextID: "question-4", Groups: []domain.HistoryToolGroup{group}}
		p.Items = []domain.Item{{ID: "question-4", Order: 7700, Kind: "user", Text: "继续验证长会话：默认只加载最新内容，向上滚动时再补载。", Final: true}}
		for i := first; i <= last; i++ {
			p.Items = append(p.Items, domain.Item{ID: fmt.Sprint("tool-", i), Order: i, TurnID: "t", Kind: "tool", Name: fmt.Sprintf("go test ./internal/history -run TestScenario%d", i), Text: "PASS\nok  shioji/internal/history", Final: true})
		}
		return p
	}
	old := d
	old.History = &domain.HistoryWindow{First: 1, Last: 100, Newer: true}
	old.Items = []domain.Item{{ID: "question-1", Order: 1, Kind: "user", Text: "早期方案：怎样减少远程长会话的加载时间？", Final: true}, {ID: "answer-1", Order: 2, Kind: "assistant", Text: "先读取最新的一页历史，保留当前提问。\n\n左侧仍保留完整提问索引；跳转旧提问时直接读取目标附近的内容。\n\n向上滚动再加载更早消息，并保持正在阅读的消息位置。", Final: true}}
	reply := func(method string, out any) {
		t.Helper()
		j := historyJob(t, v, method)
		b, _ := json.Marshal(out)
		j.Apply(m, b, nil)
		ui.Frame()
	}
	index := domain.HistoryOutline{SessionID: "s", Prompts: []domain.HistoryPrompt{{ID: "question-1", Order: 1, Text: "早期方案：怎样减少远程长会话的加载时间？"}, {ID: "question-2", Order: 2001, Text: "保留会话中的完整提问索引"}, {ID: "question-3", Order: 5001, Text: "验证查找和远程连接"}, {ID: "question-4", Order: 7700, Text: "继续验证长会话"}}}
	for frame := 0; frame < 450; frame++ {
		switch frame {
		case 30:
			reply("session.history", page(7900, 7999))
		case 40:
			reply("session.outline", index)
		case 75:
			if e := ui.Click("200 次工具调用"); e != nil {
				t.Fatal(e)
			}
		case 105:
			if e := ui.Click("200 次工具调用"); e != nil {
				t.Fatal(e)
			}
		case 135:
			ui.Wheel(800, 300, 0, -1)
		case 165:
			reply("session.history", page(7800, 7899))
		case 195:
			if e := ui.Click("跳转到消息 1"); e != nil {
				t.Fatal(e)
			}
		case 225:
			reply("session.history", old)
		case 270:
			if e := ui.Click("回到最新"); e != nil {
				t.Fatal(e)
			}
		case 300:
			reply("session.history", page(7900, 7999))
		case 330:
			v.openConversationFind()
			ui.Frame()
			ui.Click("在会话中查找")
			ui.Type("早期方案")
		case 360:
			reply("session.matches", domain.HistoryMatches{SessionID: "s", Query: "早期方案", Matches: []domain.HistoryMatch{{ID: "question-1", Order: 1, Count: 1, Start: 0, End: 12}}})
		case 390:
			reply("session.history", old)
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
}
