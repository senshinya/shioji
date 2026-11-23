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
