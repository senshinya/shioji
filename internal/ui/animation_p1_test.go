package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestInspectorToggleSingleColumnHasNoPrimaryDoubleEasing(t *testing.T) {
	m := layoutModel()
	m.InspectorWidth = 360
	m.Inspectors["s"] = false
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)

	m.Inspectors["s"] = true
	ui.Frame()
	// Both conversation-pane and review-column use 200ms transitions.
	// If primary-conversation stacks a second 200ms Size transition on top of conversation-pane,
	// it restarts on the final frames of conversation-pane and is STILL animating at 230ms!
	ui.Advance(230 * time.Millisecond)
	at230, ok := ui.Find("左侧会话")
	pendingAt230 := ui.FramePending()
	ui.Advance(time.Second)
	settled, _ := ui.Find("左侧会话")
	if !ok || pendingAt230 || math.Abs(float64(at230.W-settled.W)) > 1 {
		t.Fatalf("primary-conversation kept animating past 200ms due to double Size easing: at230=%+v settled=%+v pendingAt230=%v",
			at230, settled, pendingAt230)
	}
}

func TestWorkInspectorCollapseRetainsBottomContentAndAvoidsTopDoubleEasing(t *testing.T) {
	v, ui := dockingUI(t)
	v.Model.InspectorLayout.Groups = []app.InspectorGroup{
		{Views: []string{"changes", "agents"}, Selected: "changes"},
		{Views: []string{"tasks", "background"}, Selected: "tasks"},
	}
	ui.Frame()
	ui.Advance(time.Second)

	// Collapse the bottom split by merging groups into one.
	v.Model.InspectorLayout.Groups = []app.InspectorGroup{
		{Views: []string{"changes", "agents", "tasks", "background"}, Selected: "changes"},
	}
	v.changesFor("s").WorkGroups = app.CloneInspectorGroups(v.Model.InspectorLayout.Groups)
	ui.Frame()
	ui.Advance(60 * time.Millisecond)

	// Top pane must expand directly with the collapsing bottom split (no 200ms Size double-easing lag).
	// And the bottom region (inert while closing) must still paint non-background pixels rather than turning blank.
	topPane, okTop := ui.Find("文件更改区域")
	inspector, okInsp := ui.Find("更改侧栏")
	if !okTop || !okInsp {
		t.Fatalf("missing panes during collapse: top=%v inspector=%v", okTop, okInsp)
	}
	if topPane.H <= 460 || topPane.H >= 890 {
		t.Fatalf("top pane height did not track bottom collapse smoothly at 60ms: top=%+v", topPane)
	}
	img := ui.Image()
	bg := img.RGBAAt(int(inspector.X+inspector.W-10), int(topPane.Y+topPane.H+2))
	paintedBottom := false
	for y := int(topPane.Y + topPane.H + 1); y < int(inspector.Y+inspector.H-4); y++ {
		for x := int(inspector.X + 10); x < int(inspector.X+inspector.W-10); x += 4 {
			if img.RGBAAt(x, y) != bg {
				paintedBottom = true
				break
			}
		}
		if paintedBottom {
			break
		}
	}
	if !paintedBottom {
		t.Fatalf("collapsing workInspector turned bottom split into a blank box at 60ms: top=%+v inspector=%+v", topPane, inspector)
	}
	ui.Advance(300 * time.Millisecond)
	topSettled, _ := ui.Find("文件更改区域")
	if math.Abs(float64(topSettled.H-900)) > 1 {
		t.Fatalf("top pane did not fill inspector after collapse: %+v", topSettled)
	}
}

func TestDeviceDisclosureHeightHasSingleOwner(t *testing.T) {
	open := false
	tester := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			deviceDisclosure(c.Key("dev-disc"), "运行详情", &open, func() {
				n.Box(c.Key("inner-body")).FillWidth().Height(100).Label("detail-body")
			})
		})
	}, 360, 300)

	open = true
	tester.Frame()
	// Outer animatedContentHeight probes on frame 1 (16.7ms) and animates for 200ms (done by ~217ms).
	// After 230ms it must be completely settled at 100 with no inner disclosureMotion still running.
	tester.Advance(230 * time.Millisecond)
	body, ok := tester.Find("detail-body")
	if !ok || math.Abs(float64(body.H-100)) > 1 || tester.FramePending() {
		t.Fatalf("deviceDisclosure still animating past outer 200ms due to double height transition: body=%+v pending=%v", body, tester.FramePending())
	}
}

func TestSidebarWorkspaceExpandAnimatesChildHeightWithoutOverlap(t *testing.T) {
	m := navigationFixture()
	v := New(m, nil, "")
	v.collapsedWorkspaces["w"] = true
	ui := n.NewTester(v.View, 1280, 800)
	ui.Advance(time.Second)

	beforeRemote, ok := ui.Find("Remote workspace")
	if !ok {
		t.Fatal("missing lower workspace")
	}

	v.collapsedWorkspaces["w"] = false
	ui.Frame()
	ui.Advance(60 * time.Millisecond)

	midRemote, okR := ui.Find("Remote workspace")
	midChild, okC := ui.Find("Other session")
	if !okR || !okC {
		t.Fatalf("missing rows mid-expand: remote=%v child=%v", okR, okC)
	}
	// The lower workspace must stay at or below the expanding child session's bottom edge (no overlap).
	if midRemote.Y < midChild.Y+midChild.H-2 {
		t.Fatalf("lower workspace overlapped expanding child session: child=%+v remote=%+v (before=%+v)", midChild, midRemote, beforeRemote)
	}
	ui.Advance(time.Second)
	endRemote, _ := ui.Find("Remote workspace")
	if endRemote.Y <= midRemote.Y {
		t.Fatalf("lower workspace did not continue moving smoothly to final position: mid=%+v end=%+v", midRemote, endRemote)
	}
}

func TestPendingRequestArrivalMeasuresBeforeFirstVisibleFrame(t *testing.T) {
	v := requestMotionModel()
	d := v.Model.Details["s"]
	req := d.Requests[0]
	d.Requests = nil
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)

	// New request arrives on an idle session (presence starts at 0).
	// Advance 17ms (first 60Hz visible frame after t=0) and check that card height already
	// follows the full settled height * CubicBezier(.2, 0, 0, 1), not header-only height.
	d.Requests = []domain.PendingRequest{req}
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(17 * time.Millisecond)
	first, ok1 := ui.Find("待处理请求卡片")
	ui.Advance(time.Second)
	settled, ok2 := ui.Find("待处理请求卡片")
	if !ok1 || !ok2 || settled.H <= 100 {
		t.Fatalf("request card did not open: first=%+v(%v) settled=%+v(%v)", first, ok1, settled, ok2)
	}
	ease := n.CubicBezier(.2, 0, 0, 1)
	wantFirst := settled.H * ease(float32(time.Second/60)/float32(320*time.Millisecond))
	if math.Abs(float64(first.H-wantFirst)) > 1.5 {
		t.Fatalf("pending request card skipped body measurement on frame 1: first=%g want=%g settled=%g",
			first.H, wantFirst, settled.H)
	}
}

func TestRequestCollapseOptionsDoesNotUse500msSpring(t *testing.T) {
	v := requestMotionModel()
	opts := make([]domain.Option, 12)
	for i := range opts {
		opts[i] = domain.Option{Label: strings.Repeat("选项", 4) + string(rune('A'+i)), Description: "详细说明文本"}
	}
	d := v.Model.Details["s"]
	d.Requests = []domain.PendingRequest{{
		ID: "long-q", SessionID: "s", Kind: "questions", Title: "长选项", State: "pending",
		Questions: []domain.Question{{ID: "q1", Text: "请选择", Options: opts}},
	}}
	v.Model.Details["s"] = d
	pos := &n.ScrollState{Y: 200, MaxY: 400}
	expanded := map[string]bool{"long-q/q1": true}
	tester := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			n.Scroll(c).Size(400, 120).TrackScroll(pos).Children(func() {
				n.Box(c).FillWidth().Height(520)
			})
			v.questionSelectionControls(c, c.Theme(), requestPage{request: d.Requests[0], question: 0}, expanded, pos)
		})
	}, 420, 200)
	tester.Advance(time.Second)

	if err := tester.Click("收起选项"); err != nil {
		t.Fatal(err)
	}
	// After 220ms, an explicit 180ms EaseOut scroll reset must have settled (not a 500ms Smooth spring).
	tester.Advance(220 * time.Millisecond)
	if pos.Scrolling() || pos.Y > 1 {
		t.Fatalf("collapsing options used 500ms spring instead of 180ms ease: Y=%g scrolling=%v", pos.Y, pos.Scrolling())
	}
}
