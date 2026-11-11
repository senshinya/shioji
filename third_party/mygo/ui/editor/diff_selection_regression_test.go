package editor

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyDiffDoesNotBlinkAnInsertionCaret(t *testing.T) {
	d := DiffState{Old: NewBuffer("old\n"), New: NewBuffer("new\n"), Layout: DiffUnified, TextSelection: true}
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 300, 160)
	tester.ClickAt(90, 35)
	tester.Advance(2 * time.Second)
	first := bytes.Clone(tester.Image().Pix)
	tester.Advance(blinkInterval)
	if !bytes.Equal(first, tester.Image().Pix) {
		t.Fatal("focused read-only Diff paints a blinking insertion caret")
	}
}

func TestPreciseDiffMixedLineEndingsPreserveOriginalSelectionBytes(t *testing.T) {
	original := "a\nb\r\nnext.Left\n"
	d := DiffState{Old: NewBuffer(""), New: NewBuffer(original), Layout: DiffUnified}
	// A pointer over the third visual line produces normalized buffer bytes.
	normalized := d.New.String()
	start := strings.Index(normalized, "next.Left")
	d.setTextSelection(DiffTextPoint{DiffNew, start}, DiffTextPoint{DiffNew, start + len("next.Left")})
	selected, ok := d.SelectedText()
	if !ok {
		t.Fatal("missing selection")
	}
	f := selected.Fragments[0]
	if f.End > len(original) || original[f.Start:f.End] != f.Text {
		t.Fatalf("copied %q but offsets [%d,%d) select %q in immutable source", f.Text, f.Start, f.End, original[f.Start:min(f.End, len(original))])
	}
}

func TestExactSourcesRoundTripBothSidesAndUpdates(t *testing.T) {
	for _, side := range []DiffSide{DiffOld, DiffNew} {
		for _, raw := range []string{"a\nb\r\n国😀\n", "a\r\nb\n国😀\r\n"} {
			d := DiffState{Old: NewBuffer(""), New: NewBuffer(""), Layout: DiffUnified, ExpandUnchanged: true}
			if side == DiffNew {
				d.New.Set(raw)
			} else {
				d.Old.Reset(raw)
			}
			start := strings.Index(raw, "国")
			d.SetTextSelection(DiffTextPoint{side, start}, DiffTextPoint{side, len(raw)})
			selected, ok := d.SelectedText()
			if !ok || len(selected.Fragments) != 1 {
				t.Fatalf("selection absent: %#v", selected)
			}
			f := selected.Fragments[0]
			if f.Start != start || f.End != len(raw) || f.Text != raw[start:] {
				t.Fatalf("source round trip changed: %#v, raw %q", f, raw)
			}
		}
	}
}

func TestPreciseDiffRestoringFragmentEndpointsDoesNotExpandCollapsedGap(t *testing.T) {
	common := strings.Repeat("hidden context\n", 30)
	d := DiffState{Old: NewBuffer("old first\n" + common + "old last\n"), New: NewBuffer("new first\n" + common + "new last\n"), Layout: DiffUnified, TextSelection: true}
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 500, 300)
	d.SetTextSelection(DiffTextPoint{DiffNew, 0}, DiffTextPoint{DiffNew, d.New.Len() - 1})
	selected, ok := d.SelectedText()
	if !ok {
		t.Fatal("missing original selection")
	}
	if len(selected.Fragments) < 3 {
		t.Fatalf("setup needs a collapsed gap: %#v", selected.Fragments)
	}
	first, last := selected.Fragments[0], selected.Fragments[len(selected.Fragments)-1]
	// Exact restoration algorithm used by locateDiffComment.
	d.ExpandAll()
	tester.Frame()
	_ = first
	_ = last
	d.SetTextSelectionFragments(selected.Fragments)
	restored, _ := d.SelectedText()
	if restored.Text != selected.Text {
		t.Fatalf("restoration expanded selected text from %d to %d bytes (%d to %d fragments)", len(selected.Text), len(restored.Text), len(selected.Fragments), len(restored.Fragments))
	}
}
func TestPreciseDiffShiftDownMovesOneWrappedRow(t *testing.T) {
	d := DiffState{Old: NewBuffer("old"), New: NewBuffer(strings.Repeat("word ", 40)), Layout: DiffUnified, TextSelection: true, Wrap: true, Font: ui.Font{Family: "Menlo", Size: 12}}
	ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 160, 300)
	d.SetTextSelection(DiffTextPoint{DiffNew, 0}, DiffTextPoint{DiffNew, 0})
	d.textSelectionInput(&d.eds[1], ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyDown, Mods: ui.Shift})
	selection, ok := d.SelectedText()
	if !ok || selection.Text == "" {
		t.Fatal("Shift+Down leaves caret at beginning of wrapped line; no visual row selected")
	}
}
