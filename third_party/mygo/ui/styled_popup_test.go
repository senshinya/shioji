package ui

import (
	"testing"
	"time"
)

func popupTestTheme(c *Context) {
	t := *c.Theme()
	t.MenuStyle = &MenuStyle{Surface: Hex("#ffffff"), Selection: Hex("#eeeeee"), RowHeight: 26, Inset: 3, Radius: 7, FontSize: 13, SubmenuDelay: 200 * time.Millisecond}
	t.TooltipStyle = &TooltipStyle{Surface: t.Background, Text: t.Text, Border: t.Border, Radius: 6, PaddingY: 6, PaddingX: 10, FontSize: 12, LineHeight: 18, MaxWidth: 320, Gap: 6, DisabledHints: true}
	c.SetTheme(&t)
	c.UsePopoverMenus()
}

func TestStyledAutomaticEditingMenuPreservesSelectedText(t *testing.T) {
	draft := "hello world"
	tt := NewTester(func(c *Context) { popupTestTheme(c); TextInput(c, &draft).Label("draft").Size(220, 32) }, 400, 240)
	tt.Click("draft")
	tt.Key(Cmd, KeyA)
	tt.RightClick("draft")
	if !tt.HasText("Copy") {
		t.Fatal("automatic menu still uses platform renderer", tt.Menu())
	}
	if err := tt.ChooseMenuItem("Copy"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "hello world" {
		t.Fatal("selection lost", tt.Clipboard())
	}
	if !tt.Focused("draft") {
		t.Fatal("menu did not restore editing focus")
	}
}

func TestStyledVirtualTextEditingMenuUsesSameRenderer(t *testing.T) {
	var list ListState
	tt := NewTester(func(c *Context) {
		popupTestTheme(c)
		List(c, &list, 100, func(i int) { Text(c, "selectable virtual row").Height(28).Selectable() }).Size(300, 150)
	}, 400, 240)
	tt.RightClick("selectable virtual row")
	if !tt.HasText("Copy") {
		t.Fatal("virtual text escaped to platform menu", tt.Menu())
	}
}

func TestStyledMenuClosesOnBlurAndOutsideScroll(t *testing.T) {
	tt := NewTester(func(c *Context) {
		popupTestTheme(c)
		ButtonBase(c).Label("source").Size(120, 28).ContextMenu(func(m *Menu) { m.Item("action") })
	}, 400, 240)
	tt.RightClick("source")
	tt.SetFocused(false)
	if tt.HasText("action") {
		t.Fatal("menu survived window blur")
	}
	tt.SetFocused(true)
	tt.RightClick("source")
	tt.Scroll(300, 150, 0, 40)
	if tt.HasText("action") {
		t.Fatal("menu survived outside scrolling")
	}
}

func TestStyledMenuSubmenuWaitsWithoutStealingFocus(t *testing.T) {
	tt := NewTester(func(c *Context) {
		popupTestTheme(c)
		ButtonBase(c).Label("source").Size(120, 28).ContextMenu(func(m *Menu) { m.Submenu("more", func(m *Menu) { m.Item("child") }); m.Item("last") })
	}, 400, 240)
	tt.RightClick("source")
	r, _ := tt.Find("more")
	tt.Move(r.X+10, r.Y+10)
	tt.Advance(199 * time.Millisecond)
	if tt.HasText("child") {
		t.Fatal("submenu opened without hover delay")
	}
	tt.Advance(time.Millisecond)
	if !tt.HasText("child") {
		t.Fatal("delayed submenu never opened")
	}
	tt.Key(0, KeyEscape)
	if tt.HasText("child") {
		t.Fatal("Escape did not close submenu")
	}
	tt.Key(0, KeyEscape)
	if tt.HasText("more") {
		t.Fatal("root Escape did not close menu")
	}
}

func TestStyledTooltipExplainsDisabledWithoutOfferingAction(t *testing.T) {
	clicks := 0
	tt := NewTester(func(c *Context) {
		popupTestTheme(c)
		b := ButtonBase(c).Label("disabled").Size(120, 28).Disabled(true).Tooltip("not connected")
		if b.Clicked() {
			clicks++
		}
	}, 400, 240)
	r, _ := tt.Find("disabled")
	tt.Move(r.X+10, r.Y+10)
	tt.Advance(601 * time.Millisecond)
	if !tt.HasText("not connected") {
		t.Fatal("disabled reason cannot be discovered")
	}
	tt.ClickAt(r.X+10, r.Y+10)
	if clicks != 0 {
		t.Fatal("disabled explanation enabled action")
	}
}
