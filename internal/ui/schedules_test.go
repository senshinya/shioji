package ui

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestScheduleCatalogGroupsServiceUpdatesByWorkspace(t *testing.T) {
	for _, layout := range []struct {
		name  string
		width int
		dark  bool
	}{{"dark", 850, true}, {"light", 1280, false}} {
		t.Run(layout.name, func(t *testing.T) {
			m := layoutModel()
			m.LocalDeviceID, m.Snapshot.DeviceID = "local", "local"
			m.Snapshot.Workspaces[0].DeviceID = "local"
			m.DeviceStates["local"] = &app.DeviceState{Snapshot: m.Snapshot, Connected: true, ConnectionEpoch: "local", WorkerMinor: protocol.ScheduleMinor}
			v := New(m, nil, "")
			v.openSchedulePage()
			g := v.schedules
			g.sources["local"] = &scheduleSource{device: "local", epoch: "local", tasks: []domain.Schedule{}}
			for _, conn := range []app.DeviceConnection{
				{ID: "air", DeviceID: "mba", Name: "MBA at Home"},
				{ID: "mini", DeviceID: "mini", Name: "MacMini at Home"},
				{ID: "pro", DeviceID: "mbp", Name: "MBP at Work"},
			} {
				m.Connections = append(m.Connections, conn)
				workspace := domain.Workspace{ID: conn.ID + "-work", DeviceID: conn.DeviceID, Name: conn.Name + " 项目"}
				m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, workspace)
				m.DeviceStates[conn.ID] = &app.DeviceState{
					Snapshot: domain.Snapshot{DeviceID: conn.DeviceID, Workspaces: []domain.Workspace{workspace}}, Connected: true,
					ConnectionEpoch: conn.ID, WorkerMinor: protocol.ScheduleMinor - 1,
				}
			}
			ui := n.NewTester(v.View, layout.width, 760)
			ui.SetDark(layout.dark)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "schedule-service-"+layout.name+".png")
			const allDevices = "MBA at Home、MacMini at Home、MBP at Work"
			if !ui.HasText("需更新执行服务") || !ui.HasText(allDevices) || ui.HasText("还没有定时任务") {
				t.Fatal("service updates should share one notice and replace the empty state")
			}

			g.workspace = "w"
			ui.Frame()
			if ui.HasText("需更新执行服务") || !ui.HasText("此筛选下没有定时任务") {
				t.Fatal("supported workspace displayed notices for other devices")
			}

			g.workspace = "mini-work"
			ui.Frame()
			if ui.HasText(allDevices) || !ui.HasText("需更新执行服务") || ui.HasText("此筛选下没有定时任务") {
				t.Fatal("selected workspace did not scope the service notice to its device")
			}
			if err := ui.Click("查看设备"); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			if !v.devices || v.deviceSelected != "mini" || !ui.HasText("执行服务") {
				t.Fatal("the notice did not open the affected device's settings")
			}
		})
	}
}

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
			m.DeviceStates["local"] = &app.DeviceState{Snapshot: m.Snapshot, Connected: true, ConnectionEpoch: "test", WorkerMinor: protocol.ScheduleMinor, Readiness: m.Readiness}
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
			closeLabel := "关闭任务详情"
			if layout.width <= 1000 {
				closeLabel = "返回任务列表"
			}
			if top := find(closeLabel).Y; top != 24 {
				t.Errorf("schedule detail starts at y=%g, want y=24", top)
			}
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
			preview := slices.IndexFunc(labels, func(label string) bool { return strings.HasSuffix(label, " · Asia/Taipei") })
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

func TestNewScheduleEditorKeepsFormAlignedAndActionsVisible(t *testing.T) {
	for _, layout := range []struct {
		name          string
		width, height int
		dark          bool
	}{{"wide-light", 1440, 800, false}, {"compact-dark", 850, 600, true}} {
		t.Run(layout.name, func(t *testing.T) {
			m := layoutModel()
			m.Connections = []app.DeviceConnection{{ID: "remote", DeviceID: "mac", Name: "MBA at Home"}}
			m.Snapshot.DeviceID = "mac"
			m.Snapshot.Workspaces[0].DeviceID, m.Snapshot.Workspaces[0].Name = "mac", "MacNativeGames"
			ready := m.Readiness
			ready.Permissions = []string{"default", "read-only"}
			ready.Models = []domain.Model{{ID: "gpt-6.2-sol", Name: "GPT-6.2-Sol", Default: true, DefaultEffort: "low", Efforts: []string{"low", "medium"}}}
			m.DeviceStates["remote"] = &app.DeviceState{Snapshot: m.Snapshot, Connected: true, ConnectionEpoch: "remote", WorkerMinor: protocol.ScheduleMinor, Readiness: ready}
			v := New(m, nil, "")
			v.openSchedulePage()
			ui := n.NewTester(v.View, layout.width, layout.height)
			ui.SetDark(layout.dark)
			ui.Advance(time.Second)
			click := func(label string) {
				t.Helper()
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
			}
			find := func(label string) n.Rect {
				t.Helper()
				box, ok := ui.Find(label)
				if !ok {
					t.Fatalf("missing %q", label)
				}
				return box
			}
			click("新建任务")
			g := v.schedules
			g.draft.task.Zone = "Asia/Taipei"
			g.draft.git = domain.GitStatus{Applicable: true, Branches: []string{"main", "release"}}
			ui.Frame()
			saveNativeSkillImage(t, ui, "schedule-new-"+layout.name+".png")

			name, workspace, frequency := find("输入任务名称"), find("选择工作区"), find("频率")
			if name.X != workspace.X || name.X != frequency.X {
				t.Errorf("form controls have different leading edges: name=%+v workspace=%+v frequency=%+v", name, workspace, frequency)
			}
			if workspace.H != frequency.H {
				t.Errorf("workspace and its device should fit in a single selector row: workspace=%+v frequency=%+v", workspace, frequency)
			}
			form, save := find("定时任务设置"), find("保存任务")
			if name.X+name.W != form.X+form.W {
				t.Errorf("name must reach the trailing edge of the form: name=%+v form=%+v", name, form)
			}
			if form.W > 640 || save.Y < form.Y+form.H || save.Y+save.H > float32(layout.height) {
				t.Errorf("form should have a readable width and keep save below its scrollport: form=%+v save=%+v", form, save)
			}
			if run := find("运行配置"); save.Y-(run.Y+run.H) > 48 {
				t.Errorf("save action is too far from the editor: run=%+v save=%+v", run, save)
			}
			if next, trigger := find("运行预览"), find("触发时间"); next.X != trigger.X || next.Y <= trigger.Y {
				t.Errorf("next run does not align with its label column: next=%+v trigger=%+v", next, trigger)
			}
			ui.ClickAt(workspace.X+workspace.W-56, workspace.Y+workspace.H/2)
			ui.Advance(time.Second)
			if !ui.HasText("菜单选项") {
				t.Fatal("clicking the device name should open the workspace picker")
			}
			if menu, ok := ui.AccessByRole(n.RoleMenu, ""); !ok || menu.Bounds.W != workspace.W {
				t.Errorf("workspace picker and its options should share the same width: workspace=%+v menu=%+v", workspace, menu.Bounds)
			}
			saveNativeSkillImage(t, ui, "schedule-workspaces-"+layout.name+".png")
			ui.Key(0, n.KeyEscape)
			ui.Advance(time.Second)
			click("输入任务名称")
			ui.Type("工作日提交摘要")
			click("周六")
			click("每次运行的提示词")
			ui.Type("汇总今天的提交。\n列出待跟进事项。")
			click("运行配置")
			click("推理强度")
			click("medium")
			ui.Key(0, n.KeyEscape)
			ui.Advance(time.Second)
			click("频率")
			click("固定间隔")
			if g.draft.task.Name != "工作日提交摘要" || g.draft.task.Text != "汇总今天的提交。\n列出待跟进事项。" ||
				!slices.Contains(g.draft.task.Rule.Days, time.Saturday) || g.draft.task.Config.Effort != "medium" || g.draft.rule().Minutes != 60 {
				t.Fatalf("editor did not retain form values: %+v", g.draft)
			}
			if changed := find("保存任务"); changed.X != save.X || changed.Y+changed.H > float32(layout.height) {
				t.Fatal("save action lost its alignment or visibility when the form changed height")
			}
			click("取消")
			if g.draft != nil || !ui.HasText("新建任务") {
				t.Fatal("cancel did not return to the task list")
			}
		})
	}
}

func TestScheduleEditSharesFormEdges(t *testing.T) {
	for _, layout := range []struct {
		name          string
		width, height int
		dark          bool
	}{{"wide-light", 1440, 800, false}, {"compact-dark", 850, 600, true}} {
		t.Run(layout.name, func(t *testing.T) {
			m := layoutModel()
			m.Connections = []app.DeviceConnection{{ID: "remote", DeviceID: "mini", Name: "MacMini at Home"}}
			m.Snapshot.DeviceID = "mini"
			m.Snapshot.Workspaces[0].DeviceID, m.Snapshot.Workspaces[0].Name = "mini", "server"
			ready := m.Readiness
			ready.Permissions = []string{"YOLO"}
			ready.Models = []domain.Model{{ID: "gpt-6.2-sol", Name: "GPT-6.2-Sol", Default: true, DefaultEffort: "high", Efforts: []string{"high"}}}
			m.DeviceStates["remote"] = &app.DeviceState{Snapshot: m.Snapshot, Connected: true, ConnectionEpoch: "remote", WorkerMinor: protocol.ScheduleMinor, Readiness: ready}
			v := New(m, nil, "")
			v.openSchedulePage()
			v.editSchedule(v.schedules, domain.Schedule{
				ID: "daily", DeviceID: "mini", WorkspaceID: "w", Name: "每日巡检", Text: "巡检", Revision: 1, Enabled: true,
				Rule: domain.ScheduleRule{Kind: "daily", Time: "21:00"}, Zone: "Asia/Taipei",
				NextAt: time.Date(2026, time.November, 19, 13, 0, 0, 0, time.UTC),
				Config: domain.RunConfig{Permission: "YOLO", Model: "gpt-6.2-sol", Effort: "high"},
			})
			ui := n.NewTester(v.View, layout.width, layout.height)
			ui.SetDark(layout.dark)
			const scale = 2
			ui.SetScale(scale)
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, "schedule-edit-"+layout.name+".png")
			find := func(label string) n.Rect {
				t.Helper()
				box, ok := ui.Find(label)
				if !ok {
					t.Fatalf("missing %q", label)
				}
				return box
			}
			name, form := find("输入任务名称"), find("定时任务设置")
			saveButton, ok := ui.AccessByRole(n.RoleButton, "保存任务")
			if !ok {
				t.Fatal("missing save button")
			}
			save := saveButton.Bounds
			if name.X+name.W != form.X+form.W || math.Abs(float64((name.X+name.W)-(save.X+save.W))) > 0.01 {
				t.Errorf("name, form and save must share the trailing edge: name=%+v form=%+v save=%+v", name, form, save)
			}
			findInForm := func(label string) n.Rect {
				t.Helper()
				for _, item := range ui.AccessAll() {
					if item.Name == label && item.Bounds.X >= form.X && item.Bounds.Y >= form.Y && item.Bounds.Y < form.Y+form.H {
						return item.Bounds
					}
				}
				t.Fatalf("missing %q in the form", label)
				return n.Rect{}
			}
			workspace, device := findInForm("server"), findInForm("MacMini at Home")
			if workspace.X != name.X+configurationFieldInset || device.X+device.W != name.X+name.W-configurationFieldInset {
				t.Errorf("workspace and device must share the name field's text insets: name=%+v workspace=%+v device=%+v", name, workspace, device)
			}
			backButton, ok := ui.AccessByRole(n.RoleButton, "返回任务列表")
			if !ok {
				t.Fatal("missing back button")
			}
			back, frame := backButton.Bounds, ui.Image()
			background := frame.RGBAAt(int((form.X+form.W/2)*scale), int(back.Y*scale))
			leftInk := frame.Bounds().Max.X
			for y := int(back.Y * scale); y < int((back.Y+back.H)*scale); y++ {
				for x := int(back.X * scale); x < int((back.X+back.W)*scale); x++ {
					p := frame.RGBAAt(x, y)
					if absChannel(p.R, background.R) > 30 {
						leftInk = min(leftInk, x)
					}
				}
			}
			if offset := float64(leftInk)/scale - float64(form.X); math.Abs(offset) > 1 {
				t.Errorf("back arrow ink must align with the form's leading edge: offset=%g DIP", offset)
			}
		})
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
