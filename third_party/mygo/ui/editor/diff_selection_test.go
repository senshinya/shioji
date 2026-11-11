package editor

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

func TestDiffPreciseSelectionCopiesOnlyExpression(t *testing.T) {
	d := DiffState{Old: NewBuffer("old\n"), New: NewBuffer("root.Right = next.Left\n"), Layout: DiffUnified}
	d.SetTextSelection(DiffTextPoint{Side: DiffNew, Offset: 13}, DiffTextPoint{Side: DiffNew, Offset: 22})
	s, ok := d.SelectedText()
	if !ok || s.Text != "next.Left" || len(s.Fragments) != 1 || s.Fragments[0].Start != 13 || s.Fragments[0].End != 22 {
		t.Fatalf("precise selection %#v %v", s, ok)
	}
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 500, 300)
	if !d.Run(Copy) || tester.Clipboard() != "next.Left" {
		t.Fatalf("copy expanded selection %q", tester.Clipboard())
	}
}
func TestDiffSelectionAcrossDeletedAndAddedRows(t *testing.T) {
	d := DiffState{Old: NewBuffer("same\nold text\nend\n"), New: NewBuffer("same\nnew text\nend\n"), Layout: DiffUnified}
	d.SetTextSelection(DiffTextPoint{Side: DiffNew, Offset: 8}, DiffTextPoint{Side: DiffOld, Offset: 9})
	s, ok := d.SelectedText()
	if !ok || s.Text != "text\nnew" || len(s.Fragments) != 2 {
		t.Fatalf("mixed reverse selection %#v", s)
	}
	if s.Fragments[0].Side != DiffOld || s.Fragments[0].StartLine != 2 || s.Fragments[1].Side != DiffNew {
		t.Fatalf("lost origins %#v", s)
	}
}
func TestDiffSelectionPreservesUTF8AndCRLF(t *testing.T) {
	d := DiffState{Old: NewBuffer(""), New: NewBuffer("中🙂\r\nnext.Left\r\n"), Layout: DiffUnified}
	d.SetTextSelection(DiffTextPoint{Side: DiffNew, Offset: 3}, DiffTextPoint{Side: DiffNew, Offset: 13})
	s, ok := d.SelectedText()
	if !ok || s.Text != "🙂\r\nnext" || s.Fragments[0].Start != 3 || s.Fragments[0].End != 13 {
		t.Fatalf("UTF-8 / CRLF selection %#v", s)
	}
	d.ClearTextSelection()
	if _, ok = d.SelectedText(); ok {
		t.Fatal("clear retained selection")
	}
}

func TestDiffSelectionNativePointerReleaseAndKeyboard(t *testing.T) {
	d := DiffState{Old: NewBuffer("old text\n"), New: NewBuffer("new text\n"), Layout: DiffUnified, TextSelection: true, Font: ui.Font{Family: "Menlo", Size: 12}}
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 500, 300)
	ed := &d.eds[1]
	x := ed.r.X + ed.textX
	// First visual line is the deletion. Its exact characters are selectable.
	y := ed.r.Y + ed.view.lineHeight/2
	tester.Press(x, y)
	tester.Move(x+3*ed.view.charWidth, y)
	s, ok := d.SelectedText()
	if !ok || !s.Dragging || s.Text != "old" {
		t.Fatalf("deleted drag %#v", s)
	}
	tester.Release(x+3*ed.view.charWidth, y)
	s, ok = d.SelectedText()
	if !ok || s.Dragging || s.Bounds.H <= 0 {
		t.Fatalf("release geometry %#v", s)
	}
	tester.Key(ui.Cmd, ui.KeyC)
	if tester.Clipboard() != "old" {
		t.Fatalf("native copy %q", tester.Clipboard())
	}
	tester.Key(ui.Shift, ui.KeyRight)
	s, _ = d.SelectedText()
	if s.Text != "old " {
		t.Fatalf("keyboard extension %#v", s)
	}
	d.New.Set("changed\n")
	if _, ok = d.SelectedText(); ok {
		t.Fatal("selection survived a changed source buffer")
	}
}

func TestDiffKeyboardRetainsGraphemesAndTraversesDeletedRows(t *testing.T) {
	d := DiffState{Old: NewBuffer("e\u0301 old\n"), New: NewBuffer("new\n"), Layout: DiffUnified, TextSelection: true}
	ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 500, 300)
	d.SetTextSelection(DiffTextPoint{DiffOld, 0}, DiffTextPoint{DiffOld, 0})
	d.textSelectionInput(&d.eds[1], ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyRight, Mods: ui.Shift})
	s, _ := d.SelectedText()
	if s.Text != "e\u0301" {
		t.Fatal("keyboard split a combining character", s.Text)
	}
	d.SetTextSelection(DiffTextPoint{DiffOld, 0}, DiffTextPoint{DiffOld, len("e\u0301 old")})
	d.textSelectionInput(&d.eds[1], ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyRight, Mods: ui.Shift})
	s, ok := d.SelectedText()
	if !ok || s.Text != "e\u0301 old\n" {
		t.Fatal("selection disappeared crossing deleted row", s)
	}
	d.textSelectionInput(&d.eds[1], ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyRight, Mods: ui.Shift})
	s, _ = d.SelectedText()
	if s.Text != "e\u0301 old\nn" {
		t.Fatal("selection did not enter next visible side", s)
	}
}
func TestDiffSelectAllIncludesCollapsedContext(t *testing.T) {
	common := strings.Repeat("same\n", 20)
	d := DiffState{Old: NewBuffer(common + "old\n" + common), New: NewBuffer(common + "new\n" + common), Layout: DiffUnified, TextSelection: true}
	ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 500, 300)
	d.textSelectionInput(&d.eds[1], ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyA, Mods: ui.Cmd})
	selection, ok := d.SelectedText()
	if !ok || !strings.HasPrefix(selection.Text, common) || !strings.Contains(selection.Text, "old\nnew\n") {
		t.Fatal("select all lost collapsed context", selection.Text)
	}
}
