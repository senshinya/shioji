package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestUnknownSessionOffersRecoveryAndKeepsDraftAndReadingPosition(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 7
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Revision = 8
	d.Session.Recovery = &domain.SessionRecovery{Phase: "attention"}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	m.Drafts["session:s"] = "不要丢掉这份草稿"
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if _, ok := ui.Find("恢复会话"); ok {
		t.Fatal("automatic recovery still requires an initial manual check")
	}
	if err := ui.Click("重试恢复"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(v.Controller.Jobs) > 0 {
		if (<-v.Controller.Jobs).Method == "session.reconcile" {
			found = true
		}
	}
	if !found {
		t.Fatal("check did not go to owning worker")
	}
	if m.Drafts["session:s"] != "不要丢掉这份草稿" || m.SessionID != "s" {
		t.Fatal("recovery changed draft or navigation")
	}
}

func TestAutomaticRecoveryAlwaysUsesCompactProgressWithoutRetryActions(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 8
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "checking", Message: "上次未能读取历史"}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if !ui.HasText("正在恢复会话…") || ui.HasText("重试恢复") || ui.HasText("查看处理方法") {
		t.Fatal("automatic recovery asks the user to act while it is checking")
	}
}

func TestRecoveryShowsOneNextStepAndExplainsUnownedTools(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 8
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "attention", CanEnd: true, Tasks: []domain.RecoveryTask{{ID: "tool", Command: "sleep 60", State: "unknown"}}}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if !ui.HasText("暂时无法继续") || ui.HasText("手动结束旧轮") || ui.HasText("sleep 60") {
		t.Fatal("recovery exposes technical decisions before a clear next step")
	}
	ui.Click("查看处理方法")
	ui.Advance(400 * time.Millisecond)
	if !ui.HasText("sleep 60") || ui.HasText("停止任务并恢复") {
		t.Fatal("unknown process ownership is hidden or presented as stoppable")
	}
	if err := ui.Click("高级处理"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(400 * time.Millisecond)
	if !ui.HasText("我已在设备上停止这些任务") {
		t.Fatal("manual fallback has no concrete prerequisite")
	}
}

func TestRecoveryRequiresSelectingActualProcessesBeforeStopping(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 8
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "attention", CanEnd: true, CanStopTasks: true, Tasks: []domain.RecoveryTask{{ID: "tool@123:1:0", ItemID: "tool", PID: 123, Command: "sleep 60", ProcessCommand: "/bin/sleep 60", State: "running"}}}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("查看处理方法")
	ui.Advance(400 * time.Millisecond)
	if err := ui.Click("选择进程 123"); err != nil {
		t.Fatal("no explicit process selection", err)
	}
	ui.Click("停止所选进程")
	ui.Advance(400 * time.Millisecond)
	ui.Click("确认停止所选进程")
	found := false
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.reconcile" {
			in := j.Input.(protocol.RecoveryInput)
			found = in.Action == "stop-tasks" && len(in.Targets) == 1 && in.Targets[0] == "tool@123:1:0"
		}
	}
	if !found {
		t.Fatal("stop did not carry the explicitly selected process identity")
	}
}

func TestRecoveryDetailsAnimateWithoutMovingComposerOrLosingFocus(t *testing.T) {
	for _, width := range []int{1440, 860} {
		m := layoutModel()
		m.WorkerMinor = 8
		d := m.Details["s"]
		d.Session.Status = "unknown"
		d.Session.Recovery = &domain.SessionRecovery{Phase: "attention", CanEnd: true, Tasks: []domain.RecoveryTask{{ID: "old", Command: strings.Repeat("long command ", 12), State: "unknown"}}}
		m.Details["s"] = d
		m.Snapshot.Sessions[0] = d.Session
		v := New(m, nil, "")
		ui := n.NewTester(v.View, width, 900)
		ui.Advance(time.Second)
		ui.Click("消息输入")
		before, _ := ui.Find("会话底部面板")
		inputBefore, _ := ui.Find("消息输入")
		v.recoveries[m.ConnectionID()+"/s"].Details = true
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		middle, _ := ui.Find("会话底部面板")
		ui.Advance(400 * time.Millisecond)
		expanded, _ := ui.Find("会话底部面板")
		inputAfter, _ := ui.Find("消息输入")
		if !(before.H < middle.H && middle.H < expanded.H) {
			t.Fatal("recovery details height jumps", width, before, middle, expanded)
		}
		if math.Abs(float64(inputBefore.Y-inputAfter.Y)) > 1 {
			t.Fatal("details displaced independent composer", inputBefore, inputAfter)
		}
		if _, ok := ui.TextCaret(); !ok {
			t.Fatal("details stole composer focus")
		}
		v.recoveries[m.ConnectionID()+"/s"].Details = false
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		closing, _ := ui.Find("会话底部面板")
		if !(closing.H < expanded.H && closing.H > before.H) {
			t.Fatal("details close jumps", closing)
		}
	}
}
func TestRecoveryCardReservesItsAnimatedHeightWithoutMovingInputFocus(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		m := layoutModel()
		m.WorkerMinor = 7
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		ui.Advance(time.Second)
		ui.Click("消息输入")
		before, _ := ui.Find("会话底部面板")
		d := m.Details["s"]
		d.Session.Status = "unknown"
		d.Session.Recovery = &domain.SessionRecovery{Phase: "attention"}
		m.Details["s"] = d
		m.Snapshot.Sessions[0] = d.Session
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		middle, _ := ui.Find("会话底部面板")
		ui.Advance(400 * time.Millisecond)
		after, _ := ui.Find("会话底部面板")
		if after.H <= before.H || !reduced && !(middle.H > before.H && middle.H < after.H) {
			t.Fatalf("recovery height jumps: %+v %+v %+v", before, middle, after)
		}
		if _, ok := ui.TextCaret(); !ok {
			t.Fatal("recovery stole composer focus")
		}
		if m.Drafts["session:s"] != "" {
			t.Fatal("recovery inserted text")
		}
		d.Session.Status = "completed"
		m.Details["s"] = d
		m.Snapshot.Sessions[0] = d.Session
		ui.Frame()
		ui.Advance(70 * time.Millisecond)
		closing, _ := ui.Find("会话底部面板")
		if !reduced && !(closing.H > before.H && closing.H < after.H) {
			t.Fatal("recovery card disappears before height animates", closing)
		}
	}
}
func TestRecoveryContinuationCreatesOnlyADraftAndPreservesBothDraftsAndQueue(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 7
	d := m.Details["s"]
	d.Session.Status = "abandoned"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready"}
	d.Items = append(d.Items, domain.Item{ID: "last", Kind: "user", Text: "修复红黑树旋转"})
	d.Queue = domain.QueueState{SessionID: "s", Paused: true, Items: []domain.QueueItem{{ID: "waiting"}}}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	m.Drafts["home"] = "原先的首页草稿"
	m.Drafts["session:s"] = "还没发送的补充"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if _, ok := ui.Find("更多恢复操作"); ok {
		ui.Click("更多恢复操作")
		ui.Advance(300 * time.Millisecond)
	}
	if err := ui.Click("在新会话接续"); err != nil {
		t.Fatal(err)
	}
	if m.SessionID != "" || len(m.Snapshot.Sessions) != 1 || len(m.Pending) != 0 {
		t.Fatal("continuation created or sent a session before submission")
	}
	if m.Drafts["session:s"] != "还没发送的补充" || len(m.Details["s"].Queue.Items) != 1 {
		t.Fatal("continuation moved old draft or queue")
	}
	for _, s := range []string{"修复红黑树旋转", "原先的首页草稿", "还没发送的补充"} {
		if !strings.Contains(m.Drafts["home"], s) {
			t.Fatalf("continuation lost %s", s)
		}
	}
}

func TestRecoveryContinuationFromSecondaryPaneOpensTheSharedHomeDraft(t *testing.T) {
	m := parallelModel()
	m.WorkerMinor = 7
	m.SecondarySessionID = "other"
	m.FocusSecondary = true
	d := m.Details["other"]
	d.Session.Status = "abandoned"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready"}
	m.Details["other"] = d
	m.Snapshot.Sessions[1] = d.Session
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1700, 900)
	ui.Advance(time.Second)
	if _, ok := ui.Find("更多恢复操作"); ok {
		ui.Click("更多恢复操作")
		ui.Advance(300 * time.Millisecond)
	}
	if err := ui.Click("在新会话接续"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if m.SessionID != "" || m.FocusSecondary || !strings.Contains(m.Drafts["home"], "Independent response") || m.Drafts["session:s"] != "left draft" || m.Drafts["session:other"] != "right draft" {
		t.Fatalf("secondary handoff: session=%q focus=%v home=%q left=%q right=%q error=%s", m.SessionID, m.FocusSecondary, m.Drafts["home"], m.Drafts["session:s"], m.Drafts["session:other"], m.Error)
	}
}

func TestRecoveryReceiptUpdatesOnlyItsOwningDevicesReadiness(t *testing.T) {
	m := layoutModel()
	m.LocalDeviceID = "A"
	m.Connections = []app.DeviceConnection{{ID: "air", DeviceID: "B"}}
	local := domain.Snapshot{DeviceID: "A", Workspaces: []domain.Workspace{{ID: "wa", DeviceID: "A"}}, Sessions: []domain.Session{{ID: "sa", WorkspaceID: "wa", Status: "completed"}}}
	remote := domain.Snapshot{DeviceID: "B", Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B"}}, Sessions: []domain.Session{{ID: "sb", WorkspaceID: "wb", Status: "unknown"}}}
	m.DeviceStates = map[string]*app.DeviceState{"local": {Connected: true, WorkerMinor: 7, Snapshot: local, Readiness: domain.Readiness{State: "ready", Models: []domain.Model{{ID: "local-model"}}}}, "air": {Connected: true, WorkerMinor: 7, Snapshot: remote, Readiness: domain.Readiness{State: "unavailable"}}}
	m.Snapshot.Workspaces = append(local.Workspaces, remote.Workspaces...)
	m.Snapshot.Sessions = append(local.Sessions, remote.Sessions...)
	m.WorkspaceID = "wb"
	m.SessionID = "sb"
	m.RefreshDevices()
	v := New(m, app.NewController(""), "")
	state := &recoveryState{}
	v.reconcileSession(remote.Sessions[0], "check", state)
	job := <-v.Controller.Jobs
	m.WorkspaceID = "wa"
	m.SessionID = "sa"
	m.RefreshDevices()
	m.Error = "local error stays"
	body, _ := json.Marshal(protocol.RecoveryResult{SessionID: "sb", Status: "completed", Readiness: domain.Readiness{State: "ready", Models: []domain.Model{{ID: "remote-model"}}}})
	job.Apply(m, body, nil)
	m.RefreshDevices()
	if m.DeviceStates["air"].Readiness.State != "ready" || m.Readiness.Models[0].ID != "local-model" || m.Error != "local error stays" {
		t.Fatal("recovery receipt crossed device readiness/error boundary")
	}
}

func TestContinueOriginalSessionDoesNotConsumeComposerDraftOrResumeQueue(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 7
	d := m.Details["s"]
	d.Session.Status = "abandoned"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready", CanContinue: true}
	d.Session.Context = domain.LaunchContext{CWD: "/original-worktree"}
	d.Queue = domain.QueueState{SessionID: "s", Paused: true, Items: []domain.QueueItem{{ID: "waiting"}}}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	m.Drafts["session:s"] = domain.ContinuationPrompt
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if err := ui.Click("继续原会话"); err != nil {
		t.Fatal(err)
	}
	if v.sent == nil || v.sent.source.W < 500 {
		t.Fatal("continuation squeezed message text into its action button", v.sent)
	}
	var input domain.Submission
	submits := 0
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "session.submit" {
			input = job.Input.(domain.Submission)
			submits++
		}
		if job.Method == "queue.continue" {
			t.Fatal("continuing session released queue")
		}
	}
	if submits != 1 || !input.Continue || input.SessionID != "s" || input.Text != domain.ContinuationPrompt || len(input.Attachments) != 0 {
		t.Fatal("continuation did not target original session", input)
	}
	m.ApplyOperation(domain.Operation{ID: input.OperationID, SessionID: "s", State: "confirmed"})
	if m.Drafts["session:s"] != domain.ContinuationPrompt || !m.Details["s"].Queue.Paused {
		t.Fatal("continuation consumed ordinary draft or resumed queue")
	}
}

func TestAutomaticCheckUsesSmallDelayedStatusAndDoesNotFlashACard(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 7
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("消息输入")
	before, _ := ui.Find("会话底部面板")
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "checking"}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	short, _ := ui.Find("会话底部面板")
	if short.H != before.H || ui.HasText("执行状态待核对") {
		t.Fatal("fast background recovery flashes a card", before, short)
	}
	ui.Advance(500 * time.Millisecond)
	after, _ := ui.Find("会话底部面板")
	if after.H-before.H > 40 || !ui.HasText("正在恢复会话…") {
		t.Fatal("background recovery is not a compact status", before, after)
	}
	if _, ok := ui.TextCaret(); !ok {
		t.Fatal("background check stole composer focus")
	}
}

func TestRecoveryStatusCardAndReadyRowShareContinuousHeight(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 7
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "checking"}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("消息输入")
	compact, _ := ui.Find("会话底部面板")
	d.Session.Recovery = &domain.SessionRecovery{Phase: "attention", Message: strings.Repeat("待确认的旧执行。", 12), CanEnd: true}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	middle, _ := ui.Find("会话底部面板")
	ui.Advance(400 * time.Millisecond)
	expanded, _ := ui.Find("会话底部面板")
	if !(compact.H < middle.H && middle.H < expanded.H) {
		t.Fatal("status-to-card height jumps", compact, middle, expanded)
	}
	d.Session.Status = "abandoned"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready", CanContinue: true}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	ui.Frame() // Sample at the exact clock time before reversing the transition.
	closing, _ := ui.Find("会话底部面板")
	if !(closing.H < expanded.H && closing.H > compact.H) {
		t.Fatal("card-to-ready height jumps", compact, closing, expanded)
	}
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "attention", Message: "旧工具尚待确认", CanEnd: true}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	ui.Frame()
	reversed, _ := ui.Find("会话底部面板")
	if math.Abs(float64(reversed.H-closing.H)) > 2 {
		t.Fatal("reversing recovery transition jumps", closing, reversed)
	}
	ui.Advance(500 * time.Millisecond)
	if _, ok := ui.TextCaret(); !ok {
		t.Fatal("recovery presentation changed input focus")
	}
}

func TestContinueFromSecondaryPaneUsesItsOwnThreadAndKeepsBothDrafts(t *testing.T) {
	m := parallelModel()
	m.WorkerMinor = 7
	m.SecondarySessionID = "other"
	m.FocusSecondary = true
	d := m.Details["other"]
	d.Session.Status = "abandoned"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready", CanContinue: true}
	m.Details["other"] = d
	m.Snapshot.Sessions[1] = d.Session
	controller := app.NewController("")
	v := New(m, controller, "")
	ui := n.NewTester(v.View, 1700, 900)
	ui.Advance(time.Second)
	if err := ui.Click("继续原会话"); err != nil {
		t.Fatal(err)
	}
	var input domain.Submission
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "session.submit" {
			input = job.Input.(domain.Submission)
		}
	}
	if input.SessionID != "other" || !input.Continue || m.Drafts["session:s"] != "left draft" || m.Drafts["session:other"] != "right draft" {
		t.Fatal("secondary continuation crossed thread/draft boundary", input)
	}
}

func TestRecoveredFailedAndInterruptedSessionsCanPrepareNewSessionDrafts(t *testing.T) {
	for _, status := range []string{"failed", "interrupted"} {
		m := layoutModel()
		m.WorkerMinor = 7
		d := m.Details["s"]
		d.Session.Status = status
		d.Session.Recovery = &domain.SessionRecovery{Phase: "ready", CanContinue: true}
		m.Details["s"] = d
		m.Snapshot.Sessions[0] = d.Session
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1440, 900)
		ui.Advance(time.Second)
		if _, ok := ui.Find("更多恢复操作"); ok {
			ui.Click("更多恢复操作")
			ui.Advance(300 * time.Millisecond)
		}
		if err := ui.Click("在新会话接续"); err != nil {
			t.Fatal(err)
		}
		if m.SessionID != "" || !strings.Contains(m.Drafts["home"], "Aligned reply") || strings.Contains(m.Drafts["home"], "已手动结束") {
			t.Fatal("known terminal recovery cannot hand off or misstates its outcome", status, m.Error)
		}
	}
}
func TestUnavailableOriginalThreadCanBeCheckedAgainWithoutReconnecting(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 7
	d := m.Details["s"]
	d.Session.Status = "abandoned"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready"}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if err := ui.Click("重试恢复"); err != nil {
		t.Fatal("temporarily unavailable source has no retry", err)
	}
}

func TestRecoveredSessionHasOnePrimaryActionAndSecondaryMenu(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 8
	d := m.Details["s"]
	d.Session.Status = "interrupted"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready", CanContinue: true}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if ui.HasText("可继续原会话") || ui.HasText("在新会话接续") {
		t.Fatal("recovery still has duplicate copy and equal-weight competing actions")
	}
	if !ui.HasText("上次回复已中断") {
		t.Fatal("recovery does not explain why continuation is available")
	}
	if err := ui.Click("更多恢复操作"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("在新会话接续") {
		t.Fatal("secondary action was removed instead of disclosed")
	}
}
