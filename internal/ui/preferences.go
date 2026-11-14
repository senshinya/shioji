package ui

import (
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
)

func replyTheme(p app.ClientPreferences) *markdown.Theme {
	return &markdown.Theme{Font: p.InterfaceFont, FontSize: float32(p.ReplySize), CodeFont: p.CodeFont, CodeSize: 12 / float32(p.ReplySize), LineHeight: 24.0 / 14, BlockSpacing: 8 / float32(p.ReplySize), LinkWeight: 500, LinkUnderline: true}
}

func composerBinding(mods n.Modifiers) string {
	switch mods {
	case 0:
		return "Enter"
	case n.Shift:
		return "Shift+Enter"
	case n.Cmd:
		return "Cmd+Enter"
	case n.Cmd | n.Shift:
		return "Cmd+Shift+Enter"
	case n.Ctrl:
		return "Ctrl+Enter"
	case n.Alt:
		return "Alt+Enter"
	}
	return ""
}
