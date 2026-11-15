package ui

import (
	"bytes"
	"fmt"
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

func TestObjectMenuPreservesComposerIMEUntilCommitOrCancel(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			m := layoutModel()
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.Click("消息输入")
			ui.Compose("ni", 2)
			row, _ := ui.Find(m.Snapshot.Sessions[0].Title)
			ui.RightClickAt(row.X+6, row.Y+row.H/2)
			if !ui.HasText("归档会话") {
				t.Fatal("menu did not open")
			}
			ui.Key(0, n.KeyDown)
			ui.Key(0, n.KeyEscape)
			if commit {
				ui.Type("你")
			} else {
				ui.Compose("", 0)
			}
			want := ""
			if commit {
				want = "你"
			}
			if m.Drafts[m.DraftKey()] != want {
				t.Fatal("object menu committed unfinished composition", m.Drafts[m.DraftKey()], want)
			}
			if !ui.Focused("消息输入") {
				t.Fatal("menu did not retain original input focus")
			}
		})
	}
}

func TestLinkMenuCopyPreservesAnotherInputsComposition(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items[0].Text = "[文档](docs/readme.md)"
	d.Items[0].Final = true
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1240, 820)
	ui.Click("消息输入")
	ui.Compose("ni", 2)
	r, _ := ui.Find("文档")
	ui.RightClickAt(r.X+3, r.Y+r.H/2)
	if err := ui.Click("复制链接"); err != nil {
		t.Fatal(err)
	}
	ui.Type("你")
	if m.Drafts[m.DraftKey()] != "你" || ui.Clipboard() != "docs/readme.md" {
		t.Fatal("copy link changed composition or clipboard", m.Drafts[m.DraftKey()], ui.Clipboard())
	}
}

func TestPinnedSessionOpenMenuShowsOnlyTheActionGlyph(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	pinned := false
	s := m.Snapshot.Sessions[0]
	ui := n.NewTester(func(c *n.Context) {
		t := applyAppearance(c, m.Preferences)
		n.Column(c).Width(320).Height(140).Padding(40, 20).Children(func() { v.navigationSessionRow(c, t, navigationRow{session: s, presence: 1, pinned: pinned}) })
	}, 700, 300)
	ui.Advance(time.Second)
	row, _ := ui.Find(s.Title)
	ui.RightClickAt(row.X+3, row.Y+1)
	menu, _ := ui.Find("置顶会话")
	ui.Move(menu.X+6, menu.Y+8)
	ui.Advance(time.Second)
	more, ok := ui.Find("会话菜单：" + s.Title)
	if !ok {
		t.Fatal("missing visible action")
	}
	before := contextPixels(ui, more)
	pinned = true
	ui.Frame()
	ui.Advance(time.Second)
	if !bytes.Equal(before, contextPixels(ui, more)) {
		t.Fatal("pin and ellipsis painted into the same menu-open slot")
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
