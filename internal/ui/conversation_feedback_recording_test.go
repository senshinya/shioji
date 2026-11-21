package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

// Opt-in evidence uses the production MyGo View and deterministic local data.
// It does not contact devices, run an agent, or represent transport acceptance.
func TestConversationFeedbackNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_FEEDBACK_RECORDING")
	if dir == "" {
		t.Skip("opt-in native feedback evidence")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	m := layoutModel()
	m.WorkerMinor = 8
	m.Drafts[m.DraftKey()] = "检查提交回执，保留这段普通草稿。"
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "r", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "先核对哪一部分？", Options: []domain.Option{{Label: "提交回执", Description: "核对所属设备的稳定操作标识"}, {Label: "会话历史"}}}, {ID: "q2", Text: "需要保留哪些内容？", Custom: true, Options: []domain.Option{{Label: "草稿与附件"}, {Label: "全部记录"}}}}}}
	m.Details["s"] = d
	v := New(m, nil, "")
	width := 1240
	if os.Getenv("SHIOJI_FEEDBACK_NARROW") != "" {
		width = 920
	}
	ui := n.NewTester(v.View, width, 820)
	ui.SetDark(os.Getenv("SHIOJI_FEEDBACK_DARK") != "")
	ui.Advance(time.Second)
	m.ReportFeedback(app.Feedback{ID: "sample", ConnectionID: "local", WorkspaceID: "w", SessionID: "s", Title: "操作未完成", Body: "操作未能完成，原有内容已保留。", Details: "执行设备暂时无法完成本次操作。"})
	ui.Frame()
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "overview.png")
	if os.Getenv("SHIOJI_FEEDBACK_STILL") != "" {
		return
	}
	click := func(label string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(label, err)
		}
	}
	for frame := 0; frame < 390; frame++ {
		switch frame {
		case 15:
			click("关闭消息")
		case 30:
			click("提交回执")
		case 45:
			v.draftSaveError = "本地保存暂时失败"
			ui.Frame()
		case 70:
			click("展开会话状态")
		case 90:
			click("折叠会话状态")
		case 94:
			click("展开会话状态")
		case 120:
			click("下一个请求")
		case 145:
			click("上一个请求")
		case 170:
			v.draftSaveError = ""
			ui.Frame()
		case 195:
			d = m.Details["s"]
			d.Requests = nil
			d.Session.Status = "unknown"
			d.Session.Recovery = &domain.SessionRecovery{Phase: "attention", CanStopTasks: true, Tasks: []domain.RecoveryTask{{ID: "p", PID: 2418, State: "running", Command: "go test", ProcessCommand: "go test ./internal/transport"}}}
			m.Details["s"] = d
			m.Snapshot.Sessions[0] = d.Session
			ui.Frame()
		case 225:
			click("查看详情")
		case 250:
			click("收起详情")
		case 275:
			click("查看详情")
		case 300:
			click("重新核对回复")
		case 325:
			m.ReportFeedback(app.Feedback{Title: "修改稿已复制", Kind: app.FeedbackSuccess})
			ui.Frame()
		case 350:
			click("最近消息")
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
	if m.Drafts[m.DraftKey()] != "检查提交回执，保留这段普通草稿。" {
		t.Fatal("recording changed normal draft")
	}
	if v.recoveries["local/s"].Confirm != "" {
		t.Fatal("cancel did not restore recovery")
	}
}
