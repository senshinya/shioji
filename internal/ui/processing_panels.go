package ui

import n "github.com/egoist/mygo/ui"

const (
	processingPanelInset     = 16
	processingPanelIconSize  = 14
	processingPanelIconGap   = 8
	processingPanelTextInset = processingPanelIconSize + processingPanelIconGap
)

func processingPanelSurface(e n.Element, t *n.Theme) n.Element {
	return e.Radius(10).Background(processingPanelColor(t)).Border(1, t.Border)
}
func processingPanelColor(t *n.Theme) n.Color {
	if t.Dark {
		return n.Hex("#1d1d1d")
	}
	return n.Hex("#f6f6f6")
}
func processingPanelDivider(c *n.Context, t *n.Theme) {
	n.Column(c).FillWidth().Height(1).Background(t.Border)
}
