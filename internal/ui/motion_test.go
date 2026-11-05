package ui

import (
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestInteractiveControlsFadeTheirHoverFeedback(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, label := range []string{"新建会话", "待我处理", "搜索", "Layout", "当前设备", "Permission", "运行配置"} {
			t.Run(label+map[bool]string{false: "/light", true: "/dark"}[dark], func(t *testing.T) {
				ui := n.NewTester(New(layoutModel(), nil, "").View, 1440, 900)
				ui.SetDark(dark)
				r, ok := ui.Find(label)
				if !ok {
					t.Fatal("missing control", label)
				}
				x, y := int(r.X+r.W/2), int(r.Y+3)
				rest := ui.Image().RGBAAt(x, y)
				ui.Move(r.X+r.W/2, r.Y+r.H/2)
				ui.Advance(30 * time.Millisecond)
				entering := ui.Image().RGBAAt(x, y)
				ui.Advance(150 * time.Millisecond)
				hover := ui.Image().RGBAAt(x, y)
				if hover == rest {
					t.Fatal("hover provides no visible feedback")
				}
				if entering == rest || entering == hover {
					t.Fatal("hover feedback jumps instead of fading")
				}
				ui.Move(1400, 700)
				ui.Advance(180 * time.Millisecond)
				if got := ui.Image().RGBAAt(x, y); got != rest {
					t.Fatalf("hover exit did not restore the original surface: %v != %v", got, rest)
				}
			})
		}
	}
}

func TestPressAndReducedMotionFeedback(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		ui := n.NewTester(New(layoutModel(), nil, "").View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		r, _ := ui.Find("Layout")
		x, y := int(r.X+r.W/2), int(r.Y+3)
		ui.Move(r.X+r.W/2, r.Y+r.H/2)
		ui.Advance(180 * time.Millisecond)
		hover := ui.Image().RGBAAt(x, y)
		ui.Press(r.X+r.W/2, r.Y+r.H/2)
		ui.Advance(180 * time.Millisecond)
		if pressed := ui.Image().RGBAAt(x, y); pressed == hover {
			t.Fatal("press is indistinguishable from hover")
		}
		ui.Move(1400, 700)
		ui.Release(1400, 700)
		ui.Advance(180 * time.Millisecond)
		ui.Move(r.X+r.W/2, r.Y+r.H/2)
		ui.Advance(80 * time.Millisecond)
		if reduced && ui.Image().RGBAAt(x, y) != hover {
			t.Fatal("Reduce Motion feedback lasts longer than 80ms")
		}
	}
}

func TestComposerHoverAndFocusKeepItsBorder(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, home := range []bool{false, true} {
			m := layoutModel()
			if home {
				m.SessionID = ""
			}
			ui := n.NewTester(New(m, nil, "").View, 1440, 900)
			ui.SetDark(dark)
			r, _ := ui.Find("消息编辑器")
			x, y := int(r.X), int(r.Y+r.H/2)
			rest := ui.Image().RGBAAt(x, y)
			ui.Move(r.X+r.W/2, r.Y+8)
			ui.Advance(180 * time.Millisecond)
			if hover := ui.Image().RGBAAt(x, y); hover != rest {
				t.Fatal("composer hover changes border", dark, home, rest, hover)
			}
			if err := ui.Click("消息输入"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(180 * time.Millisecond)
			if focused := ui.Image().RGBAAt(x, y); focused != rest {
				t.Fatal("composer focus changes border", dark, home, rest, focused)
			}
			if !ui.Focused("消息输入") {
				t.Fatal("composer did not retain keyboard focus")
			}
			if got, _ := ui.Find("消息编辑器"); got != r {
				t.Fatal("focus changed composer geometry")
			}
		}
	}
}

func TestPopoverAcceptsEscapeDuringItsEntrance(t *testing.T) {
	m := layoutModel()
	m.Readiness.Permissions = []string{"read-only", "default"}
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if err := ui.Click("Permission"); err != nil {
		t.Fatal(err)
	}
	if !ui.HasText("按需请求批准") {
		t.Fatal("opening menu delayed its choices")
	}
	ui.Key(0, n.KeyEscape)
	if ui.HasText("按需请求批准") {
		t.Fatal("Escape waited for the entrance to finish")
	}
	if !ui.Focused("Permission") {
		t.Fatal("menu dismissal did not restore trigger focus")
	}
	ui.Advance(200 * time.Millisecond)
	if !ui.Focused("Permission") {
		t.Fatal("menu exit completion stole trigger focus")
	}
}

func TestRequestDisclosureKeepsDraftsAndComposerPosition(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "r", State: "pending", Kind: "questions", Title: "需要回答", Questions: []domain.Question{{ID: "q", Text: "Which next?", Custom: true}}}}
	m.Details["s"] = d
	m.Drafts["session:s"] = "ordinary draft"
	m.Answers["custom:s/r/q"] = "answer draft"
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	before, _ := ui.Find("消息编辑器")
	for i := 0; i < 2; i++ {
		if err := ui.Click("需要回答"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(70 * time.Millisecond)
		if got, _ := ui.Find("消息编辑器"); got != before {
			t.Fatalf("request disclosure moved the composer: %+v != %+v", got, before)
		}
	}
	ui.Advance(200 * time.Millisecond)
	if m.Drafts["session:s"] != "ordinary draft" || m.Answers["custom:s/r/q"] != "answer draft" {
		t.Fatal("interrupted disclosure discarded a draft")
	}
	if !ui.HasText("自定义答案") {
		t.Fatal("reversing disclosure did not restore the answer field")
	}
}

func TestDisabledControlDoesNotReactToHover(t *testing.T) {
	ui := n.NewTester(New(layoutModel(), nil, "").View, 1440, 900)
	r, _ := ui.Find("设置")
	x, y := int(r.X+r.W/2), int(r.Y+3)
	rest := ui.Image().RGBAAt(x, y)
	ui.Move(r.X+r.W/2, r.Y+r.H/2)
	ui.Advance(200 * time.Millisecond)
	if got := ui.Image().RGBAAt(x, y); got != rest {
		t.Fatal("disabled control shows an enabled hover affordance")
	}
}

func TestInspectorOpensContinuouslyAndRespectsReducedMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		ui := n.NewTester(New(layoutModel(), nil, "").View, 1440, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		if err := ui.Click("展开更改侧栏"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(70 * time.Millisecond)
		moving, ok := ui.Find("更改侧栏")
		if !ok {
			t.Fatal("inspector did not open")
		}
		if moving.Y != 0 || moving.H != 900 {
			t.Fatalf("inspector lost its full-height column during animation: %+v", moving)
		}
		history, _ := ui.Find("对话记录")
		if math.Abs(float64(history.X+history.W-moving.X)) > .5 {
			t.Fatalf("conversation crosses the animated inspector divider: history=%+v inspector=%+v", history, moving)
		}
		if !reduced && (moving.W <= 0 || moving.W >= 280) {
			t.Fatalf("inspector jumped to its final width: %+v", moving)
		}
		if reduced && moving.W != 280 {
			t.Fatalf("Reduce Motion still interpolates pane size: %+v", moving)
		}
		ui.Advance(200 * time.Millisecond)
		settled, _ := ui.Find("更改侧栏")
		if settled.W != 280 {
			t.Fatalf("inspector did not settle at its frozen width: %+v", settled)
		}
	}
}

func TestInspectorReversesFromItsCurrentPosition(t *testing.T) {
	m := layoutModel()
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if err := ui.Click("展开更改侧栏"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(70 * time.Millisecond)
	for _, open := range []bool{false, true, false} {
		// Advance may stop between display frames; compare at the same time.
		ui.Frame()
		before, _ := ui.Find("当前设备")
		m.Inspectors["s"] = open
		ui.Frame()
		after, _ := ui.Find("当前设备")
		if math.Abs(float64(before.X-after.X)) > 1 {
			t.Fatalf("reversal jumped the conversation toolbar: %+v -> %+v", before, after)
		}
		ui.Advance(50 * time.Millisecond)
	}
	ui.Advance(220 * time.Millisecond)
	if ui.HasText("刷新更改") {
		t.Fatal("closed inspector retained an interactive control")
	}
}

func TestNarrowInspectorSlidesFromWindowEdge(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		ui := n.NewTester(New(layoutModel(), nil, "").View, 950, 900)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		if err := ui.Click("展开更改侧栏"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(70 * time.Millisecond)
		moving, _ := ui.Find("更改侧栏")
		// Find reports the visible box, clipped where the drawer enters.
		if math.Abs(float64(moving.W+moving.X-670-280)) > .5 || moving.H != 900 || moving.Y != 0 {
			t.Fatalf("drawer resized its contents during entrance: %+v", moving)
		}
		if !reduced && moving.X <= 670 || reduced && moving.X != 670 {
			t.Fatalf("drawer does not follow the window edge / Reduce Motion: %+v", moving)
		}
		ui.Advance(220 * time.Millisecond)
		settled, _ := ui.Find("更改侧栏")
		if settled.X != 670 || settled.W != 280 {
			t.Fatalf("drawer did not settle at its frozen bounds: %+v", settled)
		}
	}
}

func TestFileDiffNavigationKeepsInspectorWidthAndConversationStable(t *testing.T) {
	m := layoutModel()
	m.Inspectors["s"] = true
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	before, _ := ui.Find("更改侧栏")
	history, _ := ui.Find("对话记录")
	v.changesFor("s").Path = "file.go"
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	panel, _ := ui.Find("更改侧栏")
	after, _ := ui.Find("对话记录")
	if panel != before || history != after {
		t.Fatal("opening a file resizes the sidebar or conversation", before, panel, history, after)
	}
	if _, extra := ui.Find("文件差异阅读器"); extra {
		t.Fatal("opening a file creates another inspector")
	}
	if err := ui.Click("返回更改列表"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(250 * time.Millisecond)
	panel, _ = ui.Find("更改侧栏")
	if panel != before {
		t.Fatal("Back changes inspector bounds", before, panel)
	}
}
