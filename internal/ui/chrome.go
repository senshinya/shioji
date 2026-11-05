package ui

import (
	_ "embed"
	n "github.com/egoist/mygo/ui"
)

// OpenAI brand asset: https://cdn.openai.com/brand/OpenAI-Logos-2025.zip
//
//go:embed assets/openai-monoblossom.svg
var openAIBlossomSVG []byte

var openAIBlossom = n.MustParseSVG(openAIBlossomSVG)

var searchIcon = svg("M14 14l4 4M15 8.5a6.5 6.5 0 1 1-13 0 6.5 6.5 0 0 1 13 0")
var settingsIcon = svg("M8 2h4l.6 2.2 2 .9 2-.8 2 3.4-1.7 1.6v2.3l1.7 1.5-2 3.5-2-.8-2 .9L12 18H8l-.6-2.2-2-.9-2 .8-2-3.5 1.7-1.5V8.4L1.4 6.8l2-3.4 2 .8 2-.9zM13 10a3 3 0 1 1-6 0 3 3 0 0 1 6 0")

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

func navButton(c *n.Context, icon *n.SVG, label string) n.Element {
	color, size := c.Theme().TextMuted, float32(13)
	if label == "新建会话" {
		color = c.Theme().Text
	}
	if label == "设备" {
		size = 12
	}
	return control(c, 8).FillWidth().Height(28).PaddingX(8).Justify(n.Start).Radius(8).Label(label).Children(func() {
		inlineIconText(c, icon, label, n.Font{Family: "system-ui", Size: size, Weight: 400}, 16, 8, color)
	})
}
