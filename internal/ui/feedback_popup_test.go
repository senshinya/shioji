package ui

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func openBranchFeedback(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	v, ui, _ := recentFeedbackFixture(t, false, 1100)
	if err := ui.Click(recentOldTitle); err != nil {
		t.Fatal(err)
	}
	ui.Move(0, 0)
	ui.Advance(400 * time.Millisecond)
	return v, ui
}

func TestFeedbackPopupActionsShareCompactFooter(t *testing.T) {
	_, ui := openBranchFeedback(t)
	session, _ := ui.Find("查看会话")
	details, _ := ui.Find("查看消息详情")
	if math.Abs(float64(session.Y-details.Y)) > 1 || session.W > 120 || details.W > 140 {
		t.Fatal("popup actions stretch into separate menu rows", session, details)
	}
}

func TestFeedbackPopupDetailsStayAboveFooter(t *testing.T) {
	v, ui := openBranchFeedback(t)
	if err := ui.Click("查看消息详情"); err != nil {
		t.Fatal(err)
	}
	ui.Move(0, 0)
	ui.Advance(400 * time.Millisecond)
	details, _ := ui.Find(v.feedbackState().messages[0].Details)
	session, _ := ui.Find("查看会话")
	if details.H == 0 || details.Y+details.H > session.Y {
		t.Fatal("expanded content appears after the action footer", details, session)
	}
}

func TestFeedbackPopupLongBodyKeepsActionsVisibleAndContentReadable(t *testing.T) {
	v, ui := openBranchFeedback(t)
	f := v.feedbackState().messages[0].Feedback
	f.Body = strings.Repeat("测试内容用于核对长说明的滚动边界。\n", 60) + "说明末尾仍保留"
	v.Model.ReportFeedback(f)
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	toast, _ := ui.Find("弹出消息")
	session, _ := ui.Find("查看会话")
	if toast.H > 300 || session.Y+session.H > 820 {
		t.Fatal("long message pushes the action footer outside the popup", toast, session)
	}
	viewport, ok := ui.Find("消息说明")
	if !ok || viewport.H > 80 {
		t.Fatal("long body has no bounded reading area", viewport)
	}
	// Find reports painted, clipped text bounds. Verify retained content by
	// scrolling to its distinct final line while the footer stays stationary.
	textRegion := viewport
	textRegion.W -= 8 // Exclude the transient scrollbar thumb.
	before := contextPixels(ui, textRegion)
	ui.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 10000)
	ui.Advance(200 * time.Millisecond)
	if bytes.Equal(before, contextPixels(ui, textRegion)) {
		t.Fatal("long body does not scroll to its final content")
	}
	after, _ := ui.Find("查看会话")
	if math.Abs(float64(after.Y-session.Y)) > 1 {
		t.Fatal("reading the long body moves the action footer", session, after)
	}
}

func TestFeedbackPopupNoBodyKeepsEqualTopAndBottomInsets(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1100, 820)
	m.ReportFeedback(app.Feedback{ConnectionID: "local", WorkspaceID: "w", SessionID: "s", Title: "修改稿已复制", Kind: app.FeedbackSuccess})
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	toast, _ := ui.Find("弹出消息")
	source, _ := ui.Find("Example · Layout · 这台 Mac")
	title, _ := ui.Find("修改稿已复制")
	top, bottom := source.Y-toast.Y, toast.Y+toast.H-title.Y-title.H
	if math.Abs(float64(top-bottom)) > 1 {
		t.Fatal("an empty content group adds a bottom gap", top, bottom)
	}
}

// Captures the production popup, including real reversal of its disclosure motion.
func TestFeedbackPopupNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_POPUP_RECORDING")
	if dir == "" {
		t.Skip("opt-in native popup evidence")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	width := 1240
	if os.Getenv("SHIOJI_POPUP_NARROW") != "" {
		width = 920
	}
	v, ui, _ := recentFeedbackFixture(t, os.Getenv("SHIOJI_POPUP_DARK") != "", width)
	click := func(label string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(label, err)
		}
		ui.Move(0, 0)
	}
	click(recentOldTitle)
	ui.Advance(300 * time.Millisecond)
	for frame := 0; frame < 240; frame++ {
		switch frame {
		case 15, 38, 78, 135:
			click("查看消息详情")
		case 35, 75, 105:
			click("收起消息详情")
		case 165:
			click("关闭消息")
		case 195:
			click("最近消息")
		case 210:
			ui.Key(0, n.KeyDown)
		case 215:
			ui.Key(0, n.KeyEnter)
			ui.Move(0, 0)
		}
		ui.Advance(time.Second / 30)
		saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
	}
	if v.Model.SessionID != "other" || v.Model.Drafts["session:other"] != "right draft" || len(v.feedbackState().messages) != 2 {
		t.Fatal("popup recording changed current context")
	}
}
