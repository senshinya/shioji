package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/tailnet"
)

func feedbackJob(t *testing.T, v *View, method string) app.Job {
	t.Helper()
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == method {
			return job
		}
	}
	t.Fatalf("missing %s job", method)
	return app.Job{}
}

func feedbackClick(t *testing.T, ui *n.Tester, label string) {
	t.Helper()
	if err := ui.Click(label); err != nil {
		t.Fatal(err)
	}
}

func TestSteerShowsOneMovingBubbleAcrossWorkerUpdates(t *testing.T) {
	for _, delay := range []time.Duration{0, 150 * time.Millisecond, 600 * time.Millisecond, 2 * time.Second} {
		for _, eventFirst := range []bool{true, false} {
			v, ui := nativeQueueFixture()
			feedbackClick(t, ui, "消息输入")
			ui.Type("追加这条指令")
			ui.Key(n.Cmd, n.KeyEnter)
			job := feedbackJob(t, v, "turn.steer")
			in := job.Input.(protocol.SteerInput).Input
			ui.Frame()
			label := "用户消息气泡"
			start, ok := ui.Find(label)
			if !ok {
				t.Fatalf("no transcript bubble before confirmation, delay=%v eventFirst=%v", delay, eventFirst)
			}
			ui.Advance(delay)
			before, _ := ui.Find(label)
			d := v.Model.Details["s"]
			item := domain.Item{ID: "input:" + in.OperationID, TurnID: "active", Kind: "user", Text: in.Text, Status: "sent", Final: true}
			op := domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationConfirmed}
			body, _ := json.Marshal(op)
			if eventFirst {
				d.Items = append(d.Items, item)
				v.Model.Details["s"] = d
			} else {
				job.Apply(v.Model, body, nil)
			}
			ui.Frame()
			first, visible := ui.Find(label)
			if !visible || first != before {
				t.Fatalf("first update jumped or hid the bubble (%v %v): %v → %v", delay, eventFirst, before, first)
			}
			ui.Type("下一条草稿")
			if eventFirst {
				job.Apply(v.Model, body, nil)
			} else {
				d = v.Model.Details["s"]
				d.Items = append(d.Items, item)
				v.Model.Details["s"] = d
			}
			ui.Frame()
			ui.Advance(time.Second)
			end, visible := ui.Find(label)
			if !visible || start.Y <= end.Y || v.Model.Drafts["session:s"] != "下一条草稿" || !ui.Focused("消息输入") {
				t.Fatalf("flight or draft failed (%v %v): %v → %v, draft=%q", delay, eventFirst, start, end, v.Model.Drafts["session:s"])
			}
		}
	}
}

func TestSteerKeepsTheRunningFooterWhileWaitingInReducedMotion(t *testing.T) {
	for _, eventFirst := range []bool{false, true} {
		v, ui := nativeQueueFixture()
		detail := v.Model.Details["s"]
		detail.Items[0].TurnID = "active"
		detail.Items[0].Final = true
		v.Model.Details["s"] = detail
		ui.SetPreferences(n.Preferences{ReduceMotion: true})
		ui.Frame()
		progress := v.runningProgress
		before := v.historyRowBounds["turn-footer:active"]
		if progress == nil || !ui.HasText("进行中") {
			t.Fatal("fixture did not show the active turn")
		}
		feedbackClick(t, ui, "消息输入")
		ui.Type("追加这条指令")
		ui.Key(n.Cmd, n.KeyEnter)
		job := feedbackJob(t, v, "turn.steer")
		in := job.Input.(protocol.SteerInput).Input
		ui.Frame()
		running, visible := ui.Find("进行中")
		footer, hasFooter := ui.Find("回复信息")
		if !visible || !hasFooter || ui.HasText("正在发送") || running.Y < footer.Y || running.Y+running.H > footer.Y+footer.H ||
			v.historyRowBounds["turn-footer:active"] != before || v.historyRowBounds["input:"+in.OperationID].H > 0 || v.runningProgress != progress {
			t.Fatalf("the running footer moved before confirmation, eventFirst=%v: caption=%+v footer=%+v", eventFirst, running, footer)
		}
		accepted, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationAccepted})
		job.Apply(v.Model, accepted, nil)
		ui.Frame()
		if current, ok := ui.Find("进行中"); !ok || current != running {
			t.Fatal("worker acceptance changed the still-pending indicator", running, current)
		}
		detail = v.Model.Details["s"]
		item := domain.Item{ID: "input:" + in.OperationID, TurnID: "active", Kind: "user", Text: in.Text, Status: "sent", Final: true}
		updateItem := func() {
			detail.Items = append(detail.Items, item)
			v.Model.Details["s"] = detail
		}
		body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationConfirmed})
		ack := func() { job.Apply(v.Model, body, nil) }
		if eventFirst {
			updateItem()
		} else {
			ack()
		}
		ui.Frame()
		if ui.HasText("正在发送") || !ui.HasText("进行中") {
			t.Fatal("first confirmation did not update the existing indicator")
		}
		if !eventFirst && (v.historyRowBounds["turn-footer:active"] != before || v.historyRowBounds[item.ID].H > 0) {
			t.Fatal("confirmation without an authoritative input moved the running footer")
		}
		if eventFirst {
			ack()
		} else {
			updateItem()
		}
		ack()
		job.Apply(v.Model, accepted, nil)
		ui.Frame()
		tail := v.historyRowBounds["turn-footer:active"]
		inserted := v.historyRowBounds[item.ID]
		summary := v.historyRowBounds["reply-before:"+item.ID]
		if summary != before || inserted.Y <= summary.Y || tail.Y <= inserted.Y ||
			ui.HasText("正在发送") || !ui.HasText("进行中") || v.runningProgress != progress {
			t.Fatalf("reduced motion did not show the settled insertion in order, eventFirst=%v: summary=%+v input=%+v footer=%+v", eventFirst, summary, inserted, tail)
		}
	}
}

func TestSecondSteerKeepsConfirmedBubbleWhileItsItemIsDelayed(t *testing.T) {
	v, ui := nativeQueueFixture()
	feedbackClick(t, ui, "消息输入")
	ui.Type("第一条追加")
	ui.Key(n.Cmd, n.KeyEnter)
	job := feedbackJob(t, v, "turn.steer")
	in := job.Input.(protocol.SteerInput).Input
	id := "input:" + in.OperationID
	ui.Advance(600 * time.Millisecond)
	receipt, err := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationConfirmed})
	if err != nil {
		t.Fatal(err)
	}
	job.Apply(v.Model, receipt, nil)
	ui.Frame()
	ui.Type("第二条追加")
	ui.Key(n.Cmd, n.KeyEnter)
	second := feedbackJob(t, v, "turn.steer").Input.(protocol.SteerInput).Input
	ui.Advance(600 * time.Millisecond)
	check := func() {
		t.Helper()
		detail := v.homeSendDetail(v.Model.Details["s"])
		var inputs []string
		for _, item := range detail.Items {
			if item.ID == id || item.ID == "input:"+second.OperationID {
				inputs = append(inputs, item.Text)
			}
		}
		if len(inputs) != 2 || inputs[0] != in.Text || inputs[1] != second.Text {
			t.Fatalf("successive bubbles disappeared, duplicated or changed order: %v", inputs)
		}
	}
	check()
	firstBounds, firstVisible := ui.Find(in.Text)
	secondBounds, secondVisible := ui.Find(second.Text)
	if !firstVisible || !secondVisible {
		t.Fatal("both bubbles must be visible before the first item arrives")
	}
	detail := v.Model.Details["s"]
	detail.Items = append(detail.Items, domain.Item{ID: id, TurnID: "active", Kind: "user", Text: in.Text, Status: "sent", Final: true})
	v.Model.Details["s"] = detail
	ui.Frame()
	check()
	firstAfter, _ := ui.Find(in.Text)
	secondAfter, _ := ui.Find(second.Text)
	if firstAfter != firstBounds || secondAfter != secondBounds {
		t.Fatalf("the delayed item moved either bubble: %v %v → %v %v", firstBounds, secondBounds, firstAfter, secondAfter)
	}
}

func TestQueuedSendStartsFlightBeforeWorkerUpdates(t *testing.T) {
	v, ui := nativeQueueFixture()
	feedbackClick(t, ui, "待发送 2")
	ui.Advance(time.Second)
	feedbackClick(t, ui, "立即发送待发消息 1")
	feedbackJob(t, v, "queue.send")
	ui.Frame()
	if _, visible := ui.Find("用户消息气泡"); !visible || !ui.HasText("正在发送") || v.sent == nil || !v.sent.queued {
		t.Fatal("the queued message did not start its flight before the worker", ui.Texts())
	}
	original := v.sent
	ui.Advance(800 * time.Millisecond)
	assertQueuedSendIndicator(t, ui)
	before, _ := ui.Find("用户消息气泡")
	copyBefore, _ := ui.Find("复制消息")
	confirmQueuedInput(v)
	ui.Frame()
	after, _ := ui.Find("用户消息气泡")
	if v.sent != nil || original.started.IsZero() || before != after {
		t.Fatal("the item event replayed or moved the settled message", before, after)
	}
	ui.Advance(150 * time.Millisecond)
	copyAfter, _ := ui.Find("复制消息")
	if ui.HasText("正在发送") || copyBefore != copyAfter {
		t.Fatal("confirmation kept the sending status after its exit")
	}
}

func assertQueuedSendIndicator(t *testing.T, ui *n.Tester) {
	t.Helper()
	label, visible := ui.Find("正在发送")
	actions, hasActions := ui.Find("用户消息操作")
	status, hasStatus := ui.Find("回复信息")
	if !visible || !hasActions || !hasStatus || label.Y < actions.Y+actions.H || label.X > status.X+40 {
		t.Fatalf("queued send status should be in the indicator beneath the message, caption=%+v actions=%+v indicator=%+v", label, actions, status)
	}
}

func TestQueuedFlightSurvivesDeliveryOrderAndRestoresRejectedItem(t *testing.T) {
	for _, order := range []string{"reply-first", "event-first", "not-sent", "unknown"} {
		t.Run(order, func(t *testing.T) {
			v, ui := nativeQueueFixture()
			v.Model.Drafts["session:s"] = "keep this draft"
			feedbackClick(t, ui, "待发送 2")
			ui.Advance(time.Second)
			beforeQueue := v.Model.Queue()
			first := beforeQueue.Items[0]
			feedbackClick(t, ui, "立即发送待发消息 1")
			job := feedbackJob(t, v, "queue.send")
			ui.Advance(600 * time.Millisecond)
			if _, ok := ui.Find("用户消息气泡"); !ok {
				t.Fatal("missing local flight")
			}
			id := "input:" + first.Input.OperationID
			if order == "not-sent" {
				job.Apply(v.Model, nil, &protocol.RemoteError{Code: "notSent", Message: "未发送"})
				ui.Frame()
				ui.Advance(500 * time.Millisecond)
				if _, ok := ui.Find("用户消息气泡"); ok || !ui.HasText(first.Input.Text) || len(v.Model.Queue().Items) != 2 {
					t.Fatal("rejected input did not return to its queue row")
				}
				return
			}
			if order == "unknown" {
				job.Apply(v.Model, nil, errors.New("connection lost"))
				ui.Frame()
				ui.Advance(time.Second)
				if !ui.HasText("发送结果待核实") {
					t.Fatal("unknown result was dropped", ui.Texts())
				}
				for len(v.Controller.Jobs) > 0 {
					if (<-v.Controller.Jobs).Method == "queue.send" {
						t.Fatal("unknown result was resent")
					}
				}
			}
			d := v.Model.Details["s"]
			d.Items = append(d.Items, domain.Item{ID: id, Kind: "queued", TurnID: "active", Text: first.Input.Text, Final: true})
			v.Model.Details["s"] = d
			ui.Frame()
			before, visible := ui.Find("用户消息气泡")
			if !visible {
				t.Fatal("silent worker queue record hid the pending bubble")
			}
			queue := beforeQueue
			queue.Revision++
			queue.Items = queue.Items[1:]
			body, _ := json.Marshal(queue)
			accept := func() {
				d := v.Model.Details["s"]
				d.Items[len(d.Items)-1] = domain.Item{ID: id, Kind: "user", TurnID: "active", Text: first.Input.Text, Status: "sent", Final: true}
				v.Model.Details["s"] = d
			}
			if order == "reply-first" {
				job.Apply(v.Model, body, nil)
				ui.Frame()
				accept()
			} else {
				accept()
				ui.Frame()
				job.Apply(v.Model, body, nil)
			}
			job.Apply(v.Model, body, nil)
			ui.Frame()
			after, visible := ui.Find("用户消息气泡")
			if !visible || before != after || v.sent != nil || v.Model.Drafts["session:s"] != "keep this draft" {
				t.Fatal("receipt moved, replayed or hid the settled bubble", before, after)
			}
		})
	}
}

func TestQueuedFlightIntoAnIdleSessionKeepsPositionWhenTheTurnArrives(t *testing.T) {
	v, ui := nativeQueueFixture()
	d := v.Model.Details["s"]
	d.Session.Status = domain.ExecutionIdle
	d.Turns[0].Status = domain.ExecutionCompleted
	v.Model.Details["s"] = d
	v.Model.Snapshot.Sessions[0] = d.Session
	ui.Frame()
	feedbackClick(t, ui, "待发送 2")
	ui.Advance(time.Second)
	feedbackClick(t, ui, "立即发送待发消息 1")
	job := feedbackJob(t, v, "queue.send")
	in := job.Input.(protocol.QueueInput)
	if in.TurnID != "" {
		t.Fatal("fixture unexpectedly used Steer")
	}
	ui.Advance(time.Second)
	assertQueuedSendIndicator(t, ui)
	before, _ := ui.Find("用户消息气泡")
	indicatorBefore, _ := ui.Find("回复信息")
	confirmQueuedInput(v)
	d = v.Model.Details["s"]
	d.Items[len(d.Items)-1].TurnID = "new-turn"
	v.Model.Details["s"] = d
	ui.Frame()
	if frame, ok := ui.Find("回复信息"); !ok || frame != indicatorBefore {
		t.Fatal("message arrived before the turn and displaced its status", indicatorBefore, frame)
	}
	d.Turns = append(d.Turns, domain.Turn{ID: "new-turn", Status: domain.ExecutionRunning})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	after, visible := ui.Find("用户消息气泡")
	if !visible || before != after {
		t.Fatal("a new worker turn moved the already sent queue bubble", before, after)
	}
	if ui.HasText("正在发送") || !ui.HasText("进行中") || v.toolArrivals.footerKeys["new-turn"] == "" {
		t.Fatal("the new worker turn did not take over the existing status")
	}
	if indicatorAfter, ok := ui.Find("回复信息"); !ok || indicatorBefore != indicatorAfter {
		t.Fatal("the active turn relocated the status", indicatorBefore, indicatorAfter)
	}
}

func TestSessionRenameShowsSavingWhilePreservingTheProposal(t *testing.T) {
	v, _ := nativeQueueFixture()
	v.Model.WorkerMinor = 5
	v.rename = &sessionRename{Connection: "local", Session: v.Model.Details["s"].Session, Title: "renamed", Open: true}
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	feedbackClick(t, ui, "保存名称")
	job := feedbackJob(t, v, "session.rename")
	ui.Frame()
	if !ui.HasText("正在保存") || !v.rename.Open {
		t.Fatal("rename has no in-place progress")
	}
	job.Apply(v.Model, nil, errors.New("device offline"))
	ui.Advance(500 * time.Millisecond)
	if !v.rename.Open || v.rename.Title != "renamed" || !ui.HasText("device offline") {
		t.Fatal("rename failure did not preserve the input")
	}
}
func TestQueuedRemovalStartsBeforeReplyAndRestoresRejectedRow(t *testing.T) {
	v, ui := nativeQueueFixture()
	feedbackClick(t, ui, "待发送 2")
	ui.Advance(time.Second)
	second, _ := ui.Find("Add a small example")
	feedbackClick(t, ui, "移除待发消息 1")
	job := feedbackJob(t, v, "queue.remove")
	ui.Advance(250 * time.Millisecond)
	if ui.HasText("Review the rotation code") || len(v.Model.Queue().Items) != 2 {
		t.Fatal("pending removal did not leave the view while keeping the worker queue")
	}
	job.Apply(v.Model, nil, &protocol.RemoteError{Code: "notSent", Message: "未移除"})
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	restored, _ := ui.Find("Add a small example")
	if !ui.HasText("Review the rotation code") || restored.Y != second.Y {
		t.Fatalf("rejection did not restore queue order: %v → %v", second, restored)
	}
}

func TestQueuedEditReturnsToOrdinaryDraftBeforeReplyAndRestoresOnFailure(t *testing.T) {
	v, ui := nativeQueueFixture()
	v.Model.Drafts["session:s"] = "ordinary draft"
	feedbackClick(t, ui, "待发送 2")
	ui.Advance(time.Second)
	feedbackClick(t, ui, "编辑待发消息 1")
	ui.Advance(time.Second)
	ui.Type(" revised")
	edited := v.Model.Drafts["session:s"]
	feedbackClick(t, ui, "保存编辑")
	job := feedbackJob(t, v, "queue.edit")
	ui.Advance(600 * time.Millisecond)
	if v.Model.Drafts["session:s"] != "ordinary draft" || !ui.HasText("正在保存") || !ui.Focused("消息输入") {
		t.Fatal("save left the user in the edit while waiting")
	}
	ui.Type(" next")
	ordinary := v.Model.Drafts["session:s"]
	job.Apply(v.Model, nil, &protocol.RemoteError{Code: "notSent", Message: "未保存"})
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	if v.Model.Drafts["session:s"] != ordinary || !ui.Focused("消息输入") {
		t.Fatal("a failed save interrupted the next draft")
	}
	feedbackClick(t, ui, "恢复待发编辑")
	ui.Advance(500 * time.Millisecond)
	if v.Model.Drafts["session:s"] != edited || v.Model.QueueEdits["s"].Displaced != ordinary {
		t.Fatalf("rejection lost one of the drafts: %q vs %q", v.Model.Drafts["session:s"], v.Model.QueueEdits["s"].Displaced)
	}
}

func TestApprovalShowsSelectedSubmissionBeforeResolution(t *testing.T) {
	v := requestMotionModel()
	v.Controller = app.NewController("")
	d := v.Model.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "approval", SessionID: "s", Kind: "approval", Title: "需要授权", State: "pending"}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("待处理请求卡片")
	feedbackClick(t, ui, "允许这一次")
	job := feedbackJob(t, v, "request.respond")
	ui.Advance(150 * time.Millisecond)
	pending, _ := ui.Find("待处理请求卡片")
	if !ui.HasText("正在提交") || pending != before {
		t.Fatalf("request did not show stable pending feedback: %v → %v", before, pending)
	}
	job.Apply(v.Model, nil, nil)
	ui.Frame()
	if !ui.HasText("正在提交") {
		t.Fatal("RPC arrival reset the selected request button")
	}
}

func TestKeepAwakeReportsPendingOnTheDeviceControl(t *testing.T) {
	v := New(deviceHealthModel(), app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	feedbackClick(t, ui, "当前设备")
	ui.Advance(time.Second)
	feedbackClick(t, ui, "保持唤醒开关")
	job := feedbackJob(t, v, "device.keepAwake")
	ui.Advance(150 * time.Millisecond)
	if !ui.HasText("正在更新") || v.Model.DeviceHealth.KeepAwake.Enabled {
		t.Fatal("device control has no confirmed-versus-pending feedback")
	}
	job.Apply(v.Model, nil, &protocol.RemoteError{Code: "notSent", Message: "未更新"})
	ui.Frame()
	if ui.HasText("正在更新") {
		t.Fatal("pending feedback outlived the failed operation")
	}
}

func TestChildSendProjectsOneMessageWhileTheNextDraftStaysEditable(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	v.Controller = app.NewController("")
	d := v.Model.Details["s"]
	d.Works[0].CanSteer = true
	v.Model.Details["s"] = d
	feedbackClick(t, ui, "查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	feedbackClick(t, ui, "发送消息")
	ui.Advance(time.Second)
	feedbackClick(t, ui, "子 Agent 消息输入")
	ui.Type("子任务本条消息")
	ui.Key(0, n.KeyEnter)
	job := feedbackJob(t, v, "session.work.send")
	in := job.Input.(domain.WorkInput)
	ui.Advance(600 * time.Millisecond)
	before, ok := ui.Find(in.Text)
	if !ok || !ui.Focused("子 Agent 消息输入") {
		t.Fatal("child send did not project a message and keep the input ready")
	}
	ui.Type("子任务下一条")
	key := v.Model.WorkKey("s", "agent-a")
	detail := v.Model.WorkDetails[key]
	detail.Items = append(detail.Items, domain.WorkItem{Item: domain.Item{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Final: true}})
	v.Model.WorkDetails[key] = detail
	ui.Frame()
	after, ok := ui.Find(in.Text)
	if !ok || after != before {
		t.Fatalf("child message moved when the worker event arrived: %v → %v", before, after)
	}
	body, _ := json.Marshal(domain.WorkInputOperation{Input: in, State: domain.OperationConfirmed})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	u := v.workPanel("agents").detail("agent-a")
	if u.input.draft != "子任务下一条" || !ui.Focused("子 Agent 消息输入") {
		t.Fatal("late child confirmation changed the new draft or focus")
	}
}

func TestContinueOriginalSessionShowsInputBeforeWorkerAndKeepsDraft(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 7
	d := m.Details["s"]
	d.Session.Status = "abandoned"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready", CanContinue: true}
	m.Details["s"], m.Snapshot.Sessions[0] = d, d.Session
	m.Drafts["session:s"] = "ordinary continuation draft"
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	feedbackClick(t, ui, "继续原会话")
	feedbackJob(t, v, "session.submit")
	ui.Advance(600 * time.Millisecond)
	if _, ok := ui.Find("用户消息气泡"); !ok || !ui.HasText("正在发送") || m.Drafts["session:s"] != "ordinary continuation draft" {
		t.Fatal("continuation has no local input or consumed the ordinary draft")
	}
}

func TestSessionArchiveAndRestoreAnimateBeforeCatalogChanges(t *testing.T) {
	for _, action := range []struct {
		name     string
		archived bool
		row      string
		next     string
		height   float32
	}{
		{"archive", false, "Layout", "Next session", 30},
		{"restore", true, "查看归档会话：Layout", "查看归档会话：Next session", 40},
	} {
		for _, outcome := range []string{"slow", "fast", "mid-flight", "rejected", "reduced motion"} {
			t.Run(action.name+"/"+outcome, func(t *testing.T) {
				m := layoutModel()
				m.WorkerMinor = 40
				m.Snapshot.Sessions[0].Archived = action.archived
				m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "next", WorkspaceID: "w", Title: "Next session", Archived: action.archived})
				if outcome == "mid-flight" && !action.archived {
					m.SessionID = ""
					m.CompletedReplies = map[string]app.CompletedReply{"s": {Unread: true}}
				}
				v := New(m, app.NewController(""), "")
				if action.archived {
					v.openArchive()
				}
				ui := n.NewTester(v.View, 1240, 820)
				if outcome == "reduced motion" {
					ui.SetPreferences(n.Preferences{ReduceMotion: true})
				}
				ui.Advance(time.Second)
				sourceVisible := func() bool {
					row, found := ui.Find(action.row)
					return found && (action.archived || row.X < 256)
				}
				before, ok := ui.Find(action.next)
				if !ok {
					t.Fatal("missing the following row")
				}
				source, _ := ui.Find(action.row)
				if action.archived {
					row, _ := ui.Find(action.row)
					ui.Move(row.X+10, row.Y+row.H/2)
					ui.Frame()
					feedbackClick(t, ui, "恢复会话：Layout")
				} else {
					if err := ui.RightClick(action.row); err != nil {
						t.Fatal(err)
					}
					if err := ui.ChooseMenuItem("归档会话"); err != nil {
						t.Fatal(err)
					}
				}
				job := feedbackJob(t, v, "session.archive")
				if m.Snapshot.Sessions[0].Archived != action.archived {
					t.Fatal("presentation mutated the authoritative catalog")
				}
				updateCatalog := func() {
					m.Snapshot.Sessions[0].Archived = !action.archived
					ui.Frame()
				}
				switch outcome {
				case "fast":
					updateCatalog()
				case "mid-flight":
					ui.Advance(32 * time.Millisecond)
					ui.Frame()
					pixels := contextPixels(ui, source)
					saveNativeSkillImage(t, ui, action.name+"-before-receipt.png")
					job.Apply(m, nil, nil)
					delete(m.CompletedReplies, "s")
					updateCatalog()
					saveNativeSkillImage(t, ui, action.name+"-after-receipt.png")
					if !bytes.Equal(pixels, contextPixels(ui, source)) {
						t.Fatal("the source row changed pixels when the receipt arrived mid-animation")
					}
				case "slow":
					job.Apply(m, nil, nil)
				case "reduced motion":
					ui.Frame()
					after, _ := ui.Find(action.next)
					if sourceVisible() || after.Y > before.Y-action.height+1 || m.Snapshot.Sessions[0].Archived != action.archived {
						t.Fatalf("reduced motion did not close the row immediately: %v → %v", before, after)
					}
					return
				}
				if outcome == "mid-flight" {
					ui.Advance(48 * time.Millisecond)
				} else {
					ui.Advance(80 * time.Millisecond)
				}
				if outcome == "slow" {
					saveNativeSkillImage(t, ui, action.name+"-80ms.png")
				}
				moving, ok := ui.Find(action.next)
				if !ok || moving.Y >= before.Y-2 || moving.Y <= before.Y-action.height+1 {
					t.Fatalf("the following row did not move smoothly at 80 ms: %v → %v", before, moving)
				}
				ui.Advance(300 * time.Millisecond)
				settled, _ := ui.Find(action.next)
				if sourceVisible() || settled.Y >= moving.Y-2 {
					t.Fatal("the source list did not finish closing the row")
				}
				if outcome == "rejected" {
					job.Apply(m, nil, errors.New("revision changed"))
					ui.Frame()
					ui.Advance(300 * time.Millisecond)
					after, _ := ui.Find(action.next)
					if !sourceVisible() || after != before || !ui.HasText("revision changed") {
						t.Fatalf("rejection did not restore the row and show the error: %v → %v", before, after)
					}
				} else if outcome == "slow" {
					if action.archived {
						if row, visible := ui.Find("Layout"); visible && row.X < 256 {
							t.Fatal("restoring the session made it active before the catalog changed")
						}
					}
					pixels := contextPixels(ui, source)
					updateCatalog()
					for range 4 {
						after, _ := ui.Find(action.next)
						if sourceVisible() || after != settled || !bytes.Equal(pixels, contextPixels(ui, source)) {
							t.Fatalf("confirmation changed the settled source list: %v → %v", settled, after)
						}
						ui.Advance(32 * time.Millisecond)
					}
				} else if outcome == "fast" {
					job.Apply(m, nil, nil)
				}
				if action.archived && outcome != "rejected" {
					ui.Advance(300 * time.Millisecond)
					if row, visible := ui.Find("Layout"); !visible || row.X >= 256 {
						t.Fatal("confirmed restore did not make the session active")
					}
				}
			})
		}
	}
}

func TestCheckoutShowsPendingUntilOwningDeviceConfirms(t *testing.T) {
	m := layoutModel()
	m.ConnectionEpoch = "git-epoch"
	m.GitStatuses["session:s"] = domain.GitStatus{Applicable: true, Branch: "main", Branches: []string{"main", "topic"}}
	m.GitEpochs["session:s"] = m.ConnectionEpoch
	v := New(m, app.NewController(""), "")
	v.gitLoads = map[string]*gitLoadState{"session:s": {tag: "git-epoch:0", phase: gitReady}}
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	v.chooseBranch("topic")
	job := feedbackJob(t, v, "git.checkout")
	ui.Frame()
	if !ui.HasText("正在切换分支") || m.GitStatuses["session:s"].Branch != "main" {
		t.Fatal("checkout does not distinguish pending from the confirmed branch")
	}
	body, _ := json.Marshal(domain.GitStatus{Applicable: true, Branch: "topic", Branches: []string{"main", "topic"}})
	job.Apply(m, body, nil)
	ui.Frame()
	if ui.HasText("正在切换分支") || m.GitStatuses["session:s"].Branch != "topic" {
		t.Fatal("checkout did not settle")
	}
}

func TestActiveConversationConfirmsObservedBranchAndClearsNotice(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 59
	m.ConnectionEpoch = "git-epoch"
	detail := m.Details["s"]
	detail.Session.Status = "running"
	detail.Session.GitBranch = "main"
	detail.Session.ContextChanged = true
	m.Details["s"] = detail
	m.Snapshot.Sessions[0] = detail.Session
	status := domain.GitStatus{Applicable: true, Branch: "topic", Head: "abcdef1234", Branches: []string{"main", "topic"}}
	m.SetGitStatus("session:s", status)
	v := New(m, app.NewController(""), "")
	v.gitLoads = map[string]*gitLoadState{"session:s": {tag: "git-epoch:0", phase: gitReady}}
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "active-branch-notice.png")
	feedbackClick(t, ui, "查看分支")
	body, _ := json.Marshal(status)
	feedbackJob(t, v, "git.status").Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "active-branch-menu.png")
	feedbackClick(t, ui, "确认当前分支")
	job := feedbackJob(t, v, "git.confirm")
	if input, ok := job.Input.(protocol.ConfirmCheckoutInput); !ok || input.SessionID != "s" || input.WorkspaceID != "w" || input.Branch != "topic" || input.Head != "abcdef1234" {
		t.Fatalf("confirmation did not send the reviewed branch and HEAD: %+v", job.Input)
	}
	job.Apply(m, body, nil)
	confirmed := detail.Session
	confirmed.ContextChanged = false
	confirmed.GitBranch, confirmed.GitHead = "topic", "abcdef1234"
	confirmed.Revision++
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "session", SessionID: "s", Session: &confirmed}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "active-branch-confirmed.png")
	if ui.HasText("执行分支已改变") || m.Details["s"].Session.Status != "running" {
		t.Fatal("confirmation should clear the notice while the agent keeps running")
	}
}

func TestBackgroundSwitchHandsPendingCaptionToControllerState(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerService = &app.WorkerService{Background: &domain.BackgroundService{Format: 1, State: "notRegistered"}}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceTab, v.deviceSelected = true, "settings", "air"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	feedbackClick(t, ui, "登录后启动服务开关")
	feedbackJob(t, v, "$background.enable")
	ui.Advance(200 * time.Millisecond)
	if !ui.HasText("正在启用后台服务") {
		t.Fatal("background switch has no immediate feedback")
	}
	before, _ := ui.Find("后台服务详情")
	state.WorkerService.Background = &domain.BackgroundService{Format: 1, State: "enabling", Message: "正在启用后台服务"}
	ui.Frame()
	after, _ := ui.Find("后台服务详情")
	if before != after || !ui.HasText("正在启用后台服务") {
		t.Fatal("controller state restarted the background transition")
	}
}

func TestTailnetInboundShowsPendingUntilMatchingServiceObservation(t *testing.T) {
	v, ui := tailnetManagementView()
	feedbackClick(t, ui, "入站转发开关")
	job := feedbackJob(t, v, "$tailnet")
	in := job.Input.(tailnet.Action)
	ui.Advance(150 * time.Millisecond)
	if !ui.HasText("正在关闭入站转发") || !v.Model.Tailnet.InboundEnabled {
		t.Fatal("inbound control has no pending state")
	}
	v.Model.Tailnet.OperationID = "unrelated"
	ui.Frame()
	if !ui.HasText("正在关闭入站转发") {
		t.Fatal("unrelated service update retired the pending action")
	}
	v.Model.Tailnet.OperationID, v.Model.Tailnet.InboundEnabled = in.ID, false
	ui.Frame()
	if ui.HasText("正在关闭入站转发") {
		t.Fatal("matching service update did not settle the control")
	}
}

func TestRejectedSteerKeepsOnlyNextDraftAndFailureFeedback(t *testing.T) {
	v, ui := nativeQueueFixture()
	feedbackClick(t, ui, "消息输入")
	ui.Type("要保留的原文")
	ui.Key(n.Cmd, n.KeyEnter)
	job := feedbackJob(t, v, "turn.steer")
	ui.Advance(150 * time.Millisecond)
	ui.Type("独立的下一条")
	job.Apply(v.Model, nil, &protocol.RemoteError{Code: "notSent", Message: "未接纳"})
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	saveNativeSkillImage(t, ui, "rejected-steer.png")
	if v.Model.Drafts["session:s"] != "独立的下一条" || !ui.Focused("消息输入") {
		t.Fatal("rejection changed the next draft")
	}
	if !ui.HasText("消息未发送") {
		t.Fatal("rejection lost the send failure feedback")
	}
	if len(v.Model.Drafts) != 1 {
		t.Fatalf("rejection created another draft: %v", v.Model.Drafts)
	}
}

func TestQueueRemovalDoesNotReplayWhenEventOrReplyArrives(t *testing.T) {
	for _, eventFirst := range []bool{false, true} {
		v, ui := nativeQueueFixture()
		feedbackClick(t, ui, "待发送 2")
		ui.Advance(time.Second)
		feedbackClick(t, ui, "移除待发消息 1")
		job := feedbackJob(t, v, "queue.remove")
		ui.Advance(80 * time.Millisecond)
		before, _ := ui.Find("Add a small example")
		q := v.Model.Queue()
		q.Revision++
		q.Items = q.Items[1:]
		body, _ := json.Marshal(q)
		if eventFirst {
			v.Model.SetQueue(q)
		} else {
			job.Apply(v.Model, body, nil)
		}
		ui.Frame()
		first, ok := ui.Find("Add a small example")
		if !ok || first.Y != before.Y || ui.HasText("Review the rotation code") {
			t.Fatalf("first update restarted removal: %v → %v (event first %v)", before, first, eventFirst)
		}
		ui.Advance(600 * time.Millisecond)
		before, _ = ui.Find("Add a small example")
		if eventFirst {
			job.Apply(v.Model, body, nil)
		} else {
			v.Model.SetQueue(q)
		}
		ui.Frame()
		last, ok := ui.Find("Add a small example")
		if !ok || last.Y != before.Y || ui.HasText("Review the rotation code") {
			t.Fatalf("late update replayed removal: %v → %v (event first %v)", before, last, eventFirst)
		}
	}
}

func TestChildStopShowsPendingThroughCommandReceipt(t *testing.T) {
	v, ui := sessionWorkFixture(t)
	v.Controller = app.NewController("")
	d := v.Model.Details["s"]
	d.Works[0].CanStop = true
	v.Model.Details["s"] = d
	feedbackClick(t, ui, "查看子 Agent：检查边界条件")
	ui.Advance(time.Second)
	feedbackClick(t, ui, "停止子 Agent")
	job := feedbackJob(t, v, "session.work.stop")
	ui.Advance(150 * time.Millisecond)
	if !ui.HasText("正在停止") {
		t.Fatal("stop has no pending feedback")
	}
	job.Apply(v.Model, nil, nil)
	ui.Frame()
	if !ui.HasText("正在停止") {
		t.Fatal("command response alone reset the stop control")
	}
	d.Works[0].State = "stopped"
	v.Model.Details["s"] = d
	ui.Frame()
	if ui.HasText("正在停止") {
		t.Fatal("authoritatively stopped child still shows pending")
	}
}

func TestKeepAwakeIgnoresLateFailureAfterMatchingDeviceState(t *testing.T) {
	v := New(deviceHealthModel(), app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	feedbackClick(t, ui, "当前设备")
	ui.Advance(time.Second)
	feedbackClick(t, ui, "保持唤醒开关")
	job := feedbackJob(t, v, "device.keepAwake")
	observed := *v.Model.DeviceHealth
	observed.KeepAwake.Enabled = true
	observed.KeepAwake.Revision++
	v.Model.SetDeviceHealth(observed, time.Now())
	ui.Frame()
	if ui.HasText("正在更新") {
		t.Fatal("matching device state did not settle the same control")
	}
	job.Apply(v.Model, nil, contextDeadlineError{})
	ui.Frame()
	if ui.HasText("未知") || !v.Model.DeviceHealth.KeepAwake.Enabled {
		t.Fatal("late command failure reset the confirmed control")
	}
}
