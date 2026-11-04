package ui

import n "github.com/egoist/mygo/ui"

// Frozen semantic colors; keep each appearance independent.
func subtle(t *n.Theme) n.Color {
	if t.Dark {
		return n.Hex("#969696")
	}
	return n.Hex("#707070")
}
func codeSurface(t *n.Theme) n.Color {
	if t.Dark {
		return n.Hex("#1b1b1b")
	}
	return n.Hex("#f5f5f5")
}
func userBubble(t *n.Theme) n.Color {
	if t.Dark {
		return n.Hex("#1b1b1b")
	}
	return n.Hex("#f4f4f4")
}
func gitColors(t *n.Theme) (added, deleted, addedFill, deletedFill n.Color) {
	if t.Dark {
		return n.Hex("#99ada0"), n.Hex("#c39b94"), n.Hex("#252e28"), n.Hex("#332928")
	}
	return n.Hex("#45634f"), n.Hex("#90564f"), n.Hex("#f0f5f1"), n.Hex("#faf1ef")
}
