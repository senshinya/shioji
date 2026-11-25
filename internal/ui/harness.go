package ui

import (
	_ "embed"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// Google Antigravity monochrome brand asset: https://antigravity.google/press
//
//go:embed assets/antigravity-monochrome.svg
var antigravityMarkSVG []byte

var antigravityMark = n.MustParseSVG(antigravityMarkSVG)
var genericAgentIcon = svg("M3 4h14v12H3zM6 8l2 2-2 2M11 12h3")

// Icon tokens are presentation catalog data. Execution selection never depends
// on an icon or display name; unrecognized artwork uses the neutral fallback.
func harnessIcon(d domain.HarnessDescriptor) *n.SVG {
	icons := map[string]*n.SVG{
		"antigravity": antigravityMark,
		"openai":      openAIBlossom,
	}
	if icon := icons[d.Icon]; icon != nil {
		return icon
	}
	return genericAgentIcon
}

const systemProfileLabel = "系统"
const systemProfileHelp = "跟随所属设备上的 Agent 原生配置，包括账号或 API 接入"

func isSystemAgentProfile(info domain.HarnessInfo) bool {
	// The catalog only attaches a Profile to isolated configurations.
	// ProfileModes advertises creation options and can be absent on native entries.
	return info.ID != "" && info.Profile == nil
}

func systemProfileBadge(c *n.Context, info domain.HarnessInfo) {
	if isSystemAgentProfile(info) {
		t := c.Theme()
		n.Text(c, systemProfileLabel).FontSize(11).FixedLineHeight(16).PaddingX(4).Radius(4).
			TextColor(t.TextMuted).Background(t.Text.Alpha(.065)).Shrink(0).
			Description(systemProfileHelp).Tooltip(systemProfileHelp)
	}
}

func runAgentCaption(ready domain.Readiness) string {
	name := ready.Descriptor().Name
	for _, info := range ready.Harnesses {
		if info.ID == ready.HarnessID && isSystemAgentProfile(info) {
			return name + " · " + systemProfileLabel
		}
	}
	return name
}
