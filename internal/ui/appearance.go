package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"time"
)

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

// Text selections need more separation from raised editing surfaces than
// the subdued background used for selected navigation rows.
func editorSelection(t *n.Theme) n.Color {
	if t.Dark {
		return n.Hex("#505050")
	}
	return n.Hex("#e7e7e7")
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

func applyAppearance(c *n.Context, prefs app.ClientPreferences) *n.Theme {
	t := *c.Theme()
	if prefs.Appearance != "system" {
		t.Dark = prefs.Appearance == "dark"
	}
	t.FontSize = 13
	t.Font = prefs.InterfaceFont
	t.FontAntialiased = true
	t.Spacing = 3
	t.Radius = 6
	if t.Dark {
		t.Background = n.Hex("#101010")
		t.Surface = n.Hex("#202020")
		t.SurfaceHover = n.Hex("#252525")
		t.SurfacePressed = n.Hex("#363636")
		t.Border = n.Hex("#343434")
		t.Text = n.Hex("#e9e9e9")
		t.TextMuted = n.Hex("#ababab")
		t.Accent = n.Hex("#e5e5e5")
		t.AccentText = n.Hex("#181818")
		t.Selection = n.Hex("#303030")
		t.Focus = n.Hex("#8c8c90")
		t.Success = n.Hex("#32d769")
		t.Warning = n.Hex("#ff9f0a")
		t.Danger = n.Hex("#ff6257")
	} else {
		t.Background = n.Hex("#ffffff")
		t.Surface = n.Hex("#f4f4f4")
		t.SurfaceHover = n.Hex("#eeeeee")
		t.SurfacePressed = n.Hex("#e7e7e7")
		t.Border = n.Hex("#e3e3e3")
		t.Text = n.Hex("#1c1c1c")
		t.TextMuted = n.Hex("#666666")
		t.Accent = n.Hex("#262626")
		t.AccentText = n.Hex("#ffffff")
		t.Selection = n.Hex("#dfdfdf")
		t.Focus = n.Hex("#858589")
		t.Success = n.Hex("#138a43")
		t.Warning = n.Hex("#8f4c00")
		t.Danger = n.Hex("#d92d20")
	}
	t.AccentHover = t.Accent
	t.AccentPressed = t.Accent
	t.MenuStyle = &n.MenuStyle{Surface: raised(&t), Selection: menuSelectionSurface(&t), RowHeight: compactMenuRowHeight, Inset: compactMenuInset, Radius: compactMenuRadius, FontSize: compactMenuFontSize, SubmenuDelay: 200 * time.Millisecond}
	t.TooltipStyle = &n.TooltipStyle{Surface: raised(&t), Text: t.Text, Border: t.Border, Radius: 6, PaddingY: 6, PaddingX: 10, FontSize: 12, LineHeight: 18, MaxWidth: 320, Gap: 6, DisabledHints: true}
	c.SetTheme(&t)
	c.UsePopoverMenus()
	return &t
}
