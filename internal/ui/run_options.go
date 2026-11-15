package ui

import (
	n "github.com/egoist/mygo/ui"
)

var planModeIcon = svg("M2.5 3.5l1.5 1.5L6.5 2M9 3.5h8.5M2.5 10l1.5 1.5L6.5 8.5M9 10h8.5M3 18h3M9 18h8.5")
var fastModeIcon = svg("M11.5 2 4.5 11h5l-1 7 7-10h-5z")

// One native switch owns the full menu row, including keyboard activation.
// Its indicator reuses the settings capsule without a nested focus target.
func runOptionToggle(c *n.Context, label string, icon *n.SVG, value *bool) n.Element {
	e := controlFeedback(c, n.SwitchBase(c, value).FillWidth().Height(28).PaddingX(8).Radius(4).Label(label+" 开关"), 4)
	e.Children(func() {
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
			inlineIconText(c, icon, label, n.Font{Family: c.Theme().Font, Size: 13, Weight: 400}, 14, 8, c.Theme().Text).Grow(1)
			configurationSwitchIndicator(c, n.Column(c).Size(34, 20).IgnoreInput(true), *value)
		})
	})
	return e
}
