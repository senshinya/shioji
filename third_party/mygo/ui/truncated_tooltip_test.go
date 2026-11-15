package ui

import (
	"testing"
	"time"
)

func TestTruncatedKeyboardLabelsShareOneControlHint(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		tt := NewTester(func(c *Context) {
			popupTestTheme(c)
			button := ButtonBase(c).Label("control").Width(120)
			if explicit {
				button.Tooltip("Control help")
			}
			button.Children(func() {
				Column(c).Width(80).Children(func() {
					Text(c, "First long label").SingleLine().TooltipWhenTruncated("First full value")
					Text(c, "Second long label").SingleLine().TooltipWhenTruncated("Second full value")
				})
			})
		}, 400, 200)
		tt.Advance(time.Second)
		tt.Key(0, KeyTab)
		if !tt.Focused("control") {
			t.Fatal("control lacks keyboard focus")
		}
		if tt.HasText("Second full value") || explicit && tt.HasText("First full value") {
			t.Fatal("clipped labels create competing hints on the focused control", tt.Texts())
		}
		if explicit && !tt.HasText("Control help") || !explicit && !tt.HasText("First full value") {
			t.Fatal("focused control lost its hint", tt.Texts())
		}
	}
}
