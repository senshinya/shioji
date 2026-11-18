package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestScheduleNavigationPreservesConversationDraft(t *testing.T) {
	model := layoutModel()
	model.Drafts["session:s"] = "保留这条草稿"
	v := New(model, nil, "")
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)
	if err := ui.Click("定时任务"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("新建任务") || !ui.HasText("还没有定时任务") {
		t.Fatal("native schedule catalog did not open")
	}
	if err := ui.Click("Layout"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if model.SessionID != "s" || model.Drafts["session:s"] != "保留这条草稿" || ui.HasText("新建任务") {
		t.Fatal("schedule navigation lost the original conversation")
	}
}

func TestScheduleCatalogOnlyUsesAuthorizedDevices(t *testing.T) {
	m := layoutModel()
	m.LocalDeviceID = "a"
	m.Snapshot.Workspaces[0].DeviceID = "a"
	m.Connections = []app.DeviceConnection{{ID: "remote", DeviceID: "b"}}
	m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: "remote-workspace", DeviceID: "b"}, domain.Workspace{ID: "other-workspace", DeviceID: "c"})
	v := New(m, nil, "")
	v.openSchedulePage()
	g := v.schedules
	g.sources["local"] = &scheduleSource{device: "a", tasks: []domain.Schedule{{ID: "same", DeviceID: "a", WorkspaceID: "w"}}}
	g.sources["remote"] = &scheduleSource{device: "b", tasks: []domain.Schedule{{ID: "same", DeviceID: "b", WorkspaceID: "remote-workspace"}}}
	g.sources["unconnected"] = &scheduleSource{device: "c", tasks: []domain.Schedule{{ID: "other", DeviceID: "c", WorkspaceID: "other-workspace"}}}
	rows := v.scheduleRows(g)
	if len(rows) != 2 || rows[0].DeviceID != "a" || rows[1].DeviceID != "b" {
		t.Fatal("offline cache lost authorized tasks or exposed another device", rows)
	}
	if selected, ok := g.task(scheduleKey{device: "b", id: "same"}); !ok || selected.WorkspaceID != "remote-workspace" {
		t.Fatal("same task ID on another device selected the wrong task", selected)
	}
	m.Connections = nil
	if rows = v.scheduleRows(g); len(rows) != 1 || rows[0].DeviceID != "a" {
		t.Fatal("removed device stayed in schedules", rows)
	}
}

func TestScheduleNativeLayoutsAndRunLinks(t *testing.T) {
	for _, layout := range []struct {
		width int
		dark  bool
		name  string
	}{{1280, false, "wide"}, {940, true, "narrow"}} {
		t.Run(layout.name, func(t *testing.T) {
			m := layoutModel()
			m.LocalDeviceID = "a"
			m.Snapshot.Workspaces[0].DeviceID = "a"
			m.DeviceStates["local"] = &app.DeviceState{Snapshot: m.Snapshot, Connected: true, ConnectionEpoch: "test", Readiness: m.Readiness}
			m.DeviceStates["local"].Snapshot.DeviceID = "a"
			zone := "Asia/Taipei"
			now := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
			v := New(m, nil, "")
			v.openSchedulePage()
			g := v.schedules
			task := domain.Schedule{ID: "daily", DeviceID: "a", WorkspaceID: "w", Name: "工作日提交摘要", Zone: zone, Enabled: true, NextAt: now.Add(24 * time.Hour), Revision: 1,
				Rule: domain.ScheduleRule{Kind: "weekly", Time: "09:00", Days: []time.Weekday{1, 2, 3, 4, 5}}, Config: domain.RunConfig{Model: "gpt-5.4", Permission: "read-only"}}
			failed := task
			failed.ID, failed.Name = "failure", "站点链接巡检"
			failed.Latest = &domain.ScheduleRun{State: domain.ScheduleFailed}
			g.sources["local"] = &scheduleSource{device: "a", epoch: "test", tasks: []domain.Schedule{task, failed}}
			ui := n.NewTester(v.View, layout.width, 800)
			ui.SetDark(layout.dark)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "schedules-"+layout.name+"-list.png")
			find := func(label string) n.Rect {
				t.Helper()
				box, ok := ui.Find(label)
				if !ok {
					t.Fatalf("missing %s", label)
				}
				return box
			}
			inkX := func(box n.Rect) (int, int) {
				t.Helper()
				frame := ui.Image()
				background := frame.RGBAAt(layout.width-2, 12)
				left, right := frame.Bounds().Max.X, -1
				for y := int(box.Y); y < int(box.Y+box.H); y++ {
					for x := int(box.X); x < int(box.X+box.W); x++ {
						pixel := frame.RGBAAt(x, y)
						if max(absChannel(pixel.R, background.R), absChannel(pixel.G, background.G), absChannel(pixel.B, background.B)) > 30 {
							left, right = min(left, x), max(right, x)
						}
					}
				}
				if right < left {
					t.Fatalf("missing visible ink in %+v", box)
				}
				return left, right + 1
			}
			_, newEdge := inkX(find("新建任务"))
			_, countEdge := inkX(find("2 个任务"))
			if abs(newEdge-countEdge) > 1 {
				t.Errorf("new task and task count have different right edges: %d, %d", newEdge, countEdge)
			}
			if err := ui.Click(task.Name); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			g.detail = domain.ScheduleDetail{Schedule: task, Runs: []domain.ScheduleRun{
				{ID: "live", State: domain.ScheduleCompleted, At: now, SessionID: "s", SessionLink: "open"},
				{ID: "deleted", State: domain.ScheduleCompleted, At: now.Add(-24 * time.Hour), SessionID: "deleted", SessionLink: "deleted"},
			}}
			ui.Frame()
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "schedules-"+layout.name+"-detail.png")
			if !ui.HasText("运行历史") || !ui.HasText("会话已删除") || ui.HasText("新建任务") != (layout.width > 1000) {
				t.Fatalf("detail layout or deleted-session treatment is incorrect: history=%t deleted=%t catalog=%t selected=%+v", ui.HasText("运行历史"), ui.HasText("会话已删除"), ui.HasText("新建任务"), g.selected)
			}
			link := find("打开会话 · 11 月 18 日 09:00")
			_, arrowEdge := inkX(n.Rect{X: link.X + link.W - 12, Y: link.Y + 12, W: 12, H: link.H - 24})
			_, deleteEdge := inkX(find("删除任务"))
			if abs(deleteEdge-arrowEdge) > 1 {
				t.Errorf("delete icon and history arrow have different right edges: %d, %d", deleteEdge, arrowEdge)
			}
			if err := ui.Click("打开会话 · 11 月 18 日 09:00"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if v.schedulePageOpen() || m.SessionID != "s" || g.session != "s" {
				t.Fatal("run didn't open its conversation")
			}
			if err := ui.Click("返回定时任务"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !v.schedulePageOpen() || !ui.HasText("运行历史") {
				t.Fatal("could not return to the source run")
			}
			if err := ui.Click("编辑任务"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "schedules-"+layout.name+"-editor.png")
			if !ui.HasText("工作区") || !ui.HasText("保存任务") {
				t.Fatal("native editor did not open")
			}
			frequency := find("频率")
			frequency.X, frequency.Y = frequency.X+4, frequency.Y+4
			frequency.W, frequency.H = frequency.W-24, frequency.H-8
			frequencyEdge, _ := inkX(frequency)
			labels := ui.Texts()
			preview := slices.IndexFunc(labels, func(label string) bool { return strings.HasPrefix(label, "接下来 ") })
			if preview < 0 {
				t.Fatal("missing next-run preview")
			}
			previewEdge, _ := inkX(find(labels[preview]))
			if abs(frequencyEdge-previewEdge) > 1 {
				t.Errorf("frequency and next-run preview have different left edges: %d, %d", frequencyEdge, previewEdge)
			}
		})
	}
}

func TestScheduleEditorDraftDoesNotAffectInspectorOrAttention(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.openSchedulePage()
	g := v.schedules
	g.detail.Schedule.Rule.Days = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday}
	v.editSchedule(g, g.detail.Schedule)
	g.draft.task.Rule.Days = slices.DeleteFunc(g.draft.task.Rule.Days, func(day time.Weekday) bool { return day == time.Tuesday })
	if !slices.Equal(g.detail.Schedule.Rule.Days, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday}) {
		t.Fatal("draft altered the worker's accepted schedule")
	}
	v.Model.Preferences.Notifications.Foreground = false
	if v.sessionVisible("s") || v.foregroundNotificationSuppressed("s", true) {
		t.Fatal("hidden conversation would mark a completion as read")
	}
	v.editWorkspace(v.Model.Snapshot.Workspaces[0])
	if v.schedulePageOpen() || v.workspaceEditor == nil {
		t.Fatal("workspace settings was obscured by the schedule page")
	}
	v.openSchedulePage()
	v.openParallel("other")
	if v.schedulePageOpen() || v.Model.SecondarySessionID != "other" {
		t.Fatal("parallel conversation was obscured by the schedule page")
	}
}

func TestScheduleDetailSlidesInAndOut(t *testing.T) {
	for _, layout := range []struct {
		name  string
		width int
		close string
	}{
		{"wide", 1280, "关闭任务详情"},
		{"compact", 940, "返回任务列表"},
	} {
		t.Run(layout.name, func(t *testing.T) {
			for _, reduced := range []bool{false, true} {
				m := layoutModel()
				m.LocalDeviceID = "a"
				m.Snapshot.Workspaces[0].DeviceID = "a"
				v := New(m, nil, "")
				v.openSchedulePage()
				task := domain.Schedule{ID: "motion", DeviceID: "a", WorkspaceID: "w", Name: "动画任务"}
				v.schedules.sources["local"] = &scheduleSource{device: "a", tasks: []domain.Schedule{task}}
				ui := n.NewTester(v.View, layout.width, 800)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				ui.Advance(time.Second)
				catalog, _ := ui.Find("新建任务")
				if err := ui.Click(task.Name); err != nil {
					t.Fatal(err)
				}
				ui.Advance(65 * time.Millisecond)
				moving, shown := ui.Find("任务详情与运行历史")
				if !reduced {
					saveNativeSkillImage(t, ui, "schedule-motion-"+layout.name+"-opening.png")
				}
				ui.Advance(time.Second)
				open, settled := ui.Find("任务详情与运行历史")
				if !shown || !settled || (!reduced && moving.X <= open.X+1) || (reduced && moving != open) {
					t.Errorf("detail did not slide in from the right: reduced=%t mid=%+v open=%+v shown=%t settled=%t", reduced, moving, open, shown, settled)
				}
				if err := ui.Click(layout.close); err != nil {
					t.Fatal(err)
				}
				ui.Advance(65 * time.Millisecond)
				closing, returned := ui.Find("新建任务")
				if !reduced {
					saveNativeSkillImage(t, ui, "schedule-motion-"+layout.name+"-closing.png")
				}
				if _, active := ui.Find("任务详情与运行历史"); active {
					t.Fatal("exiting detail still has live controls")
				}
				ui.Advance(time.Second)
				closed, closedOK := ui.Find("新建任务")
				if !returned || !closedOK || closed != catalog || (!reduced && closing.X >= closed.X-1) || (reduced && closing != closed) {
					t.Errorf("catalog did not return as the detail slid out: reduced=%t mid=%+v closed=%+v original=%+v returned=%t settled=%t", reduced, closing, closed, catalog, returned, closedOK)
				}
			}
		})
	}
}
