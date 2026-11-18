package ui

import (
	"bytes"
	"testing"
)

func TestWindowCaptureRefreshesReusedTransientMask(t *testing.T) {
	tt := NewTester(func(c *Context) {
		Box(c).Fill().Draw(func(p *Painter, r Rect) {
			var line Path
			line.MoveTo(10, 10)
			line.LineTo(35, 35)
			p.StrokePath(&line, 2, Hex("#f00000"))
		})
	}, 48, 48)
	h := windowHost{rt: tt.rt, last: &tt.rt.scene, lastMaskVersion: tt.rt.scene.MaskAtlas.Version()}
	if !tt.rt.transient || len(h.last.Glyphs) != 1 {
		t.Fatal("need a frame with one transient path mask")
	}
	_, _, want := h.capture()
	glyph := h.last.Glyphs[0]
	atlases := atlasGenerations(h.last)

	// Another window's first path of this size reuses the same transient shelf.
	tt.rt.text.BeginFrame()
	w, height := int(glyph.UW), int(glyph.VH)
	mask := tt.rt.text.Mask(0, func() (int, int, []byte) {
		return w, height, make([]byte, w*height)
	})
	if !mask.OK || mask.X != glyph.U || mask.Y != glyph.V || atlasGenerations(h.last) != atlases {
		t.Fatal("need a reused mask without an atlas generation change")
	}
	_, _, got := h.capture()
	if !bytes.Equal(got, want) {
		t.Fatal("capture drew another window's transient mask")
	}
}
