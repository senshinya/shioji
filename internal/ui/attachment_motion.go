package ui

import n "github.com/egoist/mygo/ui"

// The thumbnail is cropped with Cover; the reader shows the complete image
// with Contain. Interpolate both the image and its clip, keeping one opaque
// image layer rather than crossfading two differently sized images.
func previewImageGeometry(source, destination n.Rect, image n.ImageSource, progress float32) (n.Rect, n.Rect) {
	w, h := n.ImageSize(image)
	if w <= 0 || h <= 0 {
		return destination, destination
	}
	fit := func(box n.Rect, scale float32) n.Rect {
		return n.Rect{X: box.X + (box.W-w*scale)/2, Y: box.Y + (box.H-h*scale)/2, W: w * scale, H: h * scale}
	}
	start := fit(source, max(source.W/w, source.H/h))
	end := fit(destination, min(destination.W/w, destination.H/h))
	between := func(a, b n.Rect) n.Rect {
		return n.Rect{X: a.X + (b.X-a.X)*progress, Y: a.Y + (b.Y-a.Y)*progress, W: a.W + (b.W-a.W)*progress, H: a.H + (b.H-a.H)*progress}
	}
	return between(source, end), between(start, end)
}
