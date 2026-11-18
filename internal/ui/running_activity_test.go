package ui

import (
	"bytes"
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// These fixtures exercise the native footer's actual consumption of normalized
// events; a stale item, child request or terminal turn must not claim live work.
func TestRunningActivityUsesCurrentTurnEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		item    domain.Item
		request domain.PendingRequest
		want    string
	}{
		{name: "reasoning", item: domain.Item{Kind: "activity", Presentation: "reasoning"}, want: "正在思考"},
		{name: "command", item: domain.Item{Kind: "tool", Presentation: "command"}, want: "正在执行命令"},
		{name: "tool", item: domain.Item{Kind: "tool", Presentation: "mcp"}, want: "正在执行工具"},
		{name: "reply", item: domain.Item{Kind: "assistant", Text: "A streamed answer"}, want: "正在回复"},
		{name: "quiet", item: domain.Item{Kind: "tool", Presentation: "command", Final: true}, want: "进行中"},
		{name: "question", request: domain.PendingRequest{Kind: "questions", State: "pending"}, want: "等待回答"},
		{name: "approval", request: domain.PendingRequest{Kind: "approval", State: "pending"}, want: "等待授权"},
		{name: "child-request", item: domain.Item{Kind: "assistant", Text: "The parent is still working"}, request: domain.PendingRequest{Kind: "approval", WorkID: "child", State: "pending"}, want: "正在回复"},
		{name: "settled-request", item: domain.Item{Kind: "assistant", Text: "The reply continues"}, request: domain.PendingRequest{Kind: "questions", State: "answered"}, want: "正在回复"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := liveCompletionModel()
			d := v.Model.Details["s"]
			d.Items = []domain.Item{{ID: "old", TurnID: "old-turn", Kind: "activity", Presentation: "reasoning"}}
			tc.item.ID, tc.item.TurnID = "new", "t"
			d.Items = append(d.Items, tc.item)
			if tc.request.Kind != "" {
				tc.request.ID, tc.request.SessionID, tc.request.TurnID = "r", "s", "t"
				d.Requests = []domain.PendingRequest{tc.request}
			}
			v.Model.Details["s"] = d
			ui := n.NewTester(func(c *n.Context) { n.Column(c).Fill().Children(func() { v.turnFooter(c, c.Theme(), d.Turns[0]) }) }, 600, 120)
			// Transient captions are presented only after sustained activity; the raw
			// normalized state still updates immediately.
			if tc.item.Kind == "tool" && !tc.item.Final || tc.item.Presentation == "reasoning" {
				if got := v.runningActivity(d.Turns[0]).label; got != tc.want {
					t.Fatal("presentation delay changed execution evidence", got, tc.want)
				}
				ui.Advance(400 * time.Millisecond)
			}
			if tc.want == "等待回答" || tc.want == "等待授权" {
				if ui.HasText(tc.want) {
					t.Fatal("pending request duplicates its own waiting footer", ui.Texts())
				}
				if _, ok := ui.Find("进行中状态"); ok {
					t.Fatal("waiting user still shows a breathing work dot")
				}
			} else if !ui.HasText(tc.want) {
				t.Fatal("incorrect current activity", tc.want, ui.Texts())
			}
		})
	}
}

func TestElapsedActivityPaintsWithoutRebuildingConversation(t *testing.T) {
	v := liveCompletionModel()
	builds := 0
	ui := n.NewTester(func(c *n.Context) { builds++; v.View(c) }, 1240, 820)
	started := ui.Now().Add(-35 * time.Second)
	d := v.Model.Details["s"]
	d.Turns[0].StartedAt = &started
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	r, ok := ui.Find("运行时长")
	if !ok {
		t.Fatal("worker start time has no live elapsed display")
	}
	before := contextPixels(ui, r)
	count := builds
	ui.Advance(1100 * time.Millisecond)
	after := contextPixels(ui, r)
	if bytes.Equal(before, after) {
		t.Fatal("elapsed clock did not repaint")
	}
	if builds != count {
		t.Fatal("elapsed tick rebuilt the conversation", count, builds)
	}
	d.Turns[0].StartedAt = nil
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("运行时长"); ok {
		t.Fatal("missing worker time was fabricated from client mount")
	}
}

func TestLiveToolSummaryOwnsActivityExpandedAndCollapsed(t *testing.T) {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	d.Items = []domain.Item{{ID: "u", TurnID: "t", Kind: "user", Text: "Run checks", Final: true}, {ID: "a", TurnID: "t", Kind: "tool", Presentation: "command", Name: "first command"}, {ID: "b", TurnID: "t", Kind: "tool", Presentation: "command", Name: "second command"}}
	started := time.Now().Add(-35 * time.Second)
	d.Turns[0].StartedAt = &started
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	// The current executing group opens expanded; older history stays folded.
	if _, ok := ui.Find("工具组运行状态"); !ok {
		t.Fatal("expanded title lost its running indicator", ui.Texts())
	}
	checkPhase := func() {
		t.Helper()
		count := 0
		for _, text := range ui.Texts() {
			if text == "正在执行命令" {
				count++
			}
		}
		if count != 1 {
			t.Fatal("current phase must have exactly one visible owner", count)
		}
	}
	checkPhase()
	before, _ := ui.Find("2 次工具调用")
	clock, ok := ui.Find("运行时长")
	if !ok || math.Abs(float64(clock.Y-before.Y)) > 1 {
		t.Fatal("expanded title lost its elapsed clock", before, clock)
	}
	ui.Click("执行工具")
	ui.Advance(time.Second)
	after, _ := ui.Find("2 次工具调用")
	clock, ok = ui.Find("运行时长")
	if !ok || math.Abs(float64(clock.Y-after.Y)) > 1 {
		t.Fatal("folding moved the elapsed clock out of the title", after, clock)
	}
	if _, ok := ui.Find("工具组运行状态"); !ok {
		t.Fatal("folded live header has no activity")
	}
	checkPhase()
	ui.Click("2 次工具调用")
	ui.Advance(time.Second)
	if _, ok := ui.Find("工具组运行状态"); !ok {
		t.Fatal("expansion removed status from the title")
	}
	checkPhase()
	d.Turns[0].Status = "completed"
	d.Session.Status = "completed"
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("工具组运行状态"); ok {
		t.Fatal("completed tool group retained live activity")
	}
}

func TestActivityLabelChangesRetainBreathingPhaseAndWaitingStopsIt(t *testing.T) {
	reference := liveCompletionModel()
	subject := liveCompletionModel()
	build := func(v *View) func(*n.Context) {
		return func(c *n.Context) {
			n.Column(c).Fill().Children(func() { v.turnFooter(c, c.Theme(), v.Model.Details["s"].Turns[0]) })
		}
	}
	a := n.NewTester(build(reference), 600, 100)
	b := n.NewTester(build(subject), 600, 100)
	a.Advance(time.Second)
	b.Advance(time.Second)
	for _, item := range []domain.Item{{Kind: "activity", Presentation: "reasoning"}, {Kind: "tool", Presentation: "command"}, {Kind: "assistant", Text: "Streaming"}} {
		d := subject.Model.Details["s"]
		item.ID, item.TurnID = "phase", "t"
		d.Items = []domain.Item{item}
		subject.Model.Details["s"] = d
		b.Frame()
		a.Advance(350 * time.Millisecond)
		b.Advance(350 * time.Millisecond)
		ra, _ := a.Find("进行中状态")
		rb, ok := b.Find("进行中状态")
		if !ok {
			t.Fatal("live dot vanished during label change")
		}
		ca := a.Image().RGBAAt(int(ra.X+3), int(ra.Y+ra.H/2))
		cb := b.Image().RGBAAt(int(rb.X+3), int(rb.Y+rb.H/2))
		if ca != cb {
			t.Fatal("text change restarted the green-dot phase", item.Kind, ca, cb)
		}
	}
	d := subject.Model.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "q", TurnID: "t", Kind: "questions", State: "pending"}}
	subject.Model.Details["s"] = d
	b.Frame()
	b.Advance(time.Second)
	before := copyFrame(b)
	b.Advance(time.Second)
	if !bytes.Equal(before.Pix, b.Image().Pix) || b.FramePending() {
		t.Fatal("waiting state keeps animating without active work")
	}
}

func TestToolActivityHandoffNeverPaintsTwoLiveIndicators(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprint(dark, "/", reduced), func(t *testing.T) {
				v := liveCompletionModel()
				d := v.Model.Details["s"]
				d.Items = []domain.Item{{ID: "u", TurnID: "t", Kind: "user", Text: "Run checks", Final: true}, {ID: "a", TurnID: "t", Kind: "tool", Presentation: "command", Name: "command", Status: "running"}}
				v.Model.Details["s"] = d
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				ui.Advance(time.Second)
				green := func(label string) bool {
					r, ok := ui.Find(label)
					if !ok {
						return false
					}
					im := ui.Image()
					for y := int(r.Y); y < int(r.Y+r.H); y++ {
						for x := int(r.X); x < int(r.X+r.W); x++ {
							p := im.RGBAAt(x, y)
							if int(p.G)-int(p.R) > 10 && int(p.G)-int(p.B) > 10 {
								return true
							}
						}
					}
					return false
				}
				for _, action := range []string{"执行工具", "1 次工具调用"} {
					ui.Click(action)
					for frame := 0; frame < 30; frame++ {
						ui.Advance(time.Second / 60)
						if green("1 次工具调用") && green("回复信息") {
							t.Fatalf("%s paints both header and footer indicators at frame %d", action, frame)
						}
					}
				}
			})
		}
	}
}

// Compare Retina glyph pixels, allowing the project's 1 DIP rasterization tolerance.
func TestRunningStatusMatchesParagraphInkSpacing(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, size := range []int{14, 18} {
			t.Run(fmt.Sprint(dark, "/", size), func(t *testing.T) {
				v := liveCompletionModel()
				v.Model.Preferences.ReplySize = size
				d := v.Model.Details["s"]
				d.Items = []domain.Item{{ID: "body", TurnID: "t", Kind: "assistant", Text: "国国国 A\n\n国国国 B", Final: true}, {ID: "thinking", TurnID: "t", Kind: "activity", Presentation: "reasoning"}}
				v.Model.Details["s"] = d
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.SetScale(2)
				ui.Advance(time.Second)
				ink := func(label string) (int, int) {
					t.Helper()
					r, ok := ui.Find(label)
					if !ok {
						t.Fatal("missing ink target", label)
					}
					im := ui.Image()
					top, bottom := 10000, -1
					for y := int(r.Y * 2); y < int((r.Y+r.H)*2); y++ {
						for x := int(r.X * 2); x < int((r.X+r.W)*2); x++ {
							p := im.RGBAAt(x, y)
							if (!dark && p.R < 190) || (dark && p.R > 100) {
								top = min(top, y)
								bottom = max(bottom, y+1)
							}
						}
					}
					if bottom < 0 {
						t.Fatal("no glyph ink", label)
					}
					return top, bottom
				}
				_, firstBottom := ink("国国国 A")
				secondTop, secondBottom := ink("国国国 B")
				statusTop, _ := ink("正在思考")
				paragraphGap, statusGap := secondTop-firstBottom, statusTop-secondBottom
				if math.Abs(float64(paragraphGap-statusGap)/2) > 1 {
					t.Fatalf("visible paragraph/status spacing differs by more than 1 DIP: paragraphs=%.1f, status=%.1f", float64(paragraphGap)/2, float64(statusGap)/2)
				}
			})
		}
	}
}

func TestPendingRequestKeepsOnlySourceAndDock(t *testing.T) {
	for _, kind := range []string{"questions", "approval"} {
		t.Run(kind, func(t *testing.T) {
			v := liveCompletionModel()
			d := v.Model.Details["s"]
			title, waiting := "需要回答", "等待回答"
			if kind == "approval" {
				title, waiting = "需要授权", "等待授权"
			}
			request := domain.PendingRequest{ID: "r", SessionID: "s", TurnID: "t", ItemID: "source", Kind: kind, Title: title, State: "pending", Questions: []domain.Question{{ID: "q", Text: "选择一种实现方式", Custom: true}}}
			d.Items = append(d.Items, domain.Item{ID: "source", TurnID: "t", Kind: "request", Text: title, Status: "pending"})
			d.Requests = []domain.PendingRequest{request}
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			if ui.HasText(waiting) {
				t.Fatal("waiting status duplicates request source and dock", ui.Texts())
			}
			if !ui.HasText(title) {
				t.Fatal("request's existing source or dock was removed")
			}
			if v.Model.Details["s"].Requests[0].State != "pending" {
				t.Fatal("presentation resolved a pending request")
			}
		})
	}
}

// Keep coverage during the crossfade, then reclaim the dot/timer space with
// real width animation while the count stays in place until the dot fades.
func TestToolToProseProgressKeepsCoverageAndStableEndpoints(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, expanded := range []bool{false, true} {
			t.Run(fmt.Sprint(dark, "/", expanded), func(t *testing.T) {
				v := liveCompletionModel()
				d := v.Model.Details["s"]
				d.Items = []domain.Item{{ID: "u", TurnID: "t", Kind: "user", Text: "Run checks", Final: true}, {ID: "a", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go test ./..."}, {ID: "b", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go vet ./..."}}
				started := time.Now().Add(-35 * time.Second)
				d.Turns[0].StartedAt = &started
				v.Model.Details["s"] = d
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if !expanded {
					ui.Click("执行工具")
					ui.Advance(time.Second)
				}
				if ui.HasText("go test ./...") != expanded {
					t.Fatal("tool fixture does not match the requested disclosure state")
				}
				header, _ := ui.Find("2 次工具调用")
				titleX := activityTitleInkX(ui, header, dark)
				font := n.Font{Family: v.Model.Preferences.InterfaceFont, Size: 11, Weight: 400, Features: "tnum"}
				titleWidth, _ := n.ShapeText("2 次工具调用", font, 0).Size()
				clockWidth, _ := n.ShapeText("37 秒", font, 0).Size()
				phaseWidth, _ := n.ShapeText("正在执行命令", font, 0).Size()
				if right := activityLastInkX(ui, header, dark); right > titleX+titleWidth+phaseWidth+clockWidth+34 {
					t.Fatal("running status extends beyond its elapsed time", titleX, right)
				}
				lastTitleX := titleX
				d.Items[1].Final, d.Items[2].Final = true, true
				d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "三个检查均已通过。"})
				v.Model.Details["s"] = d
				began := ui.Now()
				ready := time.Duration(0)
				if expanded {
					ready = 160 * time.Millisecond
				}
				ui.Frame()
				firstFooterY := float32(-1)
				settledFooterY := float32(-1)
				for frame := 0; frame < 36; frame++ {
					ui.Advance(time.Second / 60)
					header, _ = ui.Find("2 次工具调用")
					footer, _ := ui.Find("回复信息")
					hasOld := activityGreenIn(ui, header)
					hasNew := activityGreenIn(ui, footer)
					if !hasOld && !hasNew {
						t.Fatalf("no visible progress at frame %d", frame)
					}
					if hasOld {
						t.Fatalf("tool progress remains above the new reply at frame %d", frame)
					}
					x := activityTitleInkX(ui, header, dark)
					elapsed := ui.Now().Sub(began)
					if elapsed <= ready+140*time.Millisecond && math.Abs(float64(x-titleX)) > 1 {
						t.Fatalf("title moved before the dot finished fading at frame %d", frame)
					}
					if x > lastTitleX+1 || lastTitleX-x > 4 {
						t.Fatalf("title jumped instead of gliding left: %v -> %v", lastTitleX, x)
					}
					lastTitleX = x
					if hasNew {
						if firstFooterY < 0 {
							firstFooterY = footer.Y
						}
						// The unique status follows the content while the old tool
						// body folds, then keeps its settled endpoint.
						if elapsed >= ready {
							if settledFooterY < 0 {
								settledFooterY = footer.Y
							}
							if math.Abs(float64(footer.Y-settledFooterY)) > 1 {
								t.Fatalf("settled progress is moving: %v -> %v at frame %d", settledFooterY, footer.Y, frame)
							}
						}
					}
					if elapsed >= ready+90*time.Millisecond && firstFooterY < 0 {
						t.Fatal("progress waits after the actual disclosure has settled", elapsed, ready)
					}
					if elapsed >= ready+140*time.Millisecond+90*time.Millisecond && titleX-x < 8 {
						t.Fatal("title recovery restarted its own clock instead of continuing the handoff", elapsed, titleX, x)
					}
				}
				if firstFooterY < 0 || activityGreenIn(ui, header) {
					t.Fatal("progress never completed its handoff")
				}
				if math.Abs(float64(titleX-lastTitleX-12)) > 1 {
					t.Fatal("title retained the vanished dot's indentation", titleX, lastTitleX)
				}
				if right := activityLastInkX(ui, header, dark); right <= lastTitleX+titleWidth+6 || right > lastTitleX+titleWidth+22 {
					t.Fatal("folded summary lost its disclosure cue after handoff", header, right)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("handoff-dark-%v-expanded-%v.png", dark, expanded))
				handoffGap := activityLastInkX(ui, header, dark) - lastTitleX
				// The same count must leave the disclosure cue in the same
				// place when the turn changes from prose to completed history.
				d.Turns[0].Status, d.Session.Status = "completed", "completed"
				v.Model.Details["s"] = d
				ui.Frame()
				ui.Advance(time.Second)
				header, ok := ui.Find("2 次工具调用")
				if !ok {
					t.Fatal("completed history lost the tool summary")
				}
				completedGap := activityLastInkX(ui, header, dark) - activityTitleInkX(ui, header, dark)
				saveNativeSkillImage(t, ui, fmt.Sprintf("completed-dark-%v-expanded-%v.png", dark, expanded))
				if delta := handoffGap - completedGap; math.Abs(float64(delta)) > 1 {
					t.Fatalf("hidden activity spacing shifts the disclosure cue by %.1f DIP after handoff", delta)
				}
			})
		}
	}
}

func activityGreenIn(ui *n.Tester, r n.Rect) bool {
	im := ui.Image()
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			p := im.RGBAAt(x, y)
			if int(p.G)-int(p.R) > 12 && int(p.G)-int(p.B) > 12 {
				return true
			}
		}
	}
	return false
}
func activityTitleInkX(ui *n.Tester, r n.Rect, dark bool) float32 {
	im := ui.Image()
	for x := int(r.X); x < int(r.X+80); x++ {
		for y := int(r.Y + 3); y < int(r.Y+17); y++ {
			p := im.RGBAAt(x, y)
			if abs(int(p.R)-int(p.G)) < 6 && abs(int(p.B)-int(p.G)) < 6 && ((!dark && p.R < 180) || (dark && p.R > 100)) {
				return float32(x)
			}
		}
	}
	return -1
}

func activityLastInkX(ui *n.Tester, r n.Rect, dark bool) float32 {
	im := ui.Image()
	for x := int(r.X+r.W) - 1; x >= int(r.X); x-- {
		for y := int(r.Y + 3); y < int(r.Y+17); y++ {
			p := im.RGBAAt(x, y)
			if abs(int(p.R)-int(p.G)) < 6 && abs(int(p.B)-int(p.G)) < 6 && ((!dark && p.R < 180) || (dark && p.R > 100)) {
				return float32(x)
			}
		}
	}
	return -1
}

func TestToolToProseRetainsBreathingClock(t *testing.T) {
	makeView := func() *View {
		v := liveCompletionModel()
		d := v.Model.Details["s"]
		d.Items = []domain.Item{{ID: "tool", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go test ./..."}}
		v.Model.Details["s"] = d
		return v
	}
	aView, bView := makeView(), makeView()
	a, b := n.NewTester(aView.View, 1240, 820), n.NewTester(bView.View, 1240, 820)
	// Align the two real native animation clocks before applying any turn data.
	a.Advance(b.Now().Sub(a.Now()))
	started := a.Now().Add(-35 * time.Second)
	for _, v := range []*View{aView, bView} {
		d := v.Model.Details["s"]
		d.Turns[0].StartedAt = &started
		v.Model.Details["s"] = d
	}
	// Start a distinct turn identity so both presentations consume its start.
	for _, v := range []*View{aView, bView} {
		d := v.Model.Details["s"]
		d.Turns[0].ID = "clock"
		d.Items[0].TurnID = "clock"
		v.Model.Details["s"] = d
	}
	a.Frame()
	b.Frame()
	a.Advance(time.Second)
	b.Advance(time.Second)
	d := bView.Model.Details["s"]
	d.Items[0].Final = true
	d.Items = append(d.Items, domain.Item{ID: "prose", TurnID: "clock", Kind: "assistant", Text: "All checks passed."})
	bView.Model.Details["s"] = d
	b.Frame()
	a.Advance(400 * time.Millisecond)
	b.Advance(400 * time.Millisecond)
	for i := 0; i < 5; i++ {
		ra, _ := a.Find("进行中状态")
		rb, ok := b.Find("进行中状态")
		if !ok {
			t.Fatal("new progress has no live dot")
		}
		ca := a.Image().RGBAAt(int(ra.X+3), int(ra.Y+ra.H/2))
		cb := b.Image().RGBAAt(int(rb.X+3), int(rb.Y+rb.H/2))
		if ca != cb {
			t.Fatal("tool-to-prose handoff restarted its breathing phase", ca, cb)
		}
		a.Advance(110 * time.Millisecond)
		b.Advance(110 * time.Millisecond)
	}
}

func TestToolToProseHandoffStopsForTerminalOrPendingState(t *testing.T) {
	for _, state := range []string{"completed", "interrupted", "pending", "disconnected", "reduced"} {
		t.Run(state, func(t *testing.T) {
			v := liveCompletionModel()
			d := v.Model.Details["s"]
			d.Items = []domain.Item{{ID: "tool", TurnID: "t", Kind: "tool", Presentation: "command", Name: "go test ./..."}}
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			d.Items[0].Final = true
			d.Items = append(d.Items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "All checks passed."})
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(100 * time.Millisecond)
			switch state {
			case "completed", "interrupted":
				d.Turns[0].Status = domain.ExecutionState(state)
				d.Session.Status = domain.ExecutionState(state)
			case "pending":
				d.Requests = []domain.PendingRequest{{ID: "q", SessionID: "s", TurnID: "t", Kind: "questions", State: "pending"}}
			case "disconnected":
				v.Model.Connected = false
			case "reduced":
				v.Model.Preferences.ReduceMotion = true
			}
			v.Model.Details["s"] = d
			ui.Frame()
			if state == "reduced" {
				r, _ := ui.Find("回复信息")
				if !activityGreenIn(ui, r) {
					t.Fatal("reduced motion did not finish the handoff immediately")
				}
			} else {
				ui.Advance(time.Second)
				if _, ok := ui.Find("进行中状态"); ok {
					t.Fatal("obsolete handoff survived", state)
				}
			}
		})
	}
}
