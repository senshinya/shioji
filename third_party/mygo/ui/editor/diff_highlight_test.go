package editor

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestUnifiedTextFragmentsProjectUnchangedBaseTextAndKeepDeletions(t *testing.T) {
	old := "prefix\r\nsame 国\r\nremoved value\r\nend\r\n"
	newText := "prefix\nsame 国\ninserted value\nend\n"
	d := DiffState{Old: NewBuffer(old), New: NewBuffer(newText), Layout: DiffUnified}
	part := DiffTextFragment{Side: DiffOld, Start: strings.Index(old, "国"), End: strings.Index(old, " value")}
	got := d.UnifiedTextFragments(part)
	if len(got) != 2 {
		t.Fatalf("expected one unchanged and one deleted fragment: %+v", got)
	}
	if got[0].Side != DiffNew || newText[got[0].Start:got[0].End] != "国\n" {
		t.Fatalf("unchanged quote did not map to the new side: %+v", got[0])
	}
	if got[1].Side != DiffOld || old[got[1].Start:got[1].End] != "removed" {
		t.Fatalf("deleted quote moved off the base side: %+v", got[1])
	}
}

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
