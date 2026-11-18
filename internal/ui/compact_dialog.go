package ui

import (
	"time"

	n "github.com/egoist/mygo/ui"
)

const (
	compactDialogRadius         = 12
	compactDialogBorder         = 1
	compactDialogWindowInset    = 16
	compactDialogTitleHeight    = 22
	compactDialogTitleGap       = 8
	compactDialogFooterGap      = 12
	compactDialogButtonGap      = 8
	compactDialogVerticalChrome = 2 * (configurationDialogInset + compactDialogBorder)
	dialogBackdropOpacity       = .16
)

func dialogSurface(panel n.Element, t *n.Theme) n.Element {
	return panel.Radius(compactDialogRadius).Background(raised(t)).Border(compactDialogBorder, t.Border).
		Shadow(0, 8, 28, 0, n.RGBA(0, 0, 0, dialogBackdropOpacity))
}

func compactDialog(c *n.Context, t *n.Theme, open *bool, width float32, build func(n.Element)) n.Element {
	return n.DialogBase(c, open, func(backdrop, panel n.Element) {
		w, h := c.Size()
		backdrop.Background(n.RGBA(0, 0, 0, dialogBackdropOpacity)).Transition(fadeMotion(c, 180*time.Millisecond))
		dialogSurface(panel, t).Width(min(width, w-2*compactDialogWindowInset)).MaxHeight(h - 2*compactDialogWindowInset).
			Padding(configurationDialogInset).Clip().Transition(popupMotion(c))
		build(panel)
	})
}

func compactDialogContent(c *n.Context, title string, body, footer func()) n.Element {
	_, h := c.Size()
	reserved := float32(2*compactDialogWindowInset + compactDialogVerticalChrome + compactDialogTitleHeight +
		compactDialogTitleGap + compactDialogFooterGap + configurationControlHeight)
	return n.Column(c).FillWidth().Shrink(0).Children(func() {
		n.Text(c, title).FillWidth().MinWidth(0).FontSize(configurationSectionSize).FontWeight(500).
			FixedLineHeight(compactDialogTitleHeight).Height(compactDialogTitleHeight).
			SingleLine().Ellipsis("…").TooltipWhenTruncated(title).Margin(0, 0, compactDialogTitleGap, 0)
		n.Scroll(c.Key("dialog-body")).FillWidth().MaxHeight(max(0, h-reserved)).Children(body)
		n.Row(c.Key("dialog-footer")).FillWidth().Height(configurationControlHeight).AlignItems(n.Center).
			Gap(compactDialogButtonGap).Margin(compactDialogFooterGap, 0, 0, 0).Children(footer)
	})
}

func compactDialogPrimary(c *n.Context, label string) n.Element {
	return controlFeedback(c, n.PrimaryButton(c, label).Height(configurationControlHeight).
		PaddingX(12).Radius(6).FontSize(configurationValueSize), 6, true)
}

func compactDialogLeadingAction(c *n.Context, label string) n.Element {
	return configurationAction(c, label).MarginStart(-configurationHoverInset)
}
