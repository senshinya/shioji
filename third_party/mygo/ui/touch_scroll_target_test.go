package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

func TestTouchOnlyConsumesTapsInsideDeceleratingList(t *testing.T) {
	var inside, outside int
	state := &ListState{}
	tester := NewTester(func(c *Context) {
		Column(c).Fill().Children(func() {
			List(c, state, 60, func(index int) {
				ButtonBase(c.Key(index)).FillWidth().Height(44).OnClick(func() { inside++ })
			}).Grow(1).MinHeight(0)
			ButtonBase(c.Key("outside")).FillWidth().Height(44).OnClick(func() { outside++ })
		})
	}, 240, 200)
	target, _ := tester.rt.panTarget(60, 20, Vertical)
	if target == 0 {
		t.Fatal("fixture has no scrollable list")
	}
	startFling := func() {
		tester.rt.flinging = fling{on: true, target: target, x: 60, y: 20, vy: 200, at: tester.now}
	}
	startFling()
	tester.FingerDown(60, 20)
	tester.FingerUp(60, 20)
	if inside != 0 || tester.rt.flinging.on {
		t.Fatal("tap on moving list should stop it without activating its row")
	}
	startFling()
	tester.FingerDown(60, 177)
	tester.FingerUp(60, 177)
	if outside != 1 || tester.rt.flinging.on {
		t.Fatalf("tap on a separate control should activate it and stop the list: taps=%d fling=%v", outside, tester.rt.flinging.on)
	}
}

func TestTouchCanContinueDraggingDeceleratingList(t *testing.T) {
	list := &ListState{}
	tester := NewTester(func(c *Context) {
		List(c, list, 60, func(index int) {
			Box(c.Key(index)).FillWidth().Height(44)
		}).Fill()
	}, 240, 260)
	target, _ := tester.rt.panTarget(60, 170, Vertical)
	if target == 0 {
		t.Fatal("fixture has no scrollable list")
	}
	tester.rt.flinging = fling{on: true, target: target, x: 60, y: 170, vy: 600, at: tester.now}
	tester.FingerDown(60, 170)
	tester.FingerMove(60, 150)
	tester.FingerMove(60, 70)
	if got := tester.rt.states[target].scrollY; got < 70 {
		t.Fatalf("second swipe stopped moving after catching the list: offset %.1f", got)
	}
	tester.FingerUp(60, 70)
}

func TestTouchKeepsFlingAndBounceWhenEventsAreDeliveredTogether(t *testing.T) {
	enableTouch()
	before := overscrollKind
	overscrollKind = overscrollBounce
	t.Cleanup(func() { overscrollKind = before })
	for _, virtual := range []bool{false, true} {
		list := &ListState{}
		tester := NewTester(func(c *Context) {
			if virtual {
				List(c, list, 12, func(index int) {
					Box(c.Key(index)).FillWidth().Height(44)
				}).Fill()
				return
			}
			Scroll(c).Fill().Children(func() { Box(c).Height(528) })
		}, 240, 245)
		id, _ := tester.rt.panTarget(60, 210, Vertical)
		if id == 0 {
			t.Fatal("fixture has no scrollable content")
		}
		// A busy rendering thread can receive the gesture in one batch.
		for _, event := range []struct {
			kind platform.SurfaceEventKind
			y    float64
			at   time.Duration
		}{
			{platform.PointerDown, 210, time.Second},
			{platform.PointerMove, 192, time.Second + 15*time.Millisecond},
			{platform.PointerMove, 110, time.Second + 35*time.Millisecond},
			{platform.PointerUp, 100, time.Second + 40*time.Millisecond},
		} {
			tester.send(platform.SurfaceEvent{Kind: event.kind, X: 60, Y: event.y, Touch: true, TouchTime: event.at})
		}
		s := tester.rt.states[id]
		released := s.scrollY
		tester.Advance(30 * time.Millisecond)
		if s.scrollY <= released {
			t.Fatalf("virtual=%v: content stopped at the release point %.1f", virtual, released)
		}
		tester.Advance(120 * time.Millisecond)
		if s.over[1] <= 0 {
			t.Fatalf("virtual=%v: fling stopped at the edge instead of springing past it", virtual)
		}
		tester.Advance(time.Second)
		if s.over[1] != 0 || s.scrollY != s.contentH-float64(s.h) {
			t.Fatalf("virtual=%v: rebound did not settle at the edge, offset=%v over=%v", virtual, s.scrollY, s.over[1])
		}
	}
}

func TestFlingHandsItsLastStepToTheSpringAtEitherEndOfTheTranscript(t *testing.T) {
	enableTouch()
	before := overscrollKind
	overscrollKind = overscrollBounce
	t.Cleanup(func() { overscrollKind = before })
	list := &ListState{FollowEnd: true}
	tester := NewTester(func(c *Context) {
		Column(c).Fill().Children(func() {
			list.EndOverlay = Box(c.Key("composer")).Absolute().Left(0).Right(0).Bottom(0).Height(54)
			List(c.Key("transcript"), list, 38, func(index int) {
				Box(c.Key(index)).FillWidth().Height(30 + float32(index%5)*8)
			}).Grow(1).MinHeight(0)
		})
	}, 240, 300)
	id, _ := tester.rt.panTarget(60, 150, Vertical)
	if id == 0 {
		t.Fatal("fixture has no scrollable transcript")
	}
	s := tester.rt.states[id]
	for _, direction := range []float32{-1, 1} {
		s.scrollY = .25
		if direction > 0 {
			s.scrollY = s.contentH - float64(s.h) - .25
		}
		tester.rt.flinging = fling{on: true, target: id, x: 60, y: 150, vy: 21 * direction, at: tester.now}
		tester.rt.flingStep(tester.now.Add(50 * time.Millisecond))
		if s.overV[1]*direction <= 0 {
			t.Fatalf("edge %v swallowed the remaining fling at offset %v", direction, s.scrollY)
		}
		s.over, s.overV = [2]float32{}, [2]float32{}
	}
}
