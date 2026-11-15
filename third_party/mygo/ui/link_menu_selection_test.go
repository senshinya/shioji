package ui

import "testing"

func TestInlineLinkMenuRetainsSelectedParagraphEditingItems(t *testing.T) {
	tt := NewTester(func(c *Context) {
		popupTestTheme(c)
		Column(c).Selectable().Children(func() {
			RichText(c).Label("paragraph").Children(func() {
				Text(c, "before ")
				Link(c, "link", "").ContextMenu(func(m *Menu) { m.Item("copy link"); m.EditItems() })
				Text(c, " after")
			})
		})
	}, 480, 240)
	p, _ := tt.Find("paragraph")
	tt.ClickAt(p.X+3, p.Y+p.H/2)
	tt.Key(Cmd, KeyA)
	tt.Key(Cmd, KeyC)
	if tt.Clipboard() != "before link after" {
		t.Fatal("fixture did not select paragraph", tt.Clipboard())
	}
	r, _ := tt.Find("link")
	tt.RightClickAt(r.X+3, r.Y+r.H/2)
	if err := tt.ChooseMenuItem("Copy"); err != nil {
		t.Fatal("link displaced selected text commands", err, tt.Menu())
	}
	if tt.Clipboard() != "before link after" {
		t.Fatal("link right click cleared selection", tt.Clipboard())
	}
}
