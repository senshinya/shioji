package main

import (
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/mobileclient"
)

func mobileContent(c *ui.Context, maxWidth float32) ui.Element {
	return ui.Column(c).WidthPercent(100).MaxWidth(maxWidth).AlignSelf(ui.Center)
}

func mobilePageContent(c *ui.Context, bottom float32) ui.Element {
	return mobileContent(c, contentMaxWidth).Padding(pageTopInset, pageInset, bottom)
}

func mobilePanel(c *ui.Context) ui.Element {
	return ui.Column(c).Radius(panelRadius).Background(palette(c).inset)
}

func mobileInlinePanel(c *ui.Context) ui.Element {
	return mobilePanel(c).Clip().Transition(mobilePanelTransition)
}

// Animate the space reserved by the panel so the conversation and composer
// follow the same geometry as the clipped content.
func mobileDisclosureBody(c *ui.Context, expanded bool, content func(*ui.Context)) {
	frame := ui.Column(c).Clip().Shrink(0)
	natural := ui.Local(frame, "natural-height", func() float32 { return 0 })
	target := float32(0)
	if expanded {
		target = *natural
	}
	height := frame.AnimateTo("height", target, ui.Animation{Duration: mobilePanelTransition.Duration, Ease: ui.EaseOut})
	if *natural > 0 || !expanded {
		frame.Height(height)
	}
	if !expanded && height <= 0 {
		return
	}
	ui.Inert(frame, !expanded)
	frame.Children(func() {
		body := c.Key("disclosure-content")
		measured := ui.Column(body).FillWidth().Shrink(0)
		if *natural > 0 {
			measured.Absolute().Left(0).Right(0).Top(0)
		}
		measured.DrawOver(func(_ *ui.Painter, r ui.Rect) {
			if math.Abs(float64(*natural-r.H)) > .5 {
				if expanded && *natural == 0 {
					frame.Jump("height", r.H)
				}
				*natural = r.H
				c.Services().Invalidate()
			}
		}).Children(func() { content(body) })
	})
}

func mobileUserBubble(c *ui.Context) ui.Element {
	return ui.Column(c).AlignSelf(ui.End).MaxWidthPercent(89).Padding(10, 14).Gap(6).Radius(18).Background(palette(c).inset)
}

func mobileHeaderSize(c *ui.Context) float32 { return tapSize / c.Zoom() }

type mobileKeyboardMotion struct {
	Bottom, Peak     float32
	Moving, Morphing bool
}

func mobileHeaderButton(c *ui.Context, title string, symbol *ui.SVG, activate func()) ui.Element {
	size := mobileHeaderSize(c)
	button := iconButton(c, title, symbol, toolbarIconSize, activate).
		MinWidth(size).MinHeight(size).Size(size, size)
	return floatingSurface(c, button, size/2)
}

func mobileComposerSurface(c *ui.Context, key string, home, expanded bool, send *mobileSendScroll, keyboardProgress *float32, editingQueue ...bool) (ui.Element, mobileclient.ComposerMetrics) {
	surface := mobileComposerFrame(c.Key("composer-surface"), home)
	target := float32(0)
	if expanded {
		target = 1
	}
	channel := "composer-progress:" + key
	var progress float32
	if send != nil && send.operation != "" && !send.queued && !send.keyboardComposer {
		// Without a keyboard transition, flight also releases the input space.
		progress = send.from + (target-send.from)*send.progress
		surface.Jump(channel, progress)
	} else if keyboardProgress != nil {
		progress = *keyboardProgress
		surface.Jump(channel, progress)
	} else {
		duration := mobileReplyFade
		if len(editingQueue) > 0 && editingQueue[0] {
			duration = 360 * time.Millisecond
		}
		progress = surface.AnimateTo(channel, target, ui.Animation{Duration: duration, Ease: ui.EaseOut})
	}
	if send != nil {
		send.composerProgress = progress
		if send.progress >= 1 {
			send.operation = ""
		}
	}
	geometry := mobileclient.ComposerGeometry(progress, home, composerLineHeight(c), mobileTapSize(c))
	return surface.Height(geometry.Height), geometry
}

func composerLineHeight(c *ui.Context) float32 {
	return 1.24 * bodyTextSize * c.Preferences().TextScale
}

func mobileComposerFrame(c *ui.Context, home bool) ui.Element {
	radius := float32(27)
	if home {
		radius = 24
	}
	return floatingSurface(c, ui.Column(c).Shrink(0), radius)
}

func mobilePageBody(c *ui.Context, content func()) ui.Element {
	return ui.Scroll(c).Fill().Background(palette(c).canvas).Children(func() {
		mobilePageContent(c, 34).Children(content)
	})
}

func mobilePageWithFooter(c *ui.Context, content, footer func()) ui.Element {
	var scroll ui.Element
	ui.Column(c).Fill().Background(palette(c).canvas).Children(func() {
		scroll = ui.Scroll(c).Grow(1).Basis(0).MinHeight(0).Children(func() {
			mobilePageContent(c, pageInset).Children(content)
		})
		mobilePageFooter(c, footer)
	})
	return scroll
}

func mobilePageFooter(c *ui.Context, content func()) {
	if content == nil {
		return
	}
	mobileContent(c, contentMaxWidth).Padding(10, pageInset).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(palette(c).line).Children(content)
}

func mobileMetadataField(c *ui.Context, title, value, copyLabel string) {
	if value == "" {
		return
	}
	p := palette(c)
	ui.Row(c).Gap(8).AlignItems(ui.Start).MinHeight(mobileTapSize(c)).Children(func() {
		label(c, title, 12, p.secondary).Shrink(0).Width(76).PaddingY(12)
		text := label(c, value, 13, p.ink).Grow(1).MinWidth(0).PaddingY(12).Selectable()
		if copyLabel != "" {
			text.Font("monospace").FontSize(12 * c.Preferences().TextScale)
			mobileCopyButton(c.Key("copy"), copyLabel, value)
		}
	})
}

func mobileCopyButton(c *ui.Context, title, value string) {
	slot := ui.Box(c).Size(mobileTapSize(c), mobileTapSize(c)).Shrink(0)
	copied := ui.Local(slot, "copied-until", func() time.Time { return time.Time{} })
	showCopied := c.Now().Before(*copied)
	if showCopied {
		c.After(copied.Sub(c.Now()))
	}
	slot.Children(func() {
		copy := func() {
			c.WriteClipboardThen(value, func(err error) {
				if err == nil {
					*copied = time.Now().Add(2 * time.Second)
				}
			})
		}
		if showCopied {
			mobileButton(c, title, copy).Size(mobileTapSize(c), mobileTapSize(c)).AlignItems(ui.Center).Justify(ui.Center).
				Value("已复制").Children(func() { oneLine(c, "已复制", captionTextSize, palette(c).secondary).PassThrough() })
		} else {
			iconButton(c, title, copySymbol, 16, copy)
		}
	})
}

func mobileCard(c *ui.Context, glyph *ui.SVG, title, description string) {
	p := palette(c)
	ui.Column(c).Padding(12).Gap(7).Radius(cardRadius).Background(p.inset).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, glyph).Size(inlineIconSize, inlineIconSize).TextColor(p.secondary)
			label(c, title, bodyTextSize, p.ink).FontWeight(620)
		})
		if description != "" {
			label(c, description, 13, p.secondary)
		}
	})
}

func remoteStatus(c *ui.Context, glyph *ui.SVG, title, description string) {
	p := palette(c)
	ui.Column(c).Padding(7, 0, 8).Gap(4).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			alignedSymbol(c, glyph, title, ui.Font{Size: controlTextSize * c.Preferences().TextScale}, 16, p.secondary)
			label(c, title, controlTextSize, p.ink).FontWeight(550)
		})
		if description != "" {
			label(c, description, controlTextSize, p.ink).Padding(0, 0, 0, 24)
		}
	})
}

func remoteChoiceRow(c *ui.Context, title, subtitle string, selected bool, choose func()) ui.Element {
	p := palette(c)
	return mobileButton(c, title, choose).MinHeight(55).Padding(8, 3).Gap(12).AlignItems(ui.Center).
		AccessSelected(selected).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).PassThrough().Children(func() {
			label(c, title, controlTextSize, p.ink).FontWeight(560).PassThrough()
			label(c, subtitle, captionTextSize, p.secondary).PassThrough()
		})
		mark := ui.Box(c).Size(21, 21).Shrink(0).Radius(10.5).AlignItems(ui.Center).Justify(ui.Center).PassThrough()
		if selected {
			mark.Background(p.ink).Children(func() { ui.Icon(c, checkSymbol).Size(13, 13).TextColor(p.canvas).PassThrough() })
		} else {
			mark.Border(1, p.line)
		}
	})
}

func mobileMeter(c *ui.Context, value float64) {
	p := palette(c)
	ui.Box(c).Height(6).Margin(4, 0, 8).Radius(3).Background(p.inset).Clip().Children(func() {
		ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).WidthPercent(float32(min(100, max(0, value)))).
			Radius(3).Background(p.secondary)
	})
}

type mobileSegment struct {
	title    string
	disabled bool
}

func mobileSegments(c *ui.Context, segments []mobileSegment, selection string, change func(string)) {
	p := palette(c)
	if len(segments) >= 5 {
		scroll := ui.ScrollHorizontal(c).FillWidth()
		width, _ := c.Size()
		_, right, _, left := c.SafeArea()
		textScale := c.Preferences().TextScale
		type viewport struct {
			selected         string
			width, textScale float32
		}
		current := viewport{selection, width - left - right, textScale}
		previous := ui.Local(scroll, "segment-viewport", func() viewport { return viewport{} })
		reveal := *previous != current
		*previous = current
		scroll.Children(func() {
			widths := make([]float32, len(segments))
			var x, selectedWidth float32
			for i, segment := range segments {
				width, _ := c.MeasureText(0, ui.Span{Text: segment.title, Size: controlTextSize * textScale})
				widths[i] = max(mobileTapSize(c), width+24)
				if segment.title == selection {
					selectedWidth = widths[i]
				} else if selectedWidth == 0 {
					x += widths[i] + 4
				}
			}
			track := ui.Row(c).Gap(4)
			track.Children(func() {
				ui.Box(c.Key("track")).Absolute().Left(0).Right(0).Top(4).Bottom(4).
					Radius(16).Background(p.inset).ZIndex(-2).PassThrough()
				marker := ui.Box(c.Key("indicator")).Absolute().Top(7).Bottom(7).
					Radius(14).Background(p.canvas).ZIndex(-1).PassThrough()
				animation := ui.Animation{Duration: mobileTabDuration, Ease: ui.EaseOut}
				marker.Left(marker.AnimateTo("segment-x", x, animation)).
					Width(marker.AnimateTo("segment-width", selectedWidth, animation))
				for i, segment := range segments {
					title := segment.title
					tab := action(c.Key(title), title, func() { change(title) }).Role(ui.RoleTab).
						Width(widths[i]).AccessSelected(title == selection).Disabled(segment.disabled).Shrink(0)
					if reveal && title == selection {
						tab.ScrollIntoView()
					}
				}
			})
		})
		return
	}
	ui.Column(c).Children(func() {
		ui.Box(c.Key("track")).Absolute().Left(0).Right(0).Top(4).Bottom(4).
			Radius(18).Background(p.inset).ZIndex(-1).PassThrough()
		ui.Row(c).Margin(0, 3).Children(func() {
			width := 100 / float32(len(segments))
			marker := ui.Box(c.Key("indicator")).Absolute().Top(7).Bottom(7).WidthPercent(width).
				Radius(15).Background(p.canvas).ZIndex(-1).PassThrough()
			position := marker.AnimateTo("segment", float32(mobileSegmentIndex(segments, selection))*width, ui.Animation{Duration: mobileTabDuration})
			marker.LeftPercent(position)
			for _, segment := range segments {
				title := segment.title
				ink, weight := p.secondary, 400
				if title == selection {
					ink, weight = p.ink, 620
				}
				ui.ButtonBase(c.Key(title)).Grow(1).Basis(0).MinWidth(0).MinHeight(mobileTapSize(c)).PaddingX(2).
					Radius(21).AlignItems(ui.Center).Justify(ui.Center).
					Role(ui.RoleTab).Label(title).AccessSelected(title == selection).Disabled(segment.disabled).
					OnClick(func() { change(title) }).
					Children(func() { oneLine(c, title, 13, ink).FontWeight(weight).PassThrough() })
			}
		})
	})
}

func mobileSegmentIndex(segments []mobileSegment, selection string) int {
	for i, segment := range segments {
		if segment.title == selection {
			return i
		}
	}
	return 0
}

type mobileSettingsTabMotion struct {
	selected  string
	index     int
	direction float32
	exits     map[string]*ui.Motion
}

func mobileSettingsPanel(c *ui.Context, tab string, index int, content func()) {
	frame := ui.Column(c).Grow(1).Basis(0).MinHeight(0).Clip()
	motion := ui.Local(frame, "tab-motion", func() mobileSettingsTabMotion {
		return mobileSettingsTabMotion{selected: tab, index: index, exits: map[string]*ui.Motion{}}
	})
	if motion.selected != tab {
		motion.direction = 100
		if index < motion.index {
			motion.direction = -100
		}
		motion.exits[motion.selected].XPercent = -motion.direction
		motion.selected, motion.index = tab, index
	}
	if motion.exits[tab] == nil {
		motion.exits[tab] = &ui.Motion{Opacity: 1}
	}
	frame.Children(func() {
		ui.Column(c.Key(tab)).Fill().Transition(ui.ElementTransition{Duration: mobileTabDuration,
			Enter: &ui.Motion{XPercent: motion.direction, Opacity: 1}, Exit: motion.exits[tab],
		}).Children(content)
	})
}

func mobileSection(c *ui.Context, title string) {
	ui.Row(c).MinHeight(34).AlignItems(ui.Center).Children(func() {
		label(c, title, 13, palette(c).secondary).FontWeight(600)
	})
}

func mobileGroupHeading(c *ui.Context, title string) {
	ui.Spacer(c).Height(17).Shrink(0)
	mobileSection(c, title)
}

func mobilePageContext(c *ui.Context, parts ...string) {
	var values []string
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	if len(values) > 0 {
		label(c, strings.Join(values, " · "), captionTextSize, palette(c).secondary).
			MaxLines(2).Ellipsis("…").Padding(5, 0, 6)
	}
}

func mobileSearchField(c *ui.Context, row ui.Element, query *string, title, hint string, trailing func()) ui.Element {
	p := palette(c)
	var input ui.Element
	row.MinHeight(mobileTapSize(c)).Gap(7).PaddingX(10).Radius(cardRadius).Background(p.inset).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, searchSymbol).Size(16, 16).TextColor(p.secondary).Shrink(0).PassThrough()
		input = ui.TextInputBase(c, query).Grow(1).MinWidth(0).Height(mobileTapSize(c)).PaddingX(0).
			FontSize(controlTextSize * c.Preferences().TextScale).TextColor(p.ink).PlaceholderColor(p.secondary).
			Label(title).Placeholder(hint)
		if trailing != nil {
			trailing()
		}
	})
	return input
}

func primaryAction(c *ui.Context, title string, onClick func()) ui.Element {
	p := palette(c)
	return filledAction(c, title, p.ink, p.canvas, onClick).Radius(pillRadius)
}

func filledAction(c *ui.Context, title string, fill, ink ui.Color, onClick func()) ui.Element {
	return mobileButton(c, title, onClick).Padding(4, 12).AlignItems(ui.Center).Justify(ui.Center).
		Background(fill).Children(func() {
		label(c, title, 13, ink).FontWeight(620).TextAlign(ui.Center).PassThrough()
	})
}

func secondaryAction(c *ui.Context, title string, onClick func()) ui.Element {
	return action(c, title, onClick).Radius(pillRadius).Background(palette(c).inset)
}

func mobileRow(c *ui.Context, title, subtitle, value string, icon *ui.SVG, open func()) ui.Element {
	return mobileDetailRow(c, title, subtitle, title, icon, open, func() {
		if value != "" {
			oneLine(c, value, 13, palette(c).secondary).MaxWidthPercent(42).PassThrough()
		}
	})
}

func mobileDetailRow(c *ui.Context, title, subtitle, accessibilityName string, icon *ui.SVG, open, tail func()) ui.Element {
	var row ui.Element
	if open == nil {
		row = ui.Row(c)
	} else {
		row = ui.ButtonBase(c).Role(ui.RoleButton).Label(accessibilityName).OnClick(open)
	}
	return mobileRowContent(c, row.MinHeight(max(contentRowHeight, mobileTapSize(c))), title, subtitle, icon, open != nil, tail)
}

func mobileRowContent(c *ui.Context, row ui.Element, title, subtitle string, icon *ui.SVG, disclosure bool, tail func()) ui.Element {
	p := palette(c)
	return row.AlignItems(ui.Center).Padding(8, 0).Gap(8).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Start).Gap(6).PassThrough().Children(func() {
			if icon != nil {
				font := mobileStatusFont(c, bodyTextSize)
				line := ui.ShapeText(title, font, 0).Lines()
				height := font.Size
				if len(line) > 0 {
					height = line[0].Ascent + line[0].Descent
				}
				ui.Column(c).Width(24).Height(height).Shrink(0).AlignItems(ui.Start).Justify(ui.Center).PassThrough().
					Children(func() { alignedSymbol(c, icon, title, font, inlineIconSize, p.secondary) })
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).PassThrough().Children(func() {
				label(c, title, bodyTextSize, p.ink).PassThrough()
				if subtitle != "" {
					label(c, subtitle, captionTextSize, p.secondary).MaxLines(2).Ellipsis("…").PassThrough()
				}
			})
		})
		if tail != nil {
			tail()
		}
		if disclosure {
			ui.Icon(c, rightSymbol).Size(13, 13).TextColor(p.secondary).PassThrough()
		}
	})
}

func mobileMessage(c *ui.Context, message string) {
	label(c, message, controlTextSize, palette(c).secondary).Padding(10, 0)
}

func mobileButton(c *ui.Context, title string, onClick func()) ui.Element {
	return ui.ButtonBase(c).MinHeight(mobileTapSize(c)).MinWidth(mobileTapSize(c)).Role(ui.RoleButton).Label(title).OnClick(onClick)
}

func transcriptDisclosure(button ui.Element) ui.Element {
	return button.FillWidth().Justify(ui.Start).KeepFocus()
}

func action(c *ui.Context, title string, onClick func()) ui.Element {
	p := palette(c)
	return mobileButton(c, title, onClick).PaddingX(12).AlignItems(ui.Center).Justify(ui.Center).
		Children(func() { oneLine(c, title, controlTextSize, p.ink) })
}

func mobileTextArea(c *ui.Context, value *string, title string) ui.Element {
	p := palette(c)
	return ui.TextAreaBase(c, value).Label(title).Multiline(true).Padding(8).Radius(cardRadius).
		Background(p.inset).TextColor(p.ink).PlaceholderColor(p.secondary).FontSize(bodyTextSize * c.Preferences().TextScale)
}

func menuChoice(m *ui.Menu, text string, selected bool, change func()) {
	if m.Item(text).Checked(selected).Chosen() {
		change()
	}
}

func formField(c *ui.Context, title, placeholder string, value *string) ui.Element {
	return formRow(c, title, func() {
		formInput(c, title, placeholder, value)
	})
}

func formInput(c *ui.Context, title, placeholder string, value *string) ui.Element {
	p := palette(c)
	return ui.TextInputBase(c, value).WidthPercent(58).MaxWidth(245).MinWidth(0).MinHeight(mobileTapSize(c)).
		Label(title).Placeholder(placeholder).TextColor(p.ink).PlaceholderColor(p.secondary).
		FontSize(bodyTextSize * c.Preferences().TextScale).TextAlign(ui.End)
}

func formRow(c *ui.Context, title string, control func()) ui.Element {
	p := palette(c)
	return rowDivider(ui.Row(c).MinHeight(formRowHeight).Padding(6, 0).AlignItems(ui.Center).Gap(10), p.line).Children(func() {
		label(c, title, bodyTextSize, p.ink).Grow(1).MinWidth(0)
		control()
	})
}

func rowDivider(row ui.Element, color ui.Color) ui.Element {
	return row.BorderWidth(0, 0, 1, 0).BorderColor(color)
}

func formChoice(c *ui.Context, title, value string, items func(*ui.Menu)) ui.Element {
	return formRow(c, title, func() {
		iconAction(c, value, title, nil, 0, pickerSymbol, pickerIconSize, bodyTextSize, 400, nil).PaddingX(0).
			MaxWidthPercent(62).Value(value).Menu(items)
	})
}

func mobileChoiceRow(c *ui.Context, title, subtitle string, selected bool, choose func()) {
	p := palette(c)
	fill := p.canvas
	if selected {
		fill = p.selected
	}
	mobileButton(c, title, choose).Padding(6, 10).Gap(10).Radius(9).Background(fill).
		AlignItems(ui.Center).AccessSelected(selected).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).PassThrough().Children(func() {
			label(c, title, controlTextSize, p.ink).FontWeight(570).PassThrough()
			if subtitle != "" {
				label(c, subtitle, captionTextSize, p.secondary).PassThrough()
			}
		})
		ui.Box(c).Size(18, 18).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Radius(9).
			Border(1, p.secondary).PassThrough().Children(func() {
			if selected {
				ui.Icon(c, checkSymbol).Size(12, 12).TextColor(p.ink).PassThrough()
			}
		})
	})
}

func mobileToggle(c *ui.Context, title string, value *bool, changed func()) ui.Element {
	return formRow(c, title, func() {
		mobileSwitch(c, title, value, changed)
	})
}

func mobileSwitch(c *ui.Context, title string, value *bool, changed func()) ui.Element {
	p := palette(c)
	return ui.SwitchBase(c, value).Size(mobileTapSize(c), mobileTapSize(c)).Shrink(0).AlignItems(ui.Center).Justify(ui.End).Label(title).OnChange(changed).
		Children(func() {
			fill, thumb := p.inset, p.secondary
			offset := float32(0)
			if *value {
				fill, thumb = p.ink, p.canvas
				offset = 18
			}
			ui.Column(c).Size(43, 25).Radius(13).Background(fill).Border(1, p.line).PassThrough().Children(func() {
				knob := ui.Box(c).Size(17, 17).Radius(8.5).Background(thumb).Absolute().Top(3).PassThrough()
				knob.Left(3 + knob.AnimateTo("toggle-position", offset, mobileLayoutMotion))
			})
		})
}
