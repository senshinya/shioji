package editor

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestDiffContentHeightIncludesHeadersAndVisualRows(t *testing.T) {
	d := DiffState{Layout: DiffUnified, Wrap: true, Font: ui.Font{Family: "Menlo", Size: 11}, LineHeight: 20.0 / 11}
	if d.ContentHeight() != 0 {
		t.Fatal("unlaid-out diff has a content height")
	}
	files, err := ParsePatch("@@ -1 +1 @@\n-old\n+new")
	if err != nil {
		t.Fatal(err)
	}
	d.SetPatch(files[0])
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 500, 300)
	tester.Advance(time.Second)
	if got := d.ContentHeight(); got != 72 {
		t.Fatalf("one header + deletion + insertion: got %g, want 72", got)
	}
	d.New.Set("a long changed line that wraps several times in a narrow viewport")
	tester.SetSize(180, 300)
	tester.Frame()
	if got := d.ContentHeight(); got <= 72 {
		t.Fatalf("soft wraps did not increase visual height: %g", got)
	}
	tester.SetSize(1000, 300)
	tester.Frame()
	if got := d.ContentHeight(); got != 72 {
		t.Fatalf("widening retained stale wrapped rows: %g", got)
	}
}
