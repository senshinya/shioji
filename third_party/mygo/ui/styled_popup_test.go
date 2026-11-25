package ui

import (
	"strings"
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

func TestLongTooltipFitsWholeLinesBesideItsAnchor(t *testing.T) {
	const width, height = 240, 225
	command := "/bin/zsh -lc 'printf example\n" + strings.Repeat("printf %s \"Shioji 命令详情\"\n", 30) + "'"
	for _, top := range []float32{12, 100, 191} {
		tt := NewTester(func(c *Context) {
			popupTestTheme(c)
			ButtonBase(c).Absolute().Top(top).Left(12).Size(100, 22).Label("command").Tooltip(command)
		}, width, height)
		tt.Move(50, top+10)
		tt.Advance(time.Second)

		overlay := tt.rt.c.root.last
		tip := overlay.first
		if tip == nil || tip.role != RoleTooltip {
			t.Fatal("command tooltip did not open")
		}
		label := tip
		if tip.first != nil {
			label = tip.first
		}
		if label.tl == nil || !label.tl.Truncated || len(label.tl.Lines) < 2 {
			t.Fatal("long tooltip should end with a truncated preview of whole lines")
		}
		bounds := tip.Bounds()
		textBottom := label.Bounds().Y + label.contentY() + label.tl.Height
		if bounds.X < windowMargin || bounds.X+bounds.W > width-windowMargin ||
			bounds.Y < windowMargin || bounds.Y+bounds.H > height-windowMargin ||
			textBottom > bounds.Y+bounds.H {
			t.Fatalf("tooltip text overflows at anchor y=%g: bounds=%+v, text bottom=%g", top, bounds, textBottom)
		}
		if top == 12 && bounds.Y < top+22 || top == 191 && bounds.Y+bounds.H > top {
			t.Fatalf("tooltip does not use the open side of anchor y=%g: %+v", top, bounds)
		}
	}
}

func TestSubmenuPointerHandoffRoutesKeysToHoveredLevel(t *testing.T) {
	chosen := ""
	tt := NewTester(func(c *Context) {
		popupTestTheme(c)
		ButtonBase(c).Label("source").Size(120, 28).ContextMenu(func(m *Menu) {
			m.Submenu("more", func(m *Menu) {
				if m.Item("child1").Chosen() {
					chosen = "child1"
				}
				if m.Item("child2").Chosen() {
					chosen = "child2"
				}
			})
			if m.Item("last").Chosen() {
				chosen = "last"
			}
		})
	}, 500, 300)
	tt.RightClick("source")
	r, _ := tt.Find("more")
	tt.Move(r.X+8, r.Y+10)
	tt.Advance(200 * time.Millisecond)
	child, ok := tt.Find("child1")
	if !ok {
		t.Fatal("submenu did not open")
	}
	tt.Move(child.X+8, child.Y+10)
	tt.Key(0, KeyDown)
	tt.Key(0, KeyEnter)
	if chosen != "child2" {
		t.Fatal("keyboard followed parent after pointer entered child", chosen)
	}
}

func TestMenuKeyboardNavigationDoesNotCommitPlainInputComposition(t *testing.T) {
	draft := ""
	tt := NewTester(func(c *Context) {
		popupTestTheme(c)
		Column(c).Children(func() {
			TextArea(c, &draft).Label("draft").Size(220, 60)
			ButtonBase(c).Label("object").Size(120, 28).ContextMenu(func(m *Menu) { m.Item("action") })
		})
	}, 500, 300)
	tt.Click("draft")
	tt.Compose("ni", 2)
	tt.RightClick("object")
	tt.Key(0, KeyDown)
	tt.Key(0, KeyEscape)
	tt.Compose("", 0)
	if draft != "" {
		t.Fatal("menu navigation committed composition", draft)
	}
}
