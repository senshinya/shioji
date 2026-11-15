package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

const recentOldTitle = "分支切换暂未完成"
const recentNewTitle = "工作区连接已恢复"

func recentFeedbackFixture(t *testing.T, dark bool, width int) (*View, *n.Tester, string) {
	t.Helper()
	m := parallelModel()
	m.SessionID = "other"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, width, 820)
	ui.SetDark(dark)
	ui.Advance(time.Second)
	body := "工作目录仍有改动。当前分支与草稿已保留。"
	m.ReportFeedback(app.Feedback{ID: "older", ConnectionID: "local", WorkspaceID: "w", SessionID: "s", Title: recentOldTitle, Body: body, Details: "本次分支切换未执行。请先检查工作区状态。"})
	m.ReportFeedback(app.Feedback{ID: "newer", ConnectionID: "local", WorkspaceID: "w", SessionID: "other", Title: recentNewTitle, Body: "原设备上的会话已重新连接，草稿没有自动发送。", Kind: app.FeedbackInformation})
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if err := ui.Click("最近消息"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	return v, ui, body
}

func TestRecentFeedbackListOnlyShowsBriefRows(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui, _ := recentFeedbackFixture(t, dark, 1100)
		body := "工作目录中存在尚未提交的更改。" + strings.Repeat("长文回归只用于验证缩略内容不会展开。", 12)
		f := v.feedbackState().messages[0].Feedback
		f.Body = body
		v.Model.ReportFeedback(f)
		ui.Frame()
		ui.Advance(300 * time.Millisecond)
		r, ok := ui.Find(body)
		if !ok || r.H > 22 {
			t.Fatal("recent list expands the complete message instead of a brief preview", dark, r)
		}
		for _, label := range []string{"查看记录详情", "查看会话"} {
			if ui.HasText(label) {
				t.Fatal("recent row exposes detail actions", label)
			}
		}
	}
}

func TestRecentFeedbackListReplaysSelectedMessageWithoutNavigationOrDuplication(t *testing.T) {
	v, ui, _ := recentFeedbackFixture(t, false, 1100)
	if err := ui.Click(recentOldTitle); err != nil {
		t.Fatal(err)
	}
	ui.Move(0, 0)
	ui.Advance(400 * time.Millisecond)
	if v.feedbackState().open || !ui.HasText(recentOldTitle) || ui.HasText(recentNewTitle) {
		t.Fatal("selection did not reopen the selected older message")
	}
	if v.Model.SessionID != "other" || v.Model.Drafts["session:other"] != "right draft" {
		t.Fatal("replaying a message navigated or changed the current draft")
	}
	messages := v.feedbackState().messages
	if len(messages) != 2 || messages[0].ID != "older" || messages[1].ID != "newer" {
		t.Fatal("replaying duplicated or reordered history", messages)
	}
	if !ui.HasText("Example · Layout · 这台 Mac") {
		t.Fatal("replayed message lost its original scope")
	}
}

func TestRecentFeedbackKeyboardReplaysSelectedRow(t *testing.T) {
	v, ui, _ := recentFeedbackFixture(t, false, 1100)
	ui.Key(0, n.KeyDown)
	ui.Key(0, n.KeyEnter)
	ui.Move(0, 0)
	ui.Advance(400 * time.Millisecond)
	if v.feedbackState().open || !ui.HasText(recentOldTitle) || ui.HasText(recentNewTitle) {
		t.Fatal("keyboard selection did not replay the selected older row")
	}
}

func TestRecentFeedbackReplayRestartsExpiredSuccess(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	ui.Advance(time.Second)
	m.ReportFeedback(app.Feedback{ID: "copied", Title: "修改稿已复制", Kind: app.FeedbackSuccess})
	ui.Frame()
	ui.Move(0, 0)
	ui.Advance(5 * time.Second)
	if ui.HasText("修改稿已复制") {
		t.Fatal("initial success did not expire")
	}
	m.ReportFeedback(app.Feedback{ID: "failure", Title: "操作暂未完成"})
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if err := ui.Click("最近消息"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if err := ui.Click("修改稿已复制"); err != nil {
		t.Fatal(err)
	}
	ui.Move(0, 0)
	ui.Advance(2 * time.Second)
	if !ui.HasText("修改稿已复制") || v.feedbackState().open {
		t.Fatal("expired success was not replayed with a new lifetime")
	}
	ui.Advance(3 * time.Second)
	if ui.HasText("修改稿已复制") {
		t.Fatal("replayed success did not expire normally")
	}
}

// Native evidence uses production View with the same explicit local fixtures.
func TestRecentFeedbackNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_RECENT_RECORDING")
	if dir == "" {
		t.Skip("opt-in native recent-message recording")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	width := 1240
	if os.Getenv("SHIOJI_RECENT_NARROW") != "" {
		width = 920
	}
	v, ui, _ := recentFeedbackFixture(t, os.Getenv("SHIOJI_RECENT_DARK") != "", width)
	saveNativeSkillImage(t, ui, "overview.png")
	if os.Getenv("SHIOJI_RECENT_STILL") != "" {
		return
	}
	click := func(label string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(label, err)
		}
	}
	for frame := 0; frame < 240; frame++ {
		switch frame {
		case 30:
			click(recentOldTitle)
		case 60:
			if v.feedbackState().open {
				t.Fatal("row did not replay message")
			}
			click("查看消息详情")
		case 95:
			click("关闭消息")
		case 120:
			click("最近消息")
		case 150:
			ui.Key(0, n.KeyDown)
		case 165:
			ui.Key(0, n.KeyEnter)
		case 190:
			click("关闭消息")
		case 210:
			click("最近消息")
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
	if v.Model.SessionID != "other" || v.Model.Drafts["session:other"] != "right draft" || len(v.feedbackState().messages) != 2 {
		t.Fatal("recording changed source, draft, or history")
	}
}
