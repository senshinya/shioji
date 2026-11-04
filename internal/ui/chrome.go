package ui

import n "github.com/egoist/mygo/ui"

var searchIcon = svg("M14 14l4 4M15 8.5a6.5 6.5 0 1 1-13 0 6.5 6.5 0 0 1 13 0")
var settingsIcon = svg("M8 2h4l.6 2.2 2 .9 2-.8 2 3.4-1.7 1.6v2.3l1.7 1.5-2 3.5-2-.8-2 .9L12 18H8l-.6-2.2-2-.9-2 .8-2-3.5 1.7-1.5V8.4L1.4 6.8l2-3.4 2 .8 2-.9zM13 10a3 3 0 1 1-6 0 3 3 0 0 1 6 0")
var shieldIcon = svg("M10 2l6 2v5c0 4-3 7-6 9-3-2-6-5-6-9V4zM7 10l2 2 4-5")
var attentionIcon = svg("M10 3l8 14H2zM10 7v4M10 14v.2")
var sidebarIcon = svg("M2 3h16v14H2zM12 3v14")
var closeIcon = svg("M5 5l10 10M15 5L5 15")
var branchIcon = svg("M6 5v10M14 5v3a5 5 0 0 1-5 5H6M8 3a2 2 0 1 1-4 0 2 2 0 0 1 4 0M16 3a2 2 0 1 1-4 0 2 2 0 0 1 4 0M8 17a2 2 0 1 1-4 0 2 2 0 0 1 4 0")

func navButton(c *n.Context, icon *n.SVG, label string) n.Element {
	return n.ButtonBase(c).FillWidth().Height(28).PaddingX(8).Radius(8).Label(label).Children(func() {
		n.Row(c).Gap(8).AlignItems(n.Center).Children(func() { n.Icon(c, icon).Size(16, 16); n.Text(c, label).FontSize(13).FontWeight(400) })
	})
}
