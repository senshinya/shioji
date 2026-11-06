package ui

import (
	"fmt"
	"testing"
)

func TestVirtualTextSelectionSurvivesUnmountAndCopiesWithoutMountingHistory(t *testing.T) {
	items := make([]string, 200)
	for i := range items {
		items[i] = fmt.Sprintf("message %03d selected text", i)
	}
	var list ListState
	list.Key = func(i int) any { return fmt.Sprint(i) }
	built := 0
	draft := "ordinary draft"
	tt := NewTester(func(c *Context) {
		built = 0
		Column(c).Fill().Children(func() {
			element := List(c, &list, len(items), func(i int) {
				built++
				row := Column(c).FillWidth().Height(40).Selectable().Children(func() { Text(c, items[i]) })
				SelectionItem(row, fmt.Sprint(i))
			}).Height(320).FillWidth()
			VirtualTextSelection(element, func(key string) (string, bool) {
				for i, text := range items {
					if key == fmt.Sprint(i) {
						return text, true
					}
				}
				return "", false
			})
			TextInput(c, &draft).Label("draft").Height(32)
		})
	}, 400, 400)
	tt.Click(items[0])
	tt.Key(Cmd, KeyA)
	tt.Key(Cmd, KeyC)
	if tt.Clipboard() != items[0] {
		t.Fatal("initial selection failed", tt.Clipboard())
	}
	list.ScrollToEnd()
	tt.Frame()
	if built >= 30 || tt.HasText(items[0]) {
		t.Fatal("history stayed mounted", built)
	}
	tt.SetClipboard("sentinel")
	tt.Key(Cmd, KeyC)
	if tt.Clipboard() != items[0] {
		t.Fatal("unmounted selection was lost", tt.Clipboard())
	}
	list.ScrollTo(0, Start)
	tt.Frame()
	tt.Frame()
	tt.SetClipboard("sentinel")
	tt.Command("copy")
	if tt.Clipboard() != items[0] {
		t.Fatal("selection did not restore after remount", tt.Clipboard())
	}
	list.ScrollToEnd()
	tt.Frame()
	tt.Click("draft")
	tt.Key(Cmd, KeyA)
	tt.Key(Cmd, KeyC)
	if tt.Clipboard() != draft {
		t.Fatal("retained history selection stole composer copy")
	}
}

func TestVirtualTextSelectionDoesNotReuseChangedMessage(t *testing.T) {
	source := "original message"
	var list ListState
	list.Key = func(i int) any { return i }
	tt := NewTester(func(c *Context) {
		el := List(c, &list, 100, func(i int) {
			text := fmt.Sprint(i)
			if i == 0 {
				text = source
			}
			row := Column(c).Height(40).Selectable().Children(func() { Text(c, text) })
			SelectionItem(row, fmt.Sprint(i))
		}).Fill()
		VirtualTextSelection(el, func(key string) (string, bool) { return source, key == "0" })
	}, 400, 300)
	tt.Click(source)
	tt.Key(Cmd, KeyA)
	list.ScrollToEnd()
	tt.Frame()
	source = "replacement"
	tt.Frame()
	tt.SetClipboard("sentinel")
	tt.Key(Cmd, KeyC)
	if tt.Clipboard() != "sentinel" {
		t.Fatal("stale selection copied changed content")
	}
}

func TestRetainedSelectionDoesNotUnmountAnotherFocusedEditor(t *testing.T) {
	var list ListState
	draft := "edit me"
	tt := NewTester(func(c *Context) {
		el := List(c, &list, 100, func(i int) {
			row := Column(c).Height(40).Selectable().Children(func() {
				if i == 1 {
					TextInput(c, &draft).Label("row editor")
				} else {
					Text(c, fmt.Sprint(i))
				}
			})
			SelectionItem(row, fmt.Sprint(i))
		}).Fill()
		VirtualTextSelection(el, func(key string) (string, bool) { return key, true })
	}, 400, 300)
	tt.Click("0")
	tt.Key(Cmd, KeyA)
	tt.Click("row editor")
	list.ScrollToEnd()
	tt.Frame()
	tt.Key(Cmd, KeyA)
	tt.Type("preserved editor")
	if draft != "preserved editor" {
		t.Fatal("virtual selection stole another editor's focus", draft)
	}
}
