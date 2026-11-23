package main

// On macOS, pass this file and tool_arrival_ios.go explicitly to go test.

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
)

func TestMobileToolCallReservesSpaceGraduallyAtTranscriptEnd(t *testing.T) {
	var present bool
	var progress float32
	list := ui.ListState{FollowEnd: true}
	view := ui.NewTester(func(c *ui.Context) {
		ui.List(c, &list, 3, func(i int) {
			switch i {
			case 0:
				ui.Box(c).Height(220)
			case 1:
				ui.Column(c).Children(func() {
					ui.ButtonBase(c).Height(30).Label("Previous tool")
					if present {
						mobileToolCallLayout(c.Key("incoming-tool"), progress, func(call ui.Element) {
							call.Children(func() { ui.Box(c).Height(40) })
						})
					}
				})
			case 2:
				ui.Box(c).Height(30)
			}
		}).Fill()
	}, 320, 180)
	view.Frame()
	before, ok := view.Find("Previous tool")
	if !ok {
		t.Fatal("previous tool should start in view")
	}
	present = true
	for _, step := range []struct{ progress, offset float32 }{{0, 0}, {.5, 20}, {1, 40}} {
		progress = step.progress
		view.Frame()
		row, found := view.Find("Previous tool")
		if !found || math.Abs(float64(before.Y-row.Y-step.offset)) > .5 {
			t.Fatalf("at progress %v, previous row moved %v points, want %v", progress, before.Y-row.Y, step.offset)
		}
	}
}

func TestMobileToolCallArrivalsDoNotReplayExistingCallsAfterDisclosure(t *testing.T) {
	first := domain.Item{ID: "first"}
	second := domain.Item{ID: "second"}
	now := time.Unix(1, 0)
	var arrivals mobileToolCallArrivals
	arrivals.observe(now, []domain.Item{first}, true)
	if got := arrivals.progress(now, first.ID); got != 1 {
		t.Fatalf("history entered from %v on mount", got)
	}
	now = now.Add(time.Second)
	arrivals.observe(now, []domain.Item{first, second}, true)
	if got := arrivals.progress(now, second.ID); got != 0 {
		t.Fatalf("live call entered from %v, want zero", got)
	}
	now = now.Add(50 * time.Millisecond)
	arrivals.observe(now, []domain.Item{first, second}, true)
	if got := arrivals.progress(now, second.ID); got <= 0 || got >= 1 {
		t.Fatalf("existing live animation was restarted or dropped: %v", got)
	}
	arrivals.observe(now, []domain.Item{first, second}, false)
	arrivals.observe(now, []domain.Item{first, second}, true)
	if got := arrivals.progress(now, second.ID); got != 1 {
		t.Fatalf("reopening replayed an existing call: %v", got)
	}
}
