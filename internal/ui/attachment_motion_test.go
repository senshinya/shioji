package ui

import (
	n "github.com/egoist/mygo/ui"
	"image"
	"testing"
)

func TestImageMorphRevealsTheThumbnailCropWithoutStretchingTheImage(t *testing.T) {
	source := n.Rect{X: 10, Y: 20, W: 40, H: 40}
	destination := n.Rect{X: 100, Y: 200, W: 400, H: 300}
	bitmap := n.NewBitmap(image.NewRGBA(image.Rect(0, 0, 200, 100)))
	for _, test := range []struct {
		progress   float32
		clip, draw n.Rect
	}{
		{0, n.Rect{X: 10, Y: 20, W: 40, H: 40}, n.Rect{X: -10, Y: 20, W: 80, H: 40}},
		{.5, n.Rect{X: 55, Y: 135, W: 220, H: 120}, n.Rect{X: 45, Y: 135, W: 240, H: 120}},
		{1, n.Rect{X: 100, Y: 250, W: 400, H: 200}, n.Rect{X: 100, Y: 250, W: 400, H: 200}},
	} {
		clip, draw := (previewImageView{}).geometry(source, destination, bitmap, test.progress)
		if clip != test.clip || draw != test.draw {
			t.Fatal("image crop morph is incorrect", test.progress, clip, draw)
		}
	}
}
