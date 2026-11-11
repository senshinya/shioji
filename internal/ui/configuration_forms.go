package ui

import (
	n "github.com/egoist/mygo/ui"
	"time"
)

type configurationFormStyle uint8

const (
	configurationSettingsForm configurationFormStyle = iota
	configurationDeviceForm
	configurationSetupForm
)

func configurationFormRow(c *n.Context, label string, style configurationFormStyle, build func(), top ...bool) {
	row := n.Row(c.Key(label)).FillWidth().MinHeight(configurationRowHeight).Gap(configurationSectionGap).AlignItems(n.Center)
	alignTop := style == configurationSetupForm || len(top) > 0 && top[0]
	if alignTop {
		row.PaddingY(6).AlignItems(n.Start)
	}
	if style == configurationSettingsForm {
		row.BorderWidth(0, 0, .5, 0).BorderColor(c.Theme().Border)
	}
	row.Children(func() {
		labelView := n.Text(c, label).FontSize(configurationLabelSize).FixedLineHeight(configurationTextLine).Grow(1).Transform(n.Translation(0, inlineCenterOffset(label, n.Font{Family: c.Theme().Font, Size: configurationLabelSize, Weight: 400})))
		if alignTop {
			labelView.PaddingY(4)
		}
		if style == configurationSettingsForm {
			build()
			return
		}
		width := float32(320)
		if style == configurationDeviceForm {
			available, _ := c.Size()
			width = min(320, (available-296)*.55)
			if available < 900 {
				width = min(320, (available-64)*.55)
			}
		}
		n.Column(c).Width(width).Children(build)
	})
}

func configurationTextField(c *n.Context, value *string) n.Element {
	return textField(c, value).FillWidth().Height(configurationControlHeight).Padding(4, 8).FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).Background(c.Theme().Background)
}
func configurationTextArea(c *n.Context, value *string) n.Element {
	return fieldFeedback(c, n.TextAreaBase(c, value).FillWidth().Lines(3, 6).FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).Padding(4, 8).Radius(5).Background(c.Theme().Background).Border(1, c.Theme().Border))
}
func configurationChoiceField(c *n.Context, label, value string, disabled bool) n.Element {
	font := n.Font{Family: c.Theme().Font, Size: configurationValueSize, Weight: 400}
	return control(c, 5).FillWidth().Height(configurationControlHeight).PaddingX(8).Radius(5).Border(1, c.Theme().Border).Label(label).Disabled(disabled).Children(func() {
		n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Transform(n.Translation(0, inlineCenterOffset(value, font))).Children(func() {
			n.Text(c, value).SingleLine().FontSize(configurationValueSize).FixedLineHeight(configurationTextLine).Grow(1).MinWidth(0).Tooltip(value).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			inlineSymbol(c, chevron, value, font, 12, c.Theme().TextMuted)
		})
	})
}
func configurationSectionTitle(c *n.Context, label string) n.Element {
	return n.Text(c, label).FontSize(configurationSectionSize).FontWeight(500).FixedLineHeight(configurationTitleLine)
}
func configurationAction(c *n.Context, label string) n.Element {
	return ghost(c, label, 6).Height(configurationControlHeight).PaddingX(8).FontSize(configurationValueSize)
}

// Native checkbox semantics with the same font-baseline object slot as inline
// icons. Empty captions can use the neighboring first-line title for its ink.
func configurationCheckbox(c *n.Context, value *bool, label string, font n.Font, reference ...string) n.Element {
	ink := label
	if len(reference) > 0 {
		ink = reference[0]
	}
	t := c.Theme()
	e := n.CheckboxBase(c, value).Height(configurationTextLine).Gap(8).AlignItems(n.Baseline).Label(label)
	on := *value
	focused := e.FocusVisible()
	e.Children(func() {
		n.RichText(c, inlineIconSpan(ink, font, 14, 0)).Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).FixedLineHeight(configurationTextLine).DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
			if on {
				p.Fill(r, t.Accent, 3)
				p.Icon(check, r, t.AccentText)
			} else {
				p.Fill(r, t.Background, 3)
				p.Stroke(r, t.Border.Mix(t.Text, .25), 3, 1)
			}
			if focused {
				p.FocusRing(r, [4]float32{3, 3, 3, 3})
			}
		})
		if label != "" {
			n.Text(c, label).FontSize(font.Size).FixedLineHeight(configurationTextLine).TextColor(t.TextMuted)
		}
	})
	return e
}
