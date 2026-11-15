package ui

import (
	"bytes"
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

func TestRecentFeedbackClearAllKeepsDraftAndAcceptsNewMessages(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui, _ := recentFeedbackFixture(t, dark, 1100)
		if err := ui.Click("清空所有消息"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(400 * time.Millisecond)
		if !v.feedbackState().open || !ui.HasText("暂无消息") || len(v.feedbackState().messages) != 0 || v.feedbackState().replaying != "" {
			t.Fatal("clear did not leave the recent list empty and open")
		}
		if v.Model.SessionID != "other" || v.Model.Drafts["session:other"] != "right draft" {
			t.Fatal("clearing messages changed the selected session or draft")
		}
		ui.Click("最近消息")
		ui.Advance(300 * time.Millisecond)
		ui.Click("最近消息")
		ui.Advance(300 * time.Millisecond)
		if !ui.HasText("暂无消息") || ui.HasText(recentOldTitle) || ui.HasText(recentNewTitle) {
			t.Fatal("cleared messages returned after reopening")
		}
		v.Model.ReportFeedback(app.Feedback{ID: "after-clear", Title: "清空后新消息", Kind: app.FeedbackSuccess})
		ui.Frame()
		ui.Advance(300 * time.Millisecond)
		if !ui.HasText("清空后新消息") || ui.HasText("暂无消息") || len(v.feedbackState().messages) != 1 {
			t.Fatal("clearing prevents new feedback from appearing")
		}
	}
}

func TestRecentFeedbackClearPreservesActiveWait(t *testing.T) {
	v, ui, _ := recentFeedbackFixture(t, false, 1100)
	s := v.feedbackState()
	s.put(app.Feedback{ID: "active-wait", Title: "仍在准备"}, ui.Now(), true)
	s.replaying = feedbackKey(s.messages[0].Feedback)
	ui.Frame()
	if err := ui.Click("清空所有消息"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(400 * time.Millisecond)
	if len(s.messages) != 1 || !s.messages[0].waiting || s.messages[0].ID != "active-wait" || !ui.HasText("暂无消息") || s.replaying != "" {
		t.Fatal("clear discarded the active operation or retained history", s.messages)
	}
}

func TestRecentFeedbackClearFadesBeforeCollapsing(t *testing.T) {
	for _, dark := range []bool{false, true} {
		_, ui, _ := recentFeedbackFixture(t, dark, 1100)
		row, _ := ui.Find(recentOldTitle)
		if err := ui.Click("清空所有消息"); err != nil {
			t.Fatal(err)
		}
		ui.Move(0, 0)
		ui.Advance(40 * time.Millisecond)
		fading := contextPixels(ui, row)
		first, _ := ui.Find("最近消息列表")
		ui.Advance(40 * time.Millisecond)
		if bytes.Equal(fading, contextPixels(ui, row)) {
			t.Fatal("clearing removes messages before their exit can paint")
		}
		ui.Advance(100 * time.Millisecond)
		middle, _ := ui.Find("最近消息列表")
		ui.Advance(220 * time.Millisecond)
		end, _ := ui.Find("最近消息列表")
		if first.H <= middle.H || middle.H <= end.H || !ui.HasText("暂无消息") {
			t.Fatal("the list height snaps instead of collapsing after the fade", first, middle, end)
		}
	}
}

func TestRecentFeedbackClearMotionKeepsNewMessageAndDoesNotReplayOnReopen(t *testing.T) {
	v, ui, _ := recentFeedbackFixture(t, false, 1100)
	ui.Click("清空所有消息")
	ui.Advance(40 * time.Millisecond)
	v.Model.ReportFeedback(app.Feedback{ID: "older", ConnectionID: "local", WorkspaceID: "w", SessionID: "s", Title: "同来源的新结果", Kind: app.FeedbackSuccess})
	ui.Frame()
	if !ui.HasText("同来源的新结果") {
		t.Fatal("new feedback is hidden by an in-flight clear")
	}
	ui.Click("最近消息")
	ui.Advance(30 * time.Millisecond)
	ui.Click("最近消息")
	ui.Advance(300 * time.Millisecond)
	if ui.HasText(recentOldTitle) || ui.HasText(recentNewTitle) || !ui.HasText("同来源的新结果") || len(v.feedbackState().messages) != 1 {
		t.Fatal("reopening resurrects cleared feedback or removes the new result")
	}
}

func TestRecentFeedbackClearReduceMotion(t *testing.T) {
	_, ui, _ := recentFeedbackFixture(t, false, 1100)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Click("清空所有消息")
	ui.Advance(70 * time.Millisecond)
	first, _ := ui.Find("最近消息列表")
	ui.Advance(100 * time.Millisecond)
	end, _ := ui.Find("最近消息列表")
	if first.H != end.H || ui.HasText(recentOldTitle) || !ui.HasText("暂无消息") {
		t.Fatal("Reduce Motion still moves the cleared list", first, end)
	}
}

func TestRecentFeedbackClearLongScrolledList(t *testing.T) {
	v, ui, _ := recentFeedbackFixture(t, false, 1100)
	for i := 0; i < 20; i++ {
		v.Model.ReportFeedback(app.Feedback{ID: fmt.Sprint("long-", i), Title: fmt.Sprint("历史消息 ", i)})
	}
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	list, _ := ui.Find("最近消息列表")
	if list.H != 400 {
		t.Fatal("recent history is not bounded", list)
	}
	ui.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 10000)
	ui.Advance(300 * time.Millisecond)
	ui.Click("清空所有消息")
	ui.Advance(400 * time.Millisecond)
	list, _ = ui.Find("最近消息列表")
	if list.H != 36 || !ui.HasText("暂无消息") || len(v.feedbackState().messages) != 0 {
		t.Fatal("scrolled history does not collapse to the empty state", list)
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
	for frame := 0; frame < 300; frame++ {
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
		case 235:
			click("清空所有消息")
		case 250:
			if !ui.HasText("暂无消息") {
				t.Fatal("recording did not show the cleared list")
			}
			saveNativeSkillImage(t, ui, "empty.png")
		case 265:
			v.Model.ReportFeedback(app.Feedback{ID: "after-clear", Title: "清空后新消息", Body: "新操作结果仍会正常显示。", Kind: app.FeedbackSuccess})
			ui.Frame()
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
	if v.Model.SessionID != "other" || v.Model.Drafts["session:other"] != "right draft" || len(v.feedbackState().messages) != 1 || !ui.HasText("清空后新消息") {
		t.Fatal("recording changed source, draft, or history")
	}
}
