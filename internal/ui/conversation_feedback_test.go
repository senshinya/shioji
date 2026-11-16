package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestConversationFeedbackLateJobKeepsOriginalSession(t *testing.T) {
	m := layoutModel()
	controller := app.NewController("")
	v := New(m, controller, "")
	ui := n.NewTester(v.View, 1100, 820)
	for len(controller.Jobs) > 0 {
		<-controller.Jobs
	}
	v.do(app.Job{Method: "git.checkout", Apply: func(m *app.Model, _ json.RawMessage, err error) { m.Error = err.Error() }})
	job := <-controller.Jobs
	other := domain.Session{ID: "other", WorkspaceID: "w", Title: "Other", Status: "completed"}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
	m.Details["other"] = domain.SessionDetail{Session: other}
	m.SessionID = "other"
	m.Drafts["session:other"] = "keep"
	job.Apply(m, nil, errors.New("late failure"))
	ui.Frame()
	messages := v.feedbackState().messages
	if len(messages) == 0 || messages[len(messages)-1].SessionID != "s" {
		t.Fatal("late result rebound to selected session", messages)
	}
	if m.SessionID != "other" || m.Drafts["session:other"] != "keep" {
		t.Fatal("late result changed selection or draft")
	}
}

func TestConversationFeedbackBackgroundWaitEndsAfterSessionSwitch(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	token := v.attachmentPreparationKey(m.DraftKey())
	v.attachmentPreparing = map[string]int{token: 1}
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if !ui.HasText("正在准备附件") {
		t.Fatal("no preparation feedback")
	}
	other := domain.Session{ID: "other", WorkspaceID: "w", Status: "completed"}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
	m.Details["other"] = domain.SessionDetail{Session: other}
	m.SessionID = "other"
	v.attachmentPreparing[token] = 0
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("正在准备附件") {
		t.Fatal("completed background wait remained visible")
	}
}

func TestConversationFeedbackArchiveAboveComposer(t *testing.T) {
	m := layoutModel()
	m.Snapshot.Sessions[0].Archived = true
	d := m.Details["s"]
	d.Session = m.Snapshot.Sessions[0]
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1100, 820)
	ui.Advance(time.Second)
	note, ok := ui.Find("会话已归档")
	composer, _ := ui.Find("消息编辑器")
	if !ok || note.Y+note.H > composer.Y {
		t.Fatalf("archive status must be above the composer: %v %v", note, composer)
	}
}

func TestConversationFeedbackConnectionWaitMigratesWithoutDuplicate(t *testing.T) {
	m := layoutModel()
	m.Connected = false
	ui := n.NewTester(New(m, nil, "").View, 1100, 820)
	if ui.HasText("连接执行服务中…") || ui.HasText("正在连接执行服务") {
		t.Fatal("short connection wait flashes immediately")
	}
	ui.Advance(450 * time.Millisecond)
	toast, ok := ui.Find("正在连接执行服务")
	composer, _ := ui.Find("消息编辑器")
	if !ok || toast.Y > 100 || toast.X < composer.X+composer.W/2 {
		t.Fatalf("connection wait is not at the upper right: %v", toast)
	}
	ui.Advance(5 * time.Second)
	note, ok := ui.Find("无法连接执行设备")
	if !ok || note.Y+note.H > composer.Y || ui.HasText("正在连接执行服务") {
		t.Fatalf("connection wait did not migrate once: %v %v", note, ui.Texts())
	}
	m.Connected = true
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("无法连接执行设备") || m.Drafts[m.DraftKey()] != "" {
		t.Fatal("reconnect left the blocker or sent a draft")
	}
}

func TestConversationFeedbackUnknownSubmissionQueriesOriginalOperation(t *testing.T) {
	m := layoutModel()
	m.Drafts[m.DraftKey()] = "保留原文"
	m.Pending["op-1"] = domain.Submission{OperationID: "op-1", SessionID: "s", WorkspaceID: "w", Text: "保留原文"}
	m.PendingDevices["op-1"] = "local"
	m.SubmissionError(m.Pending["op-1"], errors.New("connection interrupted"))
	controller := app.NewController("")
	v := New(m, controller, "")
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click("核对提交结果"); err != nil {
		t.Fatal("unknown submit has no query action", err)
	}
	found := false
	for len(controller.Jobs) > 0 {
		j := <-controller.Jobs
		if j.Method == "session.submit" {
			t.Fatal("unknown input was replayed")
		}
		if j.Method == "operation" {
			found = true
			b, _ := json.Marshal(j.Input)
			if string(b) != "{\"id\":\"op-1\"}" {
				t.Fatalf("wrong operation query %s", b)
			}
		}
	}
	if !found || m.Drafts[m.DraftKey()] != "保留原文" {
		t.Fatal("query missing or draft changed")
	}
}

func TestConversationFeedbackDoesNotWarnDuringOrdinarySubmit(t *testing.T) {
	m := layoutModel()
	m.Drafts[m.DraftKey()] = "原始输入"
	in, err := m.BeginSubmission()
	if err != nil {
		t.Fatal(err)
	}
	ui := n.NewTester(New(m, nil, "").View, 1100, 820)
	ui.Advance(time.Second)
	if ui.HasText("提交结果待核对") {
		t.Fatal("ordinary in-flight submission is presented as an unknown outcome")
	}
	m.SubmissionError(in, errors.New("connection interrupted"))
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("提交结果待核对") || m.Drafts[m.DraftKey()] != "原始输入" {
		t.Fatal("uncertain outcome lost its query surface or draft")
	}
}

func TestConversationFeedbackRepeatedReceiptDoesNotExtendSuccess(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	f := app.Feedback{ID: "stable", ConnectionID: "local", Title: "已复制", Kind: app.FeedbackSuccess}
	m.ReportFeedback(f)
	ui.Frame()
	ui.Move(0, 0)
	ui.Advance(3 * time.Second)
	m.ReportFeedback(f)
	ui.Frame()
	ui.Advance(2 * time.Second)
	if ui.HasText("已复制") {
		t.Fatal("duplicate receipt extended a success notification")
	}
}

func TestConversationFeedbackRecoverySharesStatePager(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 8
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "attention"}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	v := New(m, nil, "")
	v.draftSaveError = "save failed"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if ui.HasText("草稿尚未保存") && ui.HasText("暂时无法继续") {
		t.Fatal("two state panels expanded at once")
	}
	if err := ui.Click("下一个状态"); err != nil {
		t.Fatal("recovery not available through state pager", err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("暂时无法继续") {
		t.Fatal("pager did not reach recovery")
	}
}

func TestConversationFeedbackNewStateDoesNotReplaceManuallySelectedStatus(t *testing.T) {
	m := layoutModel()
	m.Snapshot.Sessions[0].Archived = true
	d := m.Details["s"]
	d.Session = m.Snapshot.Sessions[0]
	m.Details["s"] = d
	m.Readiness.State = "unavailable"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click("下一个状态"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("会话已归档") {
		t.Fatal("did not select archive status")
	}
	v.draftSaveError = "save failed"
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("会话已归档") {
		t.Fatal("new priority status displaced the user's selected status")
	}
}

func TestConversationFeedbackNewStateKeepsQuestionInForeground(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "r", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "先检查哪里？", Options: []domain.Option{{Label: "回执"}, {Label: "历史"}}}}}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click("回执"); err != nil {
		t.Fatal(err)
	}
	v.draftSaveError = "save failed"
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("本次修改仍保留在当前窗口，请重试保存，或先复制草稿。") {
		t.Fatal("new state expanded over an answer in progress")
	}
	if m.Answers["s/r/q"] != "回执" {
		t.Fatal("new state replaced answer")
	}
}

func TestConversationFeedbackErrorShowsRedactedReasonAtUpperRight(t *testing.T) {
	m := layoutModel()
	m.Error = "request failed: https://example.test/path?access_token=secret-value"
	ui := n.NewTester(New(m, nil, "").View, 1100, 820)
	ui.Advance(time.Second)
	note, ok := ui.Find("request failed: https://example.test")
	if !ok || note.Y > 100 {
		t.Fatalf("operation failure is not a compact upper-right message: %v", note)
	}
	if ui.HasText(m.Error) {
		t.Fatal("raw technical error used as a visible notice")
	}
	if ui.HasText("详细信息") {
		t.Fatal("short redacted reason still requires disclosure")
	}
	for _, text := range ui.Texts() {
		if text == m.Error {
			t.Fatal("credential leaked in details")
		}
	}
	if err := ui.Click("关闭消息"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText("request failed: https://example.test") {
		t.Fatal("dismissed toast remained visible")
	}
}

func TestConversationFeedbackRuntimeAndAttachmentBlockersStayAboveComposer(t *testing.T) {
	for _, kind := range []string{"runtime", "attachment"} {
		m := layoutModel()
		title := "当前 Agent 尚未就绪"
		if kind == "runtime" {
			m.Readiness.State = "unavailable"
			m.Readiness.Error = "not installed"
		} else {
			title = "附件暂时无法用于发送"
			m.Attachments[m.DraftKey()] = []app.DraftAttachment{{Attachment: domain.Attachment{ID: "a", Name: "log.txt"}, Error: "read failed"}}
		}
		ui := n.NewTester(New(m, nil, "").View, 1100, 820)
		ui.Advance(time.Second)
		r, ok := ui.Find(title)
		composer, _ := ui.Find("消息编辑器")
		if !ok || r.Y+r.H > composer.Y {
			t.Fatalf("%s blocker missing or below composer: %v", kind, r)
		}
	}
}

func TestConversationFeedbackRetentionAndScope(t *testing.T) {
	s := &conversationFeedback{}
	now := time.Now()
	for i := 0; i < 40; i++ {
		s.put(app.Feedback{ID: fmt.Sprint(i), ConnectionID: "local", Title: "结果"}, now, false)
	}
	s.put(app.Feedback{ID: "39", ConnectionID: "remote", Title: "另一个设备"}, now, false)
	if len(s.messages) != 30 || s.messages[28].ConnectionID == s.messages[29].ConnectionID {
		t.Fatal("retention or source identity lost", s.messages)
	}
}

func TestConversationFeedbackHoverPausesSuccess(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	m.ReportFeedback(app.Feedback{Title: "已复制", Kind: app.FeedbackSuccess})
	ui.Frame()
	ui.Advance(200 * time.Millisecond)
	r, ok := ui.Find("弹出消息")
	if !ok {
		t.Fatal("missing toast")
	}
	ui.Move(r.X+r.W/2, r.Y+r.H/2)
	ui.Advance(5 * time.Second)
	if !ui.HasText("已复制") {
		t.Fatal("hover did not pause expiry")
	}
	ui.Move(0, 0)
	ui.Advance(5 * time.Second)
	if ui.HasText("已复制") {
		t.Fatal("expiry did not resume")
	}
}

func TestConversationFeedbackToastTopInset(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1100, 820)
		ui.SetDark(dark)
		m.ReportFeedback(app.Feedback{ConnectionID: "local", WorkspaceID: "w", SessionID: "s", Title: "操作未完成", Body: "原有内容已保留。"})
		ui.Frame()
		ui.Advance(time.Second)
		card, _ := ui.Find("弹出消息")
		source, ok := ui.Find("Example · Layout · 这台 Mac")
		icon, _ := ui.Find("消息来源图标")
		if math.Abs(float64(icon.Y-source.Y)) > 1 || icon.H != source.H {
			t.Fatal("source icon and source text do not share a line", icon, source)
		}
		inkCenter := func(box n.Rect) float64 {
			im := ui.Image()
			bg := im.RGBAAt(int(card.X+3), int(box.Y+box.H/2))
			lo, hi := im.Bounds().Max.Y, -1
			for y := int(box.Y); y < int(box.Y+box.H); y++ {
				for x := int(box.X); x < int(box.X+box.W); x++ {
					p := im.RGBAAt(x, y)
					if math.Abs(float64(int(p.R)-int(bg.R))) > 25 {
						lo = min(lo, y)
						hi = max(hi, y)
					}
				}
			}
			if hi < 0 {
				t.Fatal("missing visible source ink", box)
			}
			return float64(lo+hi) / 2
		}
		if delta := inkCenter(icon) - inkCenter(source); math.Abs(delta) > 1 {
			t.Fatal("source icon differs from text ink center", dark, delta)
		}
		body, _ := ui.Find("原有内容已保留。")
		if delta := card.Y + card.H - body.Y - body.H; delta > 9 || delta < 7 {
			t.Fatal("bottom inset differs from top", card, body)
		}
		if !ok || source.Y-card.Y > 10 || source.Y-card.Y < 7 {
			t.Fatal("source row adds blank space above the toast", card, source)
		}
	}
}

func TestConversationFeedbackCompletedCancellationIsInformation(t *testing.T) {
	m := layoutModel()
	v := New(m, app.NewController(""), "")
	in := domain.HistoryChange{OperationID: "history-1", SessionID: "s"}
	m.HistoryActions["s"] = in
	v.cancelHistoryAction(in)
	job := <-v.Controller.Jobs
	if job.Method != "session.cancel-history" {
		t.Fatal(job.Method)
	}
	raw, _ := json.Marshal(domain.HistoryOperation{ID: "history-1", Input: in, State: "complete"})
	job.Apply(m, raw, nil)
	if len(m.Feedbacks) != 1 || m.Feedbacks[0].Kind != app.FeedbackInformation || m.Error != "" {
		t.Fatal("completed operation presented as a cancellation failure", m.Feedbacks, m.Error)
	}
}

func TestConversationFeedbackRecoveryCheckingKeepsPager(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 8
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "checking"}
	m.Details["s"] = d
	m.Snapshot.Sessions[0] = d.Session
	v := New(m, nil, "")
	v.draftSaveError = "save failed"
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click("下一个状态"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("上一个状态"); err != nil {
		t.Fatal("checking state trapped the status pager", err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("草稿尚未保存") {
		t.Fatal("could not return to save failure")
	}
}

func TestConversationFeedbackBackgroundHistoryDoesNotHideCurrentFailure(t *testing.T) {
	m := layoutModel()
	m.HistoryActions["other"] = domain.HistoryChange{OperationID: "background", SessionID: "other"}
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	m.Error = "current operation failed"
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("current operation failed") {
		t.Fatal("background history state swallowed current feedback")
	}
}

func TestConversationFeedbackSplitErrorDismissalSticks(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1600, 900)
	ui.Advance(time.Second)
	v.secondary.Model.Error = ""
	m.Error = "primary failure"
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("关闭消息"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		ui.Advance(time.Second / 30)
	}
	if ui.HasText("primary failure") {
		t.Fatal("dismissed primary error was recreated by the other pane")
	}
	if len(v.feedbackState().messages) != 1 {
		t.Fatal("one failure produced repeated history entries", len(v.feedbackState().messages))
	}
}

func TestConversationFeedbackRedactsCommonCredentials(t *testing.T) {
	for _, raw := range []string{`{"api_key":"example-secret"}`, `{"password":"prefix\\\"example-secret"}`, `{"access_token": "example-secret"}`, `password='example-secret'`, "Authorization: Basic ZXhhbXBsZQ==", "Authorization: Bearer example-secret", `{"Authorization":"Basic ZXhhbXBsZQ=="}`, "authenticate at https://auth.example.test/auth/example-secret?token=other-secret"} {
		cleaned := feedbackDetails(raw)
		if strings.Contains(cleaned, "example-secret") || strings.Contains(cleaned, "ZXhhbXBsZQ==") || strings.Contains(cleaned, "other-secret") {
			t.Errorf("credential survived: %s", cleaned)
		}
	}
}

func TestConversationFeedbackLateCancelKeepsOwningDevice(t *testing.T) {
	m := layoutModel()
	v := New(m, app.NewController(""), "")
	in := domain.HistoryChange{OperationID: "history-1", SessionID: "s"}
	m.HistoryActions["s"] = in
	v.cancelHistoryAction(in)
	job := <-v.Controller.Jobs
	m.Connections = append(m.Connections, app.DeviceConnection{ID: "air", DeviceID: "remote"})
	m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: "wb", DeviceID: "remote"})
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "sb", WorkspaceID: "wb"})
	m.WorkspaceID, m.SessionID = "wb", "sb"
	raw, _ := json.Marshal(domain.HistoryOperation{ID: in.OperationID, Input: in, State: "complete"})
	job.Apply(m, raw, nil)
	if len(m.Feedbacks) != 1 || m.Feedbacks[0].ConnectionID != "local" || m.Feedbacks[0].WorkspaceID != "w" || m.Feedbacks[0].SessionID != "s" {
		t.Fatal("late cancellation borrowed selected device", m.Feedbacks)
	}
}

func TestConversationFeedbackSaveFailureVisibleInNarrowSecondary(t *testing.T) {
	m := parallelModel()
	m.SecondarySessionID = "other"
	m.FocusSecondary = true
	v := New(m, nil, "")
	block := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(block, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	v.Saver = app.NewStateWriter(filepath.Join(block, "state.json"))
	defer v.Saver.Close()
	if err := v.Saver.Flush([]byte(`{}`)); err == nil {
		t.Fatal("fixture did not fail to persist")
	}
	ui := n.NewTester(v.View, 1000, 820)
	ui.Advance(time.Second)
	if !ui.HasText("草稿尚未保存") {
		t.Fatal("focused pane hid the shared save failure")
	}
	if err := ui.Click("复制草稿"); err != nil {
		t.Fatal(err)
	}
	if ui.Clipboard() != "right draft" || m.Drafts["session:s"] != "left draft" {
		t.Fatal("copy used the wrong pane draft", ui.Clipboard())
	}
}

func TestConversationFeedbackActionTextSharesBodyColumn(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		v := New(m, nil, "")
		ui := n.NewTester(v.View, 1100, 820)
		ui.SetDark(dark)
		m.ReportFeedback(app.Feedback{Title: "操作未完成", Body: "原有内容已保留", Details: strings.Repeat("test detail\n", 20)})
		ui.Frame()
		ui.Advance(time.Second)
		ui.Move(0, 0)
		card, _ := ui.Find("弹出消息")
		body, _ := ui.Find("test detail…\n原有内容已保留")
		action, _ := ui.Find("查看消息详情")
		inkLeft := func(r n.Rect) int {
			im := ui.Image()
			bg := im.RGBAAt(int(card.X+3), int(r.Y+r.H/2))
			left := im.Bounds().Max.X
			for y := int(r.Y); y < int(r.Y+r.H); y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					if p := im.RGBAAt(x, y); math.Abs(float64(int(p.R)-int(bg.R))) > 25 {
						left = min(left, x)
					}
				}
			}
			return left
		}
		if delta := inkLeft(action) - inkLeft(body); math.Abs(float64(delta)) > 1 {
			t.Fatal("action text adds a second left inset", dark, delta)
		}
	}
}
