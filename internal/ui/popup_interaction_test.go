package ui

import (
	n "github.com/egoist/mygo/ui"
	"testing"
	"time"
)

// Catches a context menu escaping to the platform, losing the shared density,
// or changing the active conversation simply to act on another row.
func TestSessionContextMenuUsesSharedNativeSurface(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("消息输入")
	ui.Type("保留草稿")
	row, ok := ui.Find(m.Snapshot.Sessions[0].Title)
	if !ok {
		t.Fatal("session missing")
	}
	ui.RightClickAt(row.X+12, row.Y+row.H/2)
	item, ok := ui.Find("归档会话")
	if !ok {
		t.Fatal("context menu is not rendered by MyGo", ui.Menu())
	}
	if item.H != 26 {
		t.Fatal("menu row density", item)
	}
	if err := ui.Click("重命名"); err == nil && v.rename != nil {
		t.Fatal("disabled remote mutation executed")
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(time.Second)
	if ui.HasText("归档会话") || m.Drafts[m.DraftKey()] != "保留草稿" {
		t.Fatal("close changed draft or left menu open")
	}
}

// Catches auto editing menus retaining a separate renderer or losing the
// input's selected contents when the menu temporarily owns keyboard focus.
func TestComposerEditingContextMenuKeepsSelection(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("消息输入")
	ui.Type("selected 中文 draft")
	ui.Key(n.Cmd, n.KeyA)
	input, _ := ui.Find("消息输入")
	ui.RightClickAt(input.X+12, input.Y+input.H/2)
	if !ui.HasText("Copy") && !ui.HasText("复制") {
		t.Fatal("editing menu did not use the shared renderer", ui.Menu())
	}
	if err := ui.ChooseMenuItem("Copy"); err != nil {
		t.Fatal(err)
	}
	if ui.Clipboard() != "selected 中文 draft" {
		t.Fatal("selection lost", ui.Clipboard())
	}
}
