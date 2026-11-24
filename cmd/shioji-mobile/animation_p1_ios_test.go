package main

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/conversationui"
	"shioji/internal/domain"
)

func TestMobileSplitColumnHasNoDoubleEasingWithSizeTransition(t *testing.T) {
	showChanges := false
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			splitColumn(c.Key("primary-transcript"), true, func() {
				ui.Box(c.Key("left-body")).Fill().Label("left-pane")
			})
			target := float32(0)
			if showChanges {
				target = 300
			}
			panel := ui.Column(c.Key("changes-panel")).Clip().Shrink(0).HeightPercent(100).Label("changes-panel")
			panel.Width(panel.AnimateTo("changes-panel-width", target, mobileLayoutMotion))
		})
	}, 800, 400)
	tester.Advance(time.Second)

	showChanges = true
	tester.Frame()
	tester.Advance(120 * time.Millisecond)
	left, okL := tester.Find("left-pane")
	panel, okR := tester.Find("changes-panel")
	if !okL || !okR {
		t.Fatalf("missing split panes mid-animation: left=%v panel=%v", okL, okR)
	}
	// With mobileLayoutMotion as the single geometry owner, left pane and changes-panel meet seamlessly.
	// Stacking a 200ms Size/Position transition on splitColumn makes left-pane lag behind changes-panel.
	if gap := math.Abs(float64((left.X + left.W) - panel.X)); gap > 1.5 {
		t.Fatalf("splitColumn double easing caused seam drift of %g pt: left=%+v panel=%+v", gap, left, panel)
	}
}

func TestMobileComposerExtraHeightTracksProgressWithoutLag(t *testing.T) {
	draft := &mobileDraftEditor{inputBounds: ui.Rect{W: 300, H: 40}}
	multiline := strings.Repeat("多行草稿测试文本内容，用于触发输入区额外高度。\n", 4)
	progress := float32(0)
	var extra float32

	tester := ui.NewTester(func(c *ui.Context) {
		surface := ui.Column(c.Key("surface")).FillWidth()
		extra = mobileComposerExtraHeight(c, surface, draft, multiline, 40, progress, false)
	}, 360, 200)
	tester.Advance(time.Second)
	if extra != 0 {
		t.Fatalf("at progress 0, expected extra=0, got %g", extra)
	}

	// Simulate the outer composer controls progress reaching 1 over two frames (32ms).
	progress = 0.5
	tester.Frame()
	tester.Advance(16 * time.Millisecond)
	progress = 1.0
	tester.Frame()
	tester.Advance(16 * time.Millisecond)

	// Because the multiline text did not change, extra must reach full 26pt in lockstep with progress
	// instead of restarting a 180ms EaseOut curve every frame.
	if extra < 25.5 {
		t.Fatalf("mobileComposerExtraHeight lagged behind progress=1 due to double easing: got %g, want 26", extra)
	}
}

func TestMobileSessionGroupFoldDoesNotTriggerSiblingPositionLag(t *testing.T) {
	m := app.NewModel()
	a := &mobileApp{model: m}
	groups := []mobileSessionGroup{
		{
			key:   "g1",
			title: "工作区一",
			rows: []mobileSessionRow{
				{workspace: domain.Workspace{ID: "w1", DeviceID: "d1"}, session: domain.Session{ID: "s1", WorkspaceID: "w1", Title: "会话一"}},
				{workspace: domain.Workspace{ID: "w1", DeviceID: "d1"}, session: domain.Session{ID: "s2", WorkspaceID: "w1", Title: "会话二"}},
				{workspace: domain.Workspace{ID: "w1", DeviceID: "d1"}, session: domain.Session{ID: "s3", WorkspaceID: "w1", Title: "会话三"}},
			},
		},
		{
			key:   "g2",
			title: "工作区二",
			rows: []mobileSessionRow{
				{workspace: domain.Workspace{ID: "w2", DeviceID: "d1"}, session: domain.Session{ID: "s4", WorkspaceID: "w2", Title: "会话四"}},
			},
		},
	}
	menu := contextMenuFrame{}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c.Key("groups")).FillWidth().PaddingX(16).Children(func() {
				for _, group := range groups {
					a.sessionGroup(c, &menu, group)
				}
			})
		})
	}, 390, 700)
	tester.Advance(time.Second)

	g1Start, _ := tester.Find("工作区一")
	g2Start, _ := tester.Find("工作区二")
	if err := tester.Click("工作区一"); err != nil {
		t.Fatal(err)
	}
	// CollapsibleBase takes 250ms. Advance 260ms so group 1 has completely collapsed;
	// if group 2 has a secondary 240ms Position transition restarted each frame, it will
	// still be lagging far below group 1 and still requesting animation frames.
	tester.Advance(260 * time.Millisecond)
	g2After, ok := tester.Find("工作区二")
	if !ok {
		t.Fatal("missing second group header")
	}
	tester.Advance(time.Second)
	g2Settled, _ := tester.Find("工作区二")
	if math.Abs(float64(g2After.Y-g2Settled.Y)) > 2 {
		t.Fatalf("second sessionGroup lagged past CollapsibleBase 250ms due to double Position transition: start=%g at260ms=%g settled=%g (g1=%g)",
			g2Start.Y, g2After.Y, g2Settled.Y, g1Start.Y)
	}
}

func TestMobileToolResultPanelClearsListFollowAnimationDuringDisclosure(t *testing.T) {
	m := app.NewModel()
	session := domain.Session{ID: "s1", WorkspaceID: "w1"}
	item := domain.Item{ID: "tool-1", Kind: "activity", Name: "读取文件", Text: "文件内容输出第一行\n第二行\n第三行", Final: true}
	open := false
	a := &mobileApp{model: m}
	list := ui.ListState{FollowEnd: true}
	var sawFollowCleared bool

	tester := ui.NewTester(func(c *ui.Context) {
		list.FollowAnimation = &conversationui.TranscriptFollowAnimation
		ui.List(c, &list, 1, func(int) {
			a.toolItem(c.Key("tool-row"), nil, session, item, &open, nil, false, &list)
		}).Fill()
		if open && list.FollowAnimation == nil {
			sawFollowCleared = true
		}
	}, 360, 400)
	tester.Advance(time.Second)

	open = true
	tester.Frame()
	tester.Advance(40 * time.Millisecond)
	if !sawFollowCleared {
		t.Fatal("toolItem disclosure did not clear list.FollowAnimation while animating its height")
	}
}

func TestMobilePanelTransitionFadesConditionalPanelInAndOut(t *testing.T) {
	show := false
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
			if show {
				mobileInlinePanel(c.Key("cond-panel")).Size(120, 40).Background(ui.Hex("#18181b"))
			}
		})
	}, 200, 120)
	tester.Advance(time.Second)

	show = true
	tester.Frame()
	tester.Advance(45 * time.Millisecond)
	midPending := tester.FramePending()
	midPx := tester.Image().RGBAAt(40, 20)
	tester.Advance(time.Second)
	endPx := tester.Image().RGBAAt(40, 20)

	if !midPending || midPx.R <= endPx.R+15 {
		t.Fatalf("mobilePanelTransition snapped in without Enter fade: midPending=%v mid=%v end=%v", midPending, midPx, endPx)
	}
}
