package ui

import (
	"math"

	n "github.com/egoist/mygo/ui"
)

type previewImageView struct {
	zoom         float32
	quarterTurns int
}

func (v previewImageView) scale() float32 {
	if v.zoom == 0 {
		return 1
	}
	return v.zoom
}

func (v *previewImageView) scroll(delta float32) {
	v.zoom = min(8, max(.25, v.scale()*float32(math.Exp(float64(-delta)*.0025))))
}

// The thumbnail is cropped with Cover; the reader shows the complete image
// with Contain. Interpolate both the image and its clip, keeping one opaque
// image layer rather than crossfading two differently sized images.
func (v previewImageView) geometry(source, destination n.Rect, image n.ImageSource, progress float32) (n.Rect, n.Rect) {
	w, h := n.ImageSize(image)
	if w <= 0 || h <= 0 {
		return destination, destination
	}
	fit := func(box n.Rect, scale float32) n.Rect {
		return n.Rect{X: box.X + (box.W-w*scale)/2, Y: box.Y + (box.H-h*scale)/2, W: w * scale, H: h * scale}
	}
	fitW, fitH := w, h
	if v.quarterTurns%2 != 0 {
		fitW, fitH = h, w
	}
	start := fit(source, max(source.W/w, source.H/h))
	scale := min(destination.W/fitW, destination.H/fitH) * v.scale()
	end := fit(destination, scale)
	clipW, clipH := min(destination.W, fitW*scale), min(destination.H, fitH*scale)
	clip := n.Rect{X: destination.X + (destination.W-clipW)/2, Y: destination.Y + (destination.H-clipH)/2, W: clipW, H: clipH}
	between := func(a, b n.Rect) n.Rect {
		return n.Rect{X: a.X + (b.X-a.X)*progress, Y: a.Y + (b.Y-a.Y)*progress, W: a.W + (b.W-a.W)*progress, H: a.H + (b.H-a.H)*progress}
	}
	return between(source, clip), between(start, end)
}

func (v previewImageView) paint(p *n.Painter, image n.ImageSource, clip, draw n.Rect, radius, progress float32) {
	turns := v.quarterTurns
	if turns == 3 {
		turns = -1
	}
	angle := -90 * float32(turns) * progress
	p.Clip(clip, radius, func() {
		p.Transform(n.Rotation(angle), draw.X+draw.W/2, draw.Y+draw.H/2, func() {
			p.Image(image, draw, n.Contain)
		})
	})
}
