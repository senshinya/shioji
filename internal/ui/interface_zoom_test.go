package ui

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestZoomToastUpdatesInPlaceExpiresAndShowsReset(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("消息输入")
	ui.Type("保留草稿")
	ui.Wheel(900, 300, 0, -40, n.Cmd)
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("缩放 111%") {
		t.Fatal("zoom gesture has no percentage toast")
	}
	ui.Advance(time.Second)
	ui.Wheel(900, 300, 0, -40, n.Cmd)
	ui.Advance(500 * time.Millisecond)
	count := 0
	for _, text := range ui.Texts() {
		if strings.HasPrefix(text, "缩放 ") {
			count++
		}
	}
	if count != 1 || !ui.HasText("缩放 122%") {
		t.Fatal("continuous zoom stacked toasts or kept the old percentage", ui.Texts())
	}
	ui.Type("继续")
	if v.Model.Drafts["session:s"] != "保留草稿继续" {
		t.Fatal("toast stole native editor focus")
	}
	ui.Advance(2 * time.Second)
	if ui.HasText("缩放 122%") {
		t.Fatal("percentage stayed visible after zooming stopped")
	}
	ui.Key(n.Cmd, n.Key0)
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("缩放 100%") {
		t.Fatal("reset did not report the restored percentage")
	}
	if len(v.feedbackState().messages) != 0 {
		t.Fatal("zoom polluted the operation message history")
	}
}

func TestRestoredZoomAndOrdinaryScrollingDoNotShowToast(t *testing.T) {
	m := layoutModel()
	m.Preferences.InterfaceZoom = 1.25
	ui := n.NewTester(New(m, nil, "").View, 1240, 820)
	ui.Scroll(900, 300, 0, -40)
	ui.Advance(250 * time.Millisecond)
	for _, text := range ui.Texts() {
		if strings.HasPrefix(text, "缩放 ") {
			t.Fatal("restoring preferences or ordinary scrolling triggered a zoom toast")
		}
	}
}

func TestCommandWheelScalesConversationChromeAndKeepsComposerEditing(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	before, _ := ui.Find("Aligned reply")
	input, _ := ui.Find("消息输入")
	ui.Click("消息输入")
	ui.Type("保留草稿 draft")
	// Over the native editor, the gesture must reach zoom before it scrolls.
	ui.Wheel(input.X+20, input.Y+10, 0, -40, n.Cmd)
	ui.Advance(time.Second)
	after, _ := ui.Find("Aligned reply")
	if after.H <= before.H*1.05 {
		t.Fatal("Cmd+wheel did not scale the actual conversation", before, after)
	}
	composer, _ := ui.Find("消息输入")
	if composer.H <= input.H*1.05 {
		t.Fatal("zoom changed prose without scaling the input", input, composer)
	}
	ui.Type("继续")
	if got := v.Model.Drafts["session:s"]; got != "保留草稿 draft继续" {
		t.Fatal("zoom lost the native editor focus or draft", got)
	}
	ui.Key(n.Cmd, n.Key0)
	ui.Advance(time.Second)
	reset, _ := ui.Find("Aligned reply")
	if math.Abs(float64(reset.H-before.H)) > .5 {
		t.Fatal("Cmd+0 did not restore the default size", before, reset)
	}
}

func TestCommandPlusAndMinusZoomWhileComposerHasFocus(t *testing.T) {
	v := New(layoutModel(), nil, "")
	var zoom float32
	ui := n.NewTester(func(c *n.Context) { v.View(c); zoom = c.Zoom() }, 1240, 820)
	ui.Click("消息输入")
	ui.Type("保留草稿")
	for _, press := range []struct {
		mods n.Modifiers
		key  n.Key
		want float32
	}{
		{n.Cmd, n.KeyEqual, 1.05},
		{n.Cmd | n.Shift, n.KeyEqual, 1.1},
		{n.Cmd, n.KeyMinus, 1.05},
		{n.Cmd, n.Key0, 1},
	} {
		ui.Key(press.mods, press.key)
		if math.Abs(float64(zoom-press.want)) > .001 {
			t.Fatalf("shortcut %v+%v zoom = %g, want %g", press.mods, press.key, zoom, press.want)
		}
	}
	ui.Type("继续")
	if got := v.Model.Drafts["session:s"]; got != "保留草稿继续" {
		t.Fatal("zoom shortcuts lost the editor focus or draft", got)
	}
}

func TestCommandScrollIsReversibleBoundedAndIndependentOfCurrentZoom(t *testing.T) {
	v := New(layoutModel(), nil, "")
	var zoom float32
	ui := n.NewTester(func(c *n.Context) { v.View(c); zoom = c.Zoom() }, 1240, 820)
	ui.Scroll(900, 300, 0, -80, n.Cmd)
	if zoom <= 1 {
		t.Fatal("precise Cmd+scroll did not zoom in", zoom)
	}
	ui.Scroll(900, 300, 0, 80, n.Cmd)
	if math.Abs(float64(zoom-1)) > .001 {
		t.Fatal("reverse gesture depends on the current zoom", zoom)
	}
	ui.Wheel(900, 300, 0, -10000, n.Cmd)
	if zoom != 1.5 {
		t.Fatal("upper zoom bound was not enforced", zoom)
	}
	ui.Wheel(900, 300, 0, 10000, n.Cmd)
	if zoom != .75 {
		t.Fatal("lower zoom bound was not enforced", zoom)
	}
	ui.Scroll(900, 300, 0, -40)
	ui.Scroll(900, 300, 40, 0, n.Cmd)
	ui.Scroll(900, 300, 0, -40, n.Ctrl)
	if zoom != .75 {
		t.Fatal("ordinary, horizontal or Ctrl scroll changed zoom", zoom)
	}
	if !v.interfaceScroll(n.InputEvent{Kind: n.InputScroll, DY: -40, Mods: n.Cmd, Momentum: true}) {
		t.Fatal("Cmd scroll inertia leaked into the transcript")
	}
	ui.Frame()
	if zoom != .75 {
		t.Fatal("inertia kept changing scale after the gesture ended", zoom)
	}
}

func TestOppositeScrollEventsBeforeNextFrameRestoreZoom(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	// Several wheel callbacks can precede the next drawn frame. Both events
	// still use the previous frame's coordinates, here at 100%.
	v.interfaceScroll(n.InputEvent{Kind: n.InputScroll, DY: -80, Mods: n.Cmd, Precise: true})
	v.interfaceScroll(n.InputEvent{Kind: n.InputScroll, DY: 80, Mods: n.Cmd, Precise: true})
	ui.Frame()
	if got := v.Model.Preferences.InterfaceZoom; math.Abs(float64(got-1)) > .001 {
		t.Fatal("batched opposite wheel callbacks drifted before rendering", got)
	}
}

func TestInterfaceZoomSurvivesClientRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	v := New(layoutModel(), nil, path)
	defer v.Saver.Close()
	var zoom float32
	ui := n.NewTester(func(c *n.Context) { v.View(c); zoom = c.Zoom() }, 1240, 820)
	ui.Wheel(900, 300, 0, -40, n.Cmd)
	if zoom <= 1 {
		t.Fatal("missing zoom before restart")
	}
	if err := v.Flush(); err != nil {
		t.Fatal(err)
	}
	restored := app.NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	reopened := New(restored, nil, "")
	var actual float32
	n.NewTester(func(c *n.Context) { reopened.View(c); actual = c.Zoom() }, 1240, 820)
	if math.Abs(float64(actual-zoom)) > .001 {
		t.Fatal("interface scale was lost at restart", zoom, actual)
	}
}

func TestZoomedNarrowConversationKeepsSendAndPermissionVisible(t *testing.T) {
	m := layoutModel()
	m.Drafts["session:s"] = "继续工作"
	cfg := m.Config()
	cfg.Model, cfg.Effort = "gpt-6.1-sol", "medium"
	m.SetConfig(m.DraftKey(), cfg)
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 850, 650)
	ui.Wheel(800, 300, 0, -1000, n.Cmd)
	ui.Advance(time.Second)
	for _, label := range []string{"发送", "权限", "运行配置", "消息输入"} {
		r, ok := ui.Find(label)
		if !ok || r.W < 12 || r.X < 0 || r.X+r.W > 850.5 {
			t.Errorf("zoom clipped %s: %+v", label, r)
		}
	}
}
