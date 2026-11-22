package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	tapSize                      float32 = 44
	pillRadius                           = tapSize / 2
	pageInset                            = 16
	pageTopInset                         = 6
	contentMaxWidth                      = 704
	contentRowHeight                     = 52
	formRowHeight                        = 47
	cardRadius                           = 12
	panelRadius                          = 13
	bodyTextSize                         = 15
	controlTextSize                      = 14
	captionTextSize                      = 12
	conversationTitle                    = 16
	conversationTitleGap                 = 4
	transcriptItemGap                    = 4
	transcriptTurnGap                    = 8
	transcriptMetadataHeight             = 24
	transcriptLeading                    = 1.48
	footerIconSize                       = 14
	footerElementGap                     = 24
	panelInset                           = 10
	panelContentGap                      = 6
	searchResultsGap                     = 12
	draftComposerWidth                   = 660
	conversationComposerWidth            = 720
	conversationComposerInset            = 19
	composerBottomGap                    = 8
	mobileTabDuration                    = 260 * time.Millisecond
	mobileReplyFade                      = 180 * time.Millisecond
	mobileToolDisclosureDuration         = 160 * time.Millisecond
	composerPanelGap                     = 6
)

var (
	// The spring owns live geometry so a reversal continues from the visible frame.
	mobileTitleMotion  = ui.Animation{Duration: 160 * time.Millisecond}
	mobileLayoutMotion = ui.Animation{Spring: ui.SpringOptions{Duration: 500 * time.Millisecond}}
	// In-place panels animate height and position. UIKit owns navigation motion.
	mobilePanelTransition = ui.ElementTransition{Height: true, Position: true, Duration: 180 * time.Millisecond}
)

func mobileHorizontalTransition(percent float32) ui.ElementTransition {
	motion := &ui.Motion{XPercent: percent, Opacity: 1}
	return ui.ElementTransition{Duration: mobileTabDuration, Enter: motion, Exit: motion}
}

type colors struct {
	canvas, panel, inset, selected, ink, secondary, line, active, warning ui.Color
	floating, floatingEdge, floatingShadow, floatingContactShadow         ui.Color
	diffRemoved, diffAddedFill, diffRemovedFill                           ui.Color
}

var lightColors = colors{
	canvas: ui.Hex("#ffffff"), panel: ui.Hex("#f7f7f9"), inset: ui.Hex("#f1f1f3"), selected: ui.Hex("#ececef"),
	ink: ui.Hex("#18181b"), secondary: ui.Hex("#66666e"), line: ui.RGBA(24, 24, 27, .09),
	active: ui.Hex("#157141"), warning: ui.Hex("#8f4c00"),
	floating: ui.Hex("#ffffff"), floatingEdge: ui.RGBA(24, 24, 27, .09).Alpha(.55),
	floatingShadow: ui.Hex("#17171b").Alpha(.08), floatingContactShadow: ui.Hex("#17171b").Alpha(.045),
	diffRemoved: ui.Hex("#b32d36"), diffAddedFill: ui.Hex("#e9f6ee"), diffRemovedFill: ui.Hex("#fbecee"),
}

var darkColors = colors{
	canvas: ui.Hex("#111113"), panel: ui.Hex("#18181b"), inset: ui.Hex("#222225"), selected: ui.Hex("#28282c"),
	ink: ui.Hex("#f2f2f4"), secondary: ui.Hex("#a3a3ad"), line: ui.RGBA(255, 255, 255, .09),
	active: ui.Hex("#79d39b"), warning: ui.Hex("#ff9f0a"),
	floating: ui.Hex("#232326"), floatingEdge: ui.Hex("#ffffff").Alpha(.13),
	floatingShadow: ui.Hex("#000000").Alpha(.27), floatingContactShadow: ui.Hex("#000000").Alpha(.33),
	diffRemoved: ui.Hex("#ff9498"), diffAddedFill: ui.Hex("#182b20"), diffRemovedFill: ui.Hex("#322025"),
}

func palette(c *ui.Context) colors {
	if c.Theme().Dark {
		return darkColors
	}
	return lightColors
}

func (a *mobileApp) codeFont() string {
	family := a.model.Preferences.CodeFont
	if family == "" || family == "SF Mono" {
		return "monospace"
	}
	return family + ", monospace"
}

// UIKit paints outside MyGo's safe area using the same appearance.
func nativeAppearance() map[string]any {
	rgb := func(color ui.Color) int { return int(color.R)<<16 | int(color.G)<<8 | int(color.B) }
	return map[string]any{
		"canvas":    [2]int{rgb(lightColors.canvas), rgb(darkColors.canvas)},
		"sidebar":   [2]int{rgb(lightColors.panel), rgb(darkColors.panel)},
		"secondary": [2]int{rgb(lightColors.secondary), rgb(darkColors.secondary)},
		"titleSize": conversationTitle,
		"scopeSize": captionTextSize,
		"titleGap":  conversationTitleGap,
	}
}

func floatingSurface(c *ui.Context, surface ui.Element, radius float32) ui.Element {
	p := palette(c)
	return surface.Background(p.floating).Radius(radius).Border(1, p.floatingEdge).
		Shadow(0, 3, 13, 0, p.floatingShadow).Shadow(0, 1, 2, 0, p.floatingContactShadow)
}

func label(c *ui.Context, text string, size float32, color ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(size * c.Preferences().TextScale).TextColor(color).Wrap()
}

func oneLine(c *ui.Context, text string, size float32, color ui.Color) ui.Element {
	return label(c, text, size, color).MinWidth(0).MaxLines(1).Ellipsis("…")
}
