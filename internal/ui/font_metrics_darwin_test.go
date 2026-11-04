//go:build darwin

package ui

import (
	n "github.com/egoist/mygo/ui"
	"testing"
)

// Logical line boxes include leading and cannot align an icon with visible ink.
func TestNativeInkBoundsDistinguishCapsDescendersAndCJK(t *testing.T) {
	font, ok := any(n.Font{Family: "system-ui", Size: 12}).(interface{ InkBounds(string) n.Rect })
	if !ok {
		t.Fatal("native font does not expose visible glyph bounds for inline alignment")
	}
	cap, descender, han := font.InkBounds("H"), font.InkBounds("Hg"), font.InkBounds("国")
	for name, r := range map[string]n.Rect{"capital": cap, "descender": descender, "CJK fallback": han} {
		if r.W <= 0 || r.H <= 0 || r.Y >= 0 {
			t.Fatalf("%s has no baseline-relative ink bounds: %+v", name, r)
		}
	}
	if descender.Y+descender.H <= cap.Y+cap.H {
		t.Fatal("descender was measured as a logical line box")
	}
	if r := font.InkBounds(" "); r.W != 0 || r.H != 0 {
		t.Fatalf("whitespace has visible ink: %+v", r)
	}
}
