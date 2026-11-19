package ui

import n "github.com/egoist/mygo/ui"

const (
	processingPanelBorder                  = 1
	processingPanelInset                   = 16
	processingPanelExpandedInset           = 16
	processingPanelHeadingLine             = 20
	processingPanelHeadingRow              = 28
	processingPanelExpandedDisclosureInset = 12
	processingPanelFoldedDisclosureInset   = 4
	// The native 13/20 heading has 5 DIP above its visible glyphs.
	// Allow for that space when matching the bottom action's visible edge.
	processingPanelHeadingInset = processingPanelExpandedInset - 5
	// Shift the expanded disclosure content within its existing height so
	// the title ink and the bottom button have the same clearance.
	processingPanelDisclosureShift = 4.5
	processingPanelDisclosureInset = 2
	processingPanelActionHeight    = 28
	processingPanelIconSize        = 14
	processingPanelIconGap         = 8
	processingPanelTextInset       = processingPanelIconSize + processingPanelIconGap
)

func processingPanelSurface(e n.Element, t *n.Theme) n.Element {
	return e.Radius(10).Background(processingPanelColor(t)).Border(processingPanelBorder, t.Border)
}

func processingPanelDisclosure(c *n.Context) n.Element {
	return control(c, 4).Height(24).Shrink(0).PaddingX(processingPanelDisclosureInset).
		MarginStart(-processingPanelDisclosureInset).Justify(n.Start)
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
