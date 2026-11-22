package ui

import "testing"

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
