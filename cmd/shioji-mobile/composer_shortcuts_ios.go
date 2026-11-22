package main

import "github.com/egoist/mygo/ui"

func mobileComposerBinding(mods ui.Modifiers) string {
	switch mods {
	case 0:
		return "Enter"
	case ui.Shift:
		return "Shift+Enter"
	case ui.Super:
		return "Cmd+Enter"
	case ui.Super | ui.Shift:
		return "Cmd+Shift+Enter"
	case ui.Ctrl:
		return "Ctrl+Enter"
	case ui.Alt:
		return "Alt+Enter"
	default:
		return ""
	}
}
