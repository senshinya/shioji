package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

var genericAgentIcon = svg("M3 4h14v12H3zM6 8l2 2-2 2M11 12h3")

// Icon tokens are presentation catalog data. Execution selection never depends
// on an icon or display name; unrecognized artwork uses the neutral fallback.
func harnessIcon(d domain.HarnessDescriptor) *n.SVG {
	icons := map[string]*n.SVG{"openai": openAIBlossom}
	if icon := icons[d.Icon]; icon != nil {
		return icon
	}
	return genericAgentIcon
}
