package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestRoundHistoryNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_ROUND_HISTORY_RECORDING")
	if dir == "" {
		t.Skip("opt-in native round history recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	m := layoutModel()
	m.Preferences.Appearance = "light"
	m.WorkerMinor = 37
	v := New(m, app.NewController(""), "")
	settings := NewSettings(v)
	ui := n.NewTester(settings.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	click := func(label string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	for frame := 0; frame < 120; frame++ {
		if frame == 30 {
			click("设置每次加载轮次")
		}
		if frame == 60 {
			click("10 轮")
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
	if m.Preferences.HistoryPageRounds != 10 {
		t.Fatal("recording did not select 10 rounds")
	}
	d := m.Details["s"]
	d.Session.Title, d.Session.ThreadID, d.Session.Status = "按轮次加载会话", "round-source", domain.ExecutionCompleted
	d.Turns = []domain.Turn{{ID: "same-turn", Order: 1, Status: domain.ExecutionCompleted}}
	d.Items = nil
	d.History = &domain.HistoryWindow{Invalidated: true}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	text := func(round int) string {
		if round == 18 {
			return "第 18 轮 · 执行中插入的消息，也单独算一轮"
		}
		return fmt.Sprintf("第 %d 轮 · 检查会话分页与执行结果", round)
	}
	page := func(first, last int) domain.SessionDetail {
		out := d
		out.History = &domain.HistoryWindow{First: int64(first * 1000), Last: int64(last*1000 + 500), WindowFirst: int64(first * 1000), WindowLast: int64(last*1000 + 500), Older: first > 1, Newer: last < 30}
		for round := first; round <= last; round++ {
			base := int64(round * 1000)
			out.Items = append(out.Items, domain.Item{ID: fmt.Sprint("u", round), Order: base, Kind: "user", TurnID: "same-turn", Text: text(round), Final: true})
			if round == 24 {
				for i := 0; i < 130; i++ {
					out.Items = append(out.Items, domain.Item{ID: fmt.Sprint("round24-tool", i), Order: base + int64(i+1), Kind: "tool", TurnID: "same-turn", Name: fmt.Sprintf("go test ./internal/history -run Case%03d", i), Text: "PASS", Final: true, Status: "completed"})
				}
				out.History.Groups = []domain.HistoryToolGroup{{ID: "round24-tool0", TurnID: "same-turn", First: base + 1, Last: base + 130, Count: 130}}
			}
			out.Items = append(out.Items, domain.Item{ID: fmt.Sprint("a", round), Order: base + 500, Kind: "assistant", TurnID: "same-turn", Text: fmt.Sprintf("第 %d 轮的结果已确认。\n\n按照用户消息划分轮次，工具调用不占用轮次数量。", round), Final: true})
		}
		return out
	}
	outline := domain.HistoryOutline{SessionID: "s", ThreadID: d.Session.ThreadID}
	for i := 1; i <= 30; i++ {
		outline.Prompts = append(outline.Prompts, domain.HistoryPrompt{ID: fmt.Sprint("u", i), Order: int64(i * 1000), TurnID: "same-turn", Text: text(i)})
	}
	ui = n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	reply := func(method string, out any) {
		t.Helper()
		job := historyJob(t, v, method)
		b, _ := json.Marshal(out)
		job.Apply(m, b, nil)
		ui.Frame()
	}
	for frame := 0; frame < 390; frame++ {
		switch frame {
		case 15:
			job := historyJob(t, v, "session.history")
			if q := job.Input.(domain.HistoryQuery); q.Rounds != 10 {
				t.Fatal("recorded page ignored settings", q)
			}
			b, _ := json.Marshal(page(21, 30))
			job.Apply(m, b, nil)
			ui.Frame()
		case 25:
			reply("session.outline", outline)
		case 70:
			click("跳转到消息 24")
		case 100:
			click("130 次工具调用")
		case 130:
			click("130 次工具调用")
		case 160:
			click("回到最新")
		case 210:
			ui.Scroll(1000, 300, 0, -20000)
		case 240:
			if v.historyPage.read == nil {
				ui.Scroll(1000, 300, 0, -1)
				ui.Advance(time.Second / 30)
			}
			reply("session.history", page(11, 20))
		case 300:
			click("跳转到消息 18")
		case 330:
			click("回到最新")
		}
		ui.Frame()
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", 120+frame))
	}
}
