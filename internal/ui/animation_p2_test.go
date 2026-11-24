package ui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestActivityCaptionDebouncesWithoutRequestingFrames(t *testing.T) {
	for _, mode := range []struct {
		name         string
		reduceMotion bool
	}{{"standard", false}, {"reduced", true}} {
		t.Run(mode.name, func(t *testing.T) {
			var p runningProgress
			var got string
			builds := 0
			tester := n.NewTesterWith(func(c *n.Context) {
				builds++
				got = p.activityCaption(c, "正在思考")
			}, 200, 80, n.TesterOptions{Preferences: n.Preferences{ReduceMotion: mode.reduceMotion}})

			if tester.FramePending() || got != "进行中" {
				t.Fatalf("expected a debounced caption and no animation frame, got %q, pending=%v", got, tester.FramePending())
			}
			before := builds
			tester.Advance(100 * time.Millisecond)
			if builds != before {
				t.Fatalf("activityCaption rebuilt %d times before debounce expired", builds-before)
			}
			tester.Advance(100 * time.Millisecond)
			if got != "正在思考" {
				t.Fatalf("activityCaption did not switch after debounce timer elapsed: got %q", got)
			}
		})
	}
}

func TestWorkspacePopupMotionUsesNegativeDurationUnderReduceMotion(t *testing.T) {
	var tr n.ElementTransition
	n.NewTesterWith(func(c *n.Context) {
		tr = workspacePopupMotion(c)
	}, 200, 80, n.TesterOptions{Preferences: n.Preferences{ReduceMotion: true}})
	if tr.Duration >= 0 {
		t.Fatalf("workspacePopupMotion returned Duration=%v under ReduceMotion (MyGo treats 0 as 200ms); want < 0", tr.Duration)
	}
}

func TestOutlinePreviewSwitchDoesNotFlashOpacityZeroFrame(t *testing.T) {
	v := previewModel("简短的测试回复内容")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)

	tick5, ok5 := ui.Find("跳转到消息 5")
	tick6, ok6 := ui.Find("跳转到消息 6")
	if !ok5 || !ok6 {
		t.Fatal("missing outline ticks")
	}
	ui.Move(tick5.X+3, tick5.Y+tick5.H/2)
	ui.Advance(220 * time.Millisecond)
	panel5, ok := ui.Find("对话导航预览")
	if !ok {
		t.Fatal("preview did not open on tick 5")
	}
	// Move to adjacent tick 6: the preview must stay visible on the very next frame
	// without resetting PreviewHeight to 0 and flashing Opacity(0).
	ui.Move(tick6.X+3, tick6.Y+tick6.H/2)
	if v.outline.PreviewHeight == 0 {
		t.Fatalf("switching outline tick reset PreviewHeight to 0 (causes 1-frame flash): prev=%+v", panel5)
	}
}

func TestStreamingRevealDelayUsesAfterTimerNotAnimationFrame(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	item := domain.Item{ID: "reply-1", Kind: "assistant", Text: "正在流式回复", Final: false}
	var start time.Time
	builds := 0
	tester := n.NewTester(func(c *n.Context) {
		if start.IsZero() {
			start = c.Now()
			v.replyStreams = map[string]*replyStream{
				item.ID: {phase: replyFresh, revealAfter: start.Add(200 * time.Millisecond)},
			}
		}
		builds++
		n.Column(c).Fill().Children(func() {
			v.assistantReply(c, item)
		})
	}, 400, 200)

	if tester.FramePending() {
		t.Fatal("assistantReply busy-waits with AnimationFrame during revealAfter delay")
	}
	before := builds
	tester.Advance(100 * time.Millisecond)
	if builds != before {
		t.Fatalf("assistantReply rebuilt %d times before revealAfter timer elapsed", builds-before)
	}
}
