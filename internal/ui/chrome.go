package ui

import (
	_ "embed"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
)

// OpenAI brand asset: https://cdn.openai.com/brand/OpenAI-Logos-2025.zip
//
//go:embed assets/openai-monoblossom.svg
var openAIBlossomSVG []byte

var openAIBlossom = n.MustParseSVG(openAIBlossomSVG)

var searchIcon = svg("M14 14l4 4M15 8.5a6.5 6.5 0 1 1-13 0 6.5 6.5 0 0 1 13 0")

// Eight equally spaced teeth keep the visible contour square in a 16px slot.
var settingsIcon = svg("M16.18 8.34 18.12 8.86 18.12 11.14 16.18 11.66 15.54 13.2 16.55 14.94 14.94 16.55 13.2 15.54 11.66 16.18 11.14 18.12 8.86 18.12 8.34 16.18 6.8 15.54 5.06 16.55 3.45 14.94 4.46 13.2 3.82 11.66 1.88 11.14 1.88 8.86 3.82 8.34 4.46 6.8 3.45 5.06 5.06 3.45 6.8 4.46 8.34 3.82 8.86 1.88 11.14 1.88 11.66 3.82 13.2 4.46 14.94 3.45 16.55 5.06 15.54 6.8ZM13 10a3 3 0 1 1-6 0 3 3 0 0 1 6 0")

// Frozen paths retain their original 24px coordinate system.
func designIcon(path string) *n.SVG {
	return n.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="` + path + `" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
}

var shieldIcon = designIcon("M12 2 3 6v6c0 6 9 10 9 10s9-4 9-10V6ZM8 12l3 3 5-6")
var disclosureDown = designIcon("m6 9 6 6 6-6")
var disclosureRight = designIcon("m9 5 7 7-7 7")
var attentionIcon = svg("M10 3l8 14H2zM10 7v4M10 14v.2")
var sidebarIcon = svg("M2 3h16v14H2zM12 3v14")
var copyReplyIcon = svg("M7 3h9a1 1 0 0 1 1 1v10M4 6h9a1 1 0 0 1 1 1v10H3V7a1 1 0 0 1 1-1z")
var closeIcon = svg("M5 5l10 10M15 5L5 15")
var branchIcon = designIcon("M6 3v12a4 4 0 0 0 8 0V8M6 5a2 2 0 1 0 0 .01M14 5a2 2 0 1 0 0 .01M6 18a2 2 0 1 0 0 .01")

// WindowTrafficLightPosition is shared by the main and preference windows.
func WindowTrafficLightPosition() *mygo.Point { return &mygo.Point{X: 22, Y: 21} }

const (
	sidebarHeaderTopInset = 6
	sidebarTitlebarHeight = 28
	sidebarHeaderGap      = 4
)

func navButton(c *n.Context, icon *n.SVG, label string) n.Element {
	color, size := c.Theme().TextMuted, float32(13)
	if label == "新建会话" {
		color = c.Theme().Text
	}
	if label == "设备" {
		size = 12
	}
	return navButtonWithColors(c, icon, label, size, color, color, nil)
}

func navButtonWithColors(c *n.Context, icon *n.SVG, label string, size float32, textColor, iconColor n.Color, trailing func()) n.Element {
	return control(c, 8).FillWidth().Height(28).PaddingX(8).Justify(n.Start).Radius(8).Label(label).Children(func() {
		inlineIconText(c, icon, label, n.Font{Family: c.Theme().Font, Size: size, Weight: 400}, 16, 8, textColor).DrawObjects(func(p *n.Painter, _ int, r n.Rect) {
			r.W = 16
			p.Icon(icon, r, iconColor)
		})
		if trailing != nil {
			n.Spacer(c).Grow(1)
			trailing()
		}
	})
}

func (v *View) attentionControl(c *n.Context, t *n.Theme) {
	_, count := v.attentionSummary()
	available := count > 0
	anchor := n.Column(c.Key("attention-entry")).FillWidth()
	anchor.Children(func() {
		textColor, iconColor := t.TextMuted, t.TextMuted
		var trailing func()
		description := "无待处理会话"
		if available {
			textColor, iconColor = t.Text, n.Hex("#c99700")
			if t.Dark {
				iconColor = n.Hex("#c4a30a")
			}
			value := strconv.Itoa(count)
			description = value + " 个会话待处理"
			trailing = func() {
				n.Text(c, value).Font("Menlo").FontSize(11).FixedLineHeight(20).MinWidth(24).Shrink(0).TextAlign(n.End).
					TextColor(t.TextMuted).Label("待处理会话数：" + value).Role(n.RoleNone)
			}
		}
		button := navButtonWithColors(c, attentionIcon, "待我处理", 13, textColor, iconColor, trailing).
			Description(description).Disabled(!available)
		if !available {
			button.Opacity(.5)
		}
		if button.Clicked() {
			v.nextAttention()
		}
	})
	if !available && anchor.Hovered() && !c.MenuOpen() {
		n.Overlay(c, func() {
			n.Column(c.Key("empty-attention-hint")).Absolute().AttachTo(anchor, n.AnchorEnd, n.AnchorStart).MarginStart(8).PassThrough().Role(n.RoleTooltip).Padding(6, 10).Radius(6).Background(raised(t)).Border(1, t.Border).Shadow(0, 2, 8, 0, n.RGBA(0, 0, 0, .12)).Transition(fadeMotion(c, 100*time.Millisecond)).Children(func() { n.Text(c, "无待处理会话").FontSize(12).FixedLineHeight(18).TextColor(t.Text) })
		})
	}
}
