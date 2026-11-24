package conversationui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestAnimatedContentHeightEmptyContentDoesNotSpinForever(t *testing.T) {
	hasBody := false
	var measuredH float32
	tester := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			frame := n.Column(c.Key("panel")).FillWidth().Shrink(0).Clip()
			AnimatedContentHeight(c, frame, true, false, 180*time.Millisecond, func() float32 {
				if hasBody {
					n.Box(c.Key("body")).FillWidth().Height(60)
				}
				return 0
			})
			frame.DrawOver(func(_ *n.Painter, r n.Rect) { measuredH = r.H })
		})
	}, 320, 200)

	if tester.FramePending() {
		t.Fatal("empty AnimatedContentHeight keeps requesting frames forever")
	}

	// When content later arrives after settling empty, it must still measure and animate open.
	hasBody = true
	tester.Frame()
	tester.Advance(60 * time.Millisecond)
	if measuredH <= 0 || measuredH >= 60 {
		t.Fatalf("late content did not animate smoothly from 0 to 60: mid=%g", measuredH)
	}
	tester.Advance(time.Second)
	if measuredH < 59.5 || measuredH > 60.5 {
		t.Fatalf("late content did not settle at 60: end=%g", measuredH)
	}
	if tester.FramePending() {
		t.Fatal("settled AnimatedContentHeight still has a pending frame")
	}
}

func TestAnimatedContentHeightChildDeltaPreservesParentEasing(t *testing.T) {
	textH := float32(100)
	childH := float32(50)
	var childDelta float32
	var frameH float32

	tester := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			frame := n.Column(c.Key("panel")).FillWidth().Shrink(0).Clip()
			AnimatedContentHeight(c, frame, true, true, 180*time.Millisecond, func() float32 {
				n.Box(c.Key("text")).FillWidth().Height(textH)
				n.Box(c.Key("child")).FillWidth().Height(childH)
				d := childDelta
				childDelta = 0
				return d
			})
			frame.DrawOver(func(_ *n.Painter, r n.Rect) { frameH = r.H })
		})
	}, 320, 400)

	if frameH < 149.5 || frameH > 150.5 {
		t.Fatalf("initial baseline height = %g, want 150", frameH)
	}

	// Start a 180ms parent ease by growing text from 100 -> 200.
	textH = 200
	tester.Frame()
	tester.Advance(60 * time.Millisecond)
	if frameH <= 150 || frameH >= 250 {
		t.Fatalf("parent should be mid-ease at 60ms, got %g", frameH)
	}

	// While the parent is easing, a child animates its own height (+10 per step for 3 steps = +30).
	for i := 0; i < 3; i++ {
		childH += 10
		childDelta = 10
		tester.Frame()
		tester.Advance(16 * time.Millisecond)
	}

	// Total elapsed since text grew: 60ms + 3*16ms + 90ms = 198ms (> 180ms parent duration).
	// If child deltas did not interrupt/restart the parent's 180ms ease, frameH must have reached 280.
	tester.Advance(90 * time.Millisecond)
	if frameH < 279 {
		t.Fatalf("child delta interrupted and delayed parent easing: got height %g at 198ms, want ~280", frameH)
	}
}
