package ui

import (
	"testing"

	n "github.com/egoist/mygo/ui"
)

func TestThemeCardsEncloseTheirLabels(t *testing.T) {
	for _, window := range []struct {
		name          string
		width, height int
		dark          bool
	}{
		{"default-light", 968, 756, false},
		{"minimum-dark", 760, 600, true},
	} {
		t.Run(window.name, func(t *testing.T) {
			m := layoutModel()
			m.Preferences.Appearance = "system"
			s := NewSettings(New(m, nil, ""))
			ui := n.NewTester(s.View, window.width, window.height)
			ui.SetDark(window.dark)
			saveNativeSkillImage(t, ui, "appearance-"+window.name+".png")
			for _, label := range []string{"浅色", "深色", "跟随系统"} {
				card, hasCard := ui.Find("外观" + label)
				footer, hasFooter := ui.Find(label)
				if !hasCard || !hasFooter || footer.Y <= card.Y || footer.Y+footer.H >= card.Y+card.H {
					t.Errorf("theme %q footer %v is outside its card %v", label, footer, card)
				}
			}
		})
	}
}
