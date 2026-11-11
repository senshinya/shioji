package editor

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestDiffAnnotationIdentityDoesNotDependOnPrecedingHeight(t *testing.T) {
	d := DiffState{Old: NewBuffer(""), New: NewBuffer("line\n"), Layout: DiffUnified, Annotations: []DiffAnnotation{{Side: DiffNew, Line: 0, Height: 40, Key: "first"}, {Side: DiffNew, Line: 0, Height: 40, Key: "second"}}}
	created := 0
	d.RenderAnnotation = func(c *ui.Context, a DiffAnnotation) {
		box := ui.Column(c).Fill()
		if a.Key == "second" {
			ui.Local(box, "identity", func() int { created++; return created })
		}
	}
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, &d).Fill() }, 400, 240)
	if created != 1 {
		t.Fatal("initial identity", created)
	}
	d.Annotations[0].Height = 80
	tester.Frame()
	if created != 1 {
		t.Fatal("preceding height remounted the following annotation", created)
	}
}
