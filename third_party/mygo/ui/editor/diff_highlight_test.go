package editor

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestDiffPersistentHighlightsPaintBothSidesWithoutChangingSelection(t *testing.T) {
	d := DiffState{Old: NewBuffer("old 国😀\r\n"), New: NewBuffer("new 国😀\r\n"), Layout: DiffUnified, TextSelection: true}
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 400, 200)
	baseline := bytes.Clone(tester.Image().Pix)
	d.TextHighlights = []DiffTextHighlight{{Side: DiffOld, Start: 4, End: len("old 国😀"), Color: ui.RGBA(233, 180, 38, .28)}, {Side: DiffNew, Start: 4, End: len("new 国😀"), Color: ui.RGBA(233, 180, 38, .28)}}
	tester.Frame()
	if bytes.Equal(baseline, tester.Image().Pix) {
		t.Fatal("persistent highlights did not paint")
	}
	if _, ok := d.SelectedText(); ok {
		t.Fatal("highlight created a user selection")
	}
	d.SetTextSelection(DiffTextPoint{DiffNew, 0}, DiffTextPoint{DiffNew, 3})
	tester.Frame()
	selected, ok := d.SelectedText()
	if !ok || selected.Text != "new" {
		t.Fatal("highlight replaced selection", selected)
	}
	d.ClearTextSelection()
	d.TextHighlights = nil
	tester.Frame()
	if !bytes.Equal(baseline, tester.Image().Pix) {
		t.Fatal("removed highlights left painted content")
	}
}
