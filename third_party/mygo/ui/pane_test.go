package ui

import (
	"github.com/egoist/mygo/internal/platform"
	"testing"
)

func TestPointerPressedWithinObservesChildWithoutTakingFocus(t *testing.T) {
	var presses int
	text := "draft"
	tt := NewTester(func(c *Context) {
		pane := Column(c).Size(200, 100).Children(func() { TextInput(c, &text).Label("input").Size(160, 32) })
		if PointerPressedWithin(pane) {
			presses++
		}
	}, 400, 200)
	if err := tt.Click("input"); err != nil {
		t.Fatal(err)
	}
	if presses == 0 || !tt.Focused("input") {
		t.Fatal("pane did not observe child or stole focus", presses)
	}
	before := presses
	tt.ClickAt(300, 150)
	if presses != before {
		t.Fatal("outside press attributed to pane")
	}
}

func TestInertPreservesPaintAndRejectsPointerKeyboardAndStaleAccessibility(t *testing.T) {
	inert, clicks := false, 0
	var id uint64
	tt := NewTester(func(c *Context) {
		pane := Column(c).Size(200, 100).Background(Hex("#808080"))
		Inert(pane, inert)
		pane.Children(func() {
			button := Button(c, "press").Size(100, 30)
			id = button.node().id
			if button.Clicked() {
				clicks++
			}
		})
	}, 400, 200)
	tt.Click("press")
	if clicks != 1 || !tt.Focused("press") {
		t.Fatal("normal control is not interactive")
	}
	color := tt.Image().RGBAAt(150, 70)
	inert = true
	tt.Frame()
	if tt.Focused("press") || tt.HasText("press") {
		t.Fatal("inert controls retain focus or discoverable text")
	}
	if got := tt.Image().RGBAAt(150, 70); got != color {
		t.Fatal("inert surface changed its paint", color, got)
	}
	tt.ClickAt(50, 15)
	tt.Key(0, KeyEnter)
	for _, node := range tt.rt.accessTree().Nodes {
		if node.ID == id {
			t.Fatal("inert control remains accessible")
		}
	}
	tt.rt.accessAction(platform.SurfaceEvent{ID: id, Action: platform.AccessPress})
	tt.Frame()
	if clicks != 1 {
		t.Fatal("outgoing control accepts an interaction")
	}
	inert = false
	tt.Frame()
	tt.Click("press")
	if clicks != 2 {
		t.Fatal("restored control does not accept input")
	}
}
