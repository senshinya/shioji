package ui

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestConfiguredComposerBindingDispatchesQueueInsteadOfSteer(t *testing.T) {
	v, ui := nativeQueueFixture()
	v.Model.Preferences.Shortcuts = app.ComposerShortcuts{Send: "Cmd+Enter", Newline: "Enter", Steer: "Alt+Enter"}
	ui.Frame()
	ui.Click("消息输入")
	ui.Type("next task")
	ui.Key(n.Cmd, n.KeyEnter)
	var methods []string
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; strings.HasPrefix(job.Method, "turn.") {
			methods = append(methods, job.Method)
		}
	}
	if len(methods) != 1 || methods[0] != "turn.enqueue" {
		t.Fatal("custom send binding did not reach queue", methods)
	}
}

func TestConfiguredEnterNewlinePreservesTextSelectionAndDoesNotDispatch(t *testing.T) {
	v, ui := nativeQueueFixture()
	v.Model.Preferences.Shortcuts = app.ComposerShortcuts{Send: "Cmd+Enter", Newline: "Enter", Steer: "Alt+Enter"}
	ui.Frame()
	ui.Click("消息输入")
	ui.Type("replace this")
	ui.Key(n.Cmd, n.KeyA)
	ui.Key(0, n.KeyEnter)
	if got := v.Model.Drafts["session:s"]; got != "\n" {
		t.Fatalf("newline lost native selection: %q", got)
	}
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; strings.HasPrefix(job.Method, "turn.") {
			t.Fatal("newline dispatched work", job.Method)
		}
	}
}

func TestSettingsChangesReachMainWindowAndRejectConflictingShortcuts(t *testing.T) {
	v := New(layoutModel(), nil, "")
	s := NewSettings(v)
	ui := n.NewTester(s.View, 968, 756)
	if err := ui.Click("外观深色"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(250 * time.Millisecond)
	if v.Model.Preferences.Appearance != "dark" {
		t.Fatal("theme selection stayed inside preview")
	}
	main := n.NewTester(v.View, 1200, 820)
	if main.Image().RGBAAt(1190, 400).R > 30 {
		t.Fatal("theme did not reach main view")
	}
	ui.Click("消息输入")
	ui.Advance(250 * time.Millisecond)
	ui.Click("修改发送或加入队列按键")
	ui.Advance(150 * time.Millisecond)
	if err := ui.Click("⌘ ↩"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(150 * time.Millisecond)
	if v.Model.Preferences.Shortcuts.Send != "Enter" || !ui.HasText("此按键已用于") {
		t.Fatal("conflicting shortcut was accepted or has no feedback")
	}
	ui.Click("修改发送或加入队列按键")
	ui.Advance(150 * time.Millisecond)
	ui.Click("⌥ ↩")
	ui.Advance(150 * time.Millisecond)
	if v.Model.Preferences.Shortcuts.Send != "Alt+Enter" {
		t.Fatal("valid shortcut change not applied")
	}
	ui.Click("恢复默认按键")
	if v.Model.Preferences.Shortcuts.Send != "Enter" {
		t.Fatal("reset did not restore send binding")
	}
}

func TestSettingsSearchFindsAnotherCategoryWithoutTouchingDraft(t *testing.T) {
	m := layoutModel()
	m.Drafts["session:s"] = "unsent draft"
	s := NewSettings(New(m, nil, ""))
	ui := n.NewTester(s.View, 968, 756)
	ui.Click("搜索设置")
	ui.Type("换行")
	ui.Advance(250 * time.Millisecond)
	if err := ui.Click("消息输入 · 换行"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("立即介入当前轮") || m.Drafts["session:s"] != "unsent draft" {
		t.Fatal("search did not navigate independently from conversation")
	}
}

func TestSettingsShowsSaveFailureWithoutRenderingConversation(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(parent, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	v := New(layoutModel(), nil, filepath.Join(parent, "state.json"))
	defer v.Saver.Close()
	s := NewSettings(v)
	ui := n.NewTester(s.View, 968, 756)
	ui.Click("外观深色")
	if err := v.Saver.FlushLatest(); err == nil {
		t.Fatal("save unexpectedly succeeded through a file")
	}
	ui.Frame()
	if !ui.HasText("无法保存设置") {
		t.Fatal("active settings window hid the disk write failure")
	}
}

func TestNotificationGrantCommitsAndQueuesSaveWithoutSettingsFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	v := New(layoutModel(), nil, path)
	defer v.Saver.Close()
	s := NewSettings(v)
	s.finishNotificationTest(true, nil)
	if !v.Model.Preferences.Notifications.Enabled {
		t.Fatal("permission result waited for rendering")
	}
	if err := v.Saver.FlushLatest(); err != nil {
		t.Fatal(err)
	}
	restored := app.NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if !restored.Preferences.Notifications.Enabled {
		t.Fatal("permission grant was not queued for persistence")
	}
}

func TestSettingsCanDisableDefaultForegroundNotifications(t *testing.T) {
	v := New(layoutModel(), nil, "")
	s := NewSettings(v)
	ui := n.NewTester(s.View, 968, 756)
	ui.Click("通知")
	if !v.Model.Preferences.Notifications.Foreground {
		t.Fatal("foreground preference did not default on")
	}
	if err := ui.Click("通知：前台也通知"); err != nil {
		t.Fatal(err)
	}
	if v.Model.Preferences.Notifications.Foreground {
		t.Fatal("custom switch failed to update its bound preference")
	}
}

func TestClientAppearanceAndReplySizeApplyToVisibleConversation(t *testing.T) {
	m := layoutModel()
	m.Preferences.Appearance = "dark"
	m.Preferences.ReplySize = 18
	m.Preferences.CodeFont = "Menlo"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	r, _ := ui.Find("Aligned reply")
	if r.H < 29 {
		t.Fatal("reply size did not affect rendered text", r)
	}
	im := ui.Image()
	pixel := im.RGBAAt(1190, 400)
	if pixel.R > 30 || pixel.G > 30 || pixel.B > 30 {
		t.Fatal("dark appearance did not reach the main window", pixel)
	}
}

func TestClientReduceMotionSettlesQueueWithoutSpatialAnimation(t *testing.T) {
	v, ui := nativeQueueFixture()
	v.Model.Preferences.ReduceMotion = true
	ui.Frame()
	ui.Click("待发送 2")
	ui.Advance(time.Millisecond)
	first, _ := ui.Find("消息编辑区")
	ui.Advance(300 * time.Millisecond)
	last, _ := ui.Find("消息编辑区")
	if math.Abs(float64(first.H-last.H)) > 1 {
		t.Fatal("client reduce-motion choice left queue moving", first, last)
	}
}
