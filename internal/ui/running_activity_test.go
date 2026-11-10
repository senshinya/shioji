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
			if !ui.HasText(tc.want) {
				t.Fatal("incorrect current activity", tc.want, ui.Texts())
			}
			if tc.want == "等待回答" || tc.want == "等待授权" {
				if _, ok := ui.Find("进行中状态"); ok {
					t.Fatal("waiting user still shows a breathing work dot")
				}
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

func TestFoldedLiveToolsOwnActivityWithoutMovingHeading(t *testing.T) {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	d.Items = []domain.Item{{ID: "u", TurnID: "t", Kind: "user", Text: "Run checks", Final: true}, {ID: "a", TurnID: "t", Kind: "tool", Presentation: "command", Name: "first command"}, {ID: "b", TurnID: "t", Kind: "tool", Presentation: "command", Name: "second command"}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("2 次工具调用") // Saved history correctly opens folded.
	ui.Advance(time.Second)
	if !ui.HasText("正在执行命令") {
		t.Fatal("expanded tools have no current activity", ui.Texts())
	}
	before, _ := ui.Find("2 次工具调用")
	ui.Click("2 次工具调用")
	ui.Advance(time.Second)
	after, _ := ui.Find("2 次工具调用")
	if math.Abs(float64(before.Y-after.Y)) > 1 {
		t.Fatal("folding moved the clicked title", before, after)
	}
	if _, ok := ui.Find("工具组运行状态"); !ok {
		t.Fatal("folded live header has no activity")
	}
	if ui.HasText("正在执行命令") {
		t.Fatal("folded header and footer duplicate activity")
	}
	ui.Click("2 次工具调用")
	ui.Advance(time.Second)
	if !ui.HasText("正在执行命令") {
		t.Fatal("expansion did not return activity to footer")
	}
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
		ca := a.Image().RGBAAt(int(ra.X+ra.W/2), int(ra.Y+ra.H/2))
		cb := b.Image().RGBAAt(int(rb.X+rb.W/2), int(rb.Y+rb.H/2))
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
				ui.Click("1 次工具调用")
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
				for _, action := range []string{"fold", "expand"} {
					ui.Click("1 次工具调用")
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
