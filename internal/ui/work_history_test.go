package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"

	"shioji/internal/app"
)

func TestChildHistoryRequestsConfiguredRounds(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	v.Model.WorkerMinor = 41
	v.Model.Preferences.HistoryPageRounds = 5
	v.Controller = app.NewController("")
	if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	j := historyJob(t, v, "session.work.history")
	b, _ := json.Marshal(j.Input)
	var q struct {
		Rounds        int
		ID, SessionID string
	}
	json.Unmarshal(b, &q)
	if q.Rounds != 5 || q.ID != "agent-a" || q.SessionID != "s" {
		t.Fatalf("child detail bypasses round history: %s", b)
	}
}

func childHistoryPage(v *View, first, last int) domain.WorkDetail {
	d := domain.WorkDetail{SessionID: "s", ThreadID: v.Model.Details["s"].Session.ThreadID, Work: v.Model.Details["s"].Works[0], Sequence: 1000, History: &domain.HistoryWindow{First: int64(first * 10), Last: int64(last*10 + 1), WindowFirst: int64(first * 10), WindowLast: int64(last*10 + 1), Older: first > 1, Newer: last < 100}}
	for i := first; i <= last; i++ {
		d.Items = append(d.Items, domain.WorkItem{Item: domain.Item{ID: fmt.Sprint("u", i), Kind: "user", Order: int64(i * 10), Text: fmt.Sprintf("子 Agent 第 %d 轮检查", i), Final: true}, Revision: 1000}, domain.WorkItem{Item: domain.Item{ID: fmt.Sprint("a", i), Kind: "assistant", Order: int64(i*10 + 1), Text: fmt.Sprintf("第 %d 轮结果：已检查边界与消息顺序。", i), Final: true}, Revision: 1000})
	}
	return d
}

func pagedChildFixture(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	v, ui := sessionWorkFixture(t)
	v.Model.WorkerMinor = 41
	v.Controller = app.NewController("")
	v.Model.Sequence = 1000
	if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	j := historyJob(t, v, "session.work.history")
	body, _ := json.Marshal(childHistoryPage(v, 81, 100))
	j.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	return v, ui
}

func TestChildHistoryPrependPreservesNativeReadingAnchorAndLiveOutput(t *testing.T) {
	for _, absoluteTop := range []bool{false, true} {
		t.Run(fmt.Sprint(absoluteTop), func(t *testing.T) {
			v, ui := pagedChildFixture(t)
			u := v.workPanel("agents").detail("agent-a")
			row := 0
			if !absoluteTop {
				row = 3
			}
			u.list.ScrollTo(row, n.Start)
			ui.Frame()
			ui.Advance(time.Second)
			marker := "子 Agent 第 81 轮检查"
			if !absoluteTop {
				marker = "子 Agent 第 82 轮检查"
			}
			before, ok := ui.Find(marker)
			if !ok {
				t.Fatal("missing anchor", ui.Texts())
			}
			port, ok := ui.Find("子 Agent内容")
			if !ok {
				t.Fatal("missing child scrollport", ui.Texts())
			}
			ui.Scroll(port.X+port.W/2, port.Y+port.H/2, 0, -1)
			ui.Frame()
			j := historyJob(t, v, "session.work.history")
			q := j.Input.(domain.WorkHistoryQuery)
			if q.Before != 810 || q.Rounds != 20 {
				t.Fatal(q)
			}
			// Update the newest record while the earlier snapshot is still in flight.
			if err := v.Model.ApplyEvent(domain.Event{Sequence: 1001, SessionID: "s", Type: "work.item", WorkID: "agent-a", WorkItem: &domain.Item{ID: "a100", Order: 1001, Kind: "assistant", Text: "实时更新保留", Final: true}}); err != nil {
				t.Fatal(err)
			}
			// Measure immediately before the prepend so wheel movement is excluded.
			ui.Frame()
			before, _ = ui.Find(marker)
			body, _ := json.Marshal(childHistoryPage(v, 61, 80))
			j.Apply(v.Model, body, nil)
			ui.Frame()
			ui.Advance(time.Second)
			after, ok := ui.Find(marker)
			if !ok || math.Abs(float64(after.Y-before.Y)) > 1 {
				t.Fatal("prepend moved native reading anchor", before, after, ok)
			}
			d := v.Model.WorkDetails[v.Model.WorkKey("s", "agent-a")]
			if len(d.Items) != 80 || d.Items[79].Item.Text != "实时更新保留" {
				t.Fatal("lost older history or live output", len(d.Items))
			}
			if u.streams["a61"] != nil && u.streams["a61"].live() {
				t.Fatal("older page replays arrival animation")
			}
			for len(v.Controller.Jobs) > 0 {
				if j := <-v.Controller.Jobs; j.Method == "session.work.history" {
					t.Fatal("one gesture drained a second page")
				}
			}
		})
	}
}

func TestChildHistoryWaitsForScrollAndCancelsLatePageOnBack(t *testing.T) {
	v, ui := pagedChildFixture(t)
	u := v.workPanel("agents").detail("agent-a")
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.work.history" {
			t.Fatal("no scroll automatically loaded older history")
		}
	}
	u.list.ScrollTo(1, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	port, _ := ui.Find("子 Agent内容")
	ui.Scroll(port.X+20, port.Y+40, 0, -1)
	ui.Frame()
	j := historyJob(t, v, "session.work.history")
	if err := ui.Click("返回子 Agent列表"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if j.Context.Err() == nil {
		t.Fatal("back did not cancel snapshot read")
	}
	body, _ := json.Marshal(childHistoryPage(v, 61, 80))
	j.Apply(v.Model, body, nil)
	ui.Frame()
	if got := v.Model.WorkDetails[v.Model.WorkKey("s", "agent-a")]; len(got.Items) != 40 {
		t.Fatal("late page changed closed detail", len(got.Items))
	}
}

func TestChildHistoryOlderFailureRetriesAtReadingEdge(t *testing.T) {
	v, ui := pagedChildFixture(t)
	u := v.workPanel("agents").detail("agent-a")
	u.list.ScrollTo(0, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	port, _ := ui.Find("子 Agent内容")
	ui.Scroll(port.X+20, port.Y+40, 0, -1)
	ui.Frame()
	j := historyJob(t, v, "session.work.history")
	j.Apply(v.Model, nil, fmt.Errorf("fixture connection failure"))
	ui.Frame()
	if !ui.HasText("fixture connection failure") {
		t.Fatal("older failure is hidden below latest messages", ui.Texts())
	}
	if err := ui.Click("重试"); err != nil {
		t.Fatal(err)
	}
	retry := historyJob(t, v, "session.work.history")
	if retry.Input.(domain.WorkHistoryQuery).Before != 810 {
		t.Fatal("retry lost older cursor", retry.Input)
	}
}

func TestChildHistoryNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_CHILD_HISTORY_RECORDING")
	if dir == "" {
		t.Skip("opt-in native child history recording")
	}
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name))
			v, ui := pagedChildFixture(t)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			u := v.workPanel("agents").detail("agent-a")
			var pending *app.Job
			reply := func(first, last int) {
				t.Helper()
				if pending == nil {
					t.Fatal("missing recorded page")
				}
				b, _ := json.Marshal(childHistoryPage(v, first, last))
				pending.Apply(v.Model, b, nil)
				pending = nil
				ui.Frame()
			}
			scroll := func() {
				t.Helper()
				r, ok := ui.Find("子 Agent内容")
				if !ok {
					t.Fatal("missing child pane")
				}
				ui.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, -20000)
			}
			for frame := 0; frame < 300; frame++ {
				switch frame {
				case 25:
					scroll()
				case 55:
					reply(61, 80)
				case 85:
					u.list.ScrollTo(0, n.Start)
					ui.Frame()
					scroll()
				case 105:
					if pending == nil {
						t.Fatal("missing failing page")
					}
					pending.Apply(v.Model, nil, fmt.Errorf("连接暂时中断，请重试"))
					pending = nil
				case 120:
					u.list.ScrollTo(0, n.Start)
				case 130:
					if err := ui.Click("重试"); err != nil {
						t.Fatalf("%v; phase=%v error=%q cursor=%d", err, u.phase, u.error, u.history.before)
					}
				case 150:
					reply(41, 60)
				case 175:
					if err := ui.Click("跟随最新输出"); err != nil {
						t.Fatal(err)
					}
				case 200:
					item := domain.Item{ID: "live-last", Kind: "assistant", Order: 1010, Text: "新的检查结果已收到，历史补载不会覆盖实时输出。", Final: true}
					if err := v.Model.ApplyEvent(domain.Event{Sequence: 1001, SessionID: "s", Type: "work.item", WorkID: "agent-a", WorkItem: &item}); err != nil {
						t.Fatal(err)
					}
				case 225:
					work := v.Model.Details["s"].Works[0]
					work.State = "completed"
					work.Revision = 1002
					if err := v.Model.ApplyEvent(domain.Event{Sequence: 1002, SessionID: "s", Type: "work.upsert", Work: &work}); err != nil {
						t.Fatal(err)
					}
				case 245:
					if err := ui.Click("返回子 Agent列表"); err != nil {
						t.Fatal(err)
					}
				case 265:
					if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
						t.Fatal(err)
					}
				case 280:
					reply(81, 100)
				}
				ui.Frame()
				ui.Advance(time.Second / 30)
				for len(v.Controller.Jobs) > 0 {
					j := <-v.Controller.Jobs
					if j.Method == "session.work.history" {
						if pending != nil {
							t.Fatal("overlapping recorded reads")
						}
						pending = &j
					}
				}
				if frame == 65 {
					saveNativeSkillImage(t, ui, "after.png")
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
		})
	}
}

func TestChildHistoryInitialSnapshotKeepsMoreThan64ConcurrentRecords(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	v.Model.WorkerMinor = 41
	v.Model.Sequence = 1000
	v.Controller = app.NewController("")
	delete(v.Model.WorkDetails, v.Model.WorkKey("s", "agent-a"))
	if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	j := historyJob(t, v, "session.work.history")
	for i := 1; i <= 90; i++ {
		item := domain.Item{ID: fmt.Sprint("during-read-", i), Kind: "assistant", Order: int64(1001 + i), Text: "在快照读取期间收到的输出", Final: true}
		if err := v.Model.ApplyEvent(domain.Event{Sequence: int64(1000 + i), SessionID: "s", Type: "work.item", WorkID: "agent-a", WorkItem: &item}); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := json.Marshal(childHistoryPage(v, 81, 100))
	j.Apply(v.Model, body, nil)
	ui.Frame()
	d := v.Model.WorkDetails[v.Model.WorkKey("s", "agent-a")]
	if len(d.Items) != 130 || d.History.Last != 1091 {
		t.Fatal("initial snapshot lost concurrent records to legacy output cap", len(d.Items))
	}
}

func TestChildHistorySecondaryReadCancelsWhenPaneCloses(t *testing.T) {
	m := parallelModel()
	m.WorkerMinor = 41
	m.SecondarySessionID = "other"
	m.FocusSecondary = true
	m.Inspectors["other"] = true
	d := m.Details["other"]
	d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
	d.Works = []domain.WorkActivity{{ID: "child", Kind: "agent", TurnID: "t", Title: "secondary child", State: "running", Revision: 1}}
	m.Details["other"] = d
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1700, 900)
	ui.Advance(time.Second)
	if err := ui.Click("查看子 Agent：secondary child"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	j := historyJob(t, v, "session.work.history")
	if j.Context.Err() != nil {
		t.Fatal("visible secondary read was cancelled")
	}
	pane := v.secondary
	key := pane.Model.WorkKey("other", "child")
	u := pane.workPanel("agents").detail("child")
	v.closeParallel()
	ui.Frame()
	if j.Context.Err() == nil {
		t.Fatal("closing secondary pane did not cancel its history snapshot")
	}
	if u.streams != nil || u.history != nil || v.Model.WorkDetails[key].History != nil {
		t.Fatal("hidden secondary retained history state")
	}
}

func TestChildHistoryClosingInspectorReleasesBrowsedCache(t *testing.T) {
	v, ui := pagedChildFixture(t)
	u := v.workPanel("agents").detail("agent-a")
	d := childHistoryPage(v, 1, 100)
	v.Model.MergeWorkDetail(d)
	ui.Frame()
	v.Model.Inspectors["s"] = false
	ui.Frame()
	got := v.Model.WorkDetails[v.Model.WorkKey("s", "agent-a")]
	if got.History != nil || len(got.Items) > 64 || u.history != nil || u.streams != nil {
		t.Fatal("closing inspector kept paged child history", len(got.Items))
	}
}
