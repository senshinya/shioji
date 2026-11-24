package conversationui

import (
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
)

const (
	HistoryShatterDuration   = 1150 * time.Millisecond
	HistoryCollapseDuration  = 200 * time.Millisecond
	HistoryForkFadeDuration  = 220 * time.Millisecond
	HistoryShatterMaxLayerPx = 8192

	shatterBaseGrain = float32(2.4)
	shatterMaxGrains = 16000
)

// HistoryShatterFits reports whether an element of size w×h DIPs at pixel scale
// fits within MyGo's GPU offscreen effect texture ceiling. Callers should fall
// back to a smooth opacity fade when false rather than letting the effect drop.
func HistoryShatterFits(w, h, scale float32) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	if scale <= 0 {
		scale = 1
	}
	// MyGo's effect layer rounds endpoints via floor/ceil, which can add 1 px.
	return int(math.Ceil(float64(w*scale)))+1 <= HistoryShatterMaxLayerPx &&
		int(math.Ceil(float64(h*scale)))+1 <= HistoryShatterMaxLayerPx
}

func shatterGrainSize(w, h float32) float32 {
	area := w * h
	if area <= 0 {
		return shatterBaseGrain
	}
	return max(shatterBaseGrain, float32(math.Sqrt(float64(area)/shatterMaxGrains)))
}

type HistoryExitFrame struct {
	Opacity, Height, Shatter float32
	Dispatch                 bool
}

func HistoryExitAt(now, start time.Time, restart, reduced bool) HistoryExitFrame {
	elapsed := max(0, now.Sub(start))
	if !restart {
		progress := min(1, float32(elapsed)/float32(HistoryForkFadeDuration))
		if reduced {
			progress = 1
		}
		return HistoryExitFrame{Opacity: 1 - n.EaseOut(progress), Height: 1, Dispatch: progress >= 1}
	}
	if reduced {
		return HistoryExitFrame{Dispatch: true}
	}
	if elapsed < HistoryShatterDuration {
		return HistoryExitFrame{Opacity: 1, Height: 1, Shatter: float32(elapsed) / float32(HistoryShatterDuration)}
	}
	progress := min(1, float32(elapsed-HistoryShatterDuration)/float32(HistoryCollapseDuration))
	return HistoryExitFrame{Height: 1 - historyCollapseEase(progress), Dispatch: progress >= 1}
}

var historyCollapseEase = n.CubicBezier(.4, 0, .2, 1)

func HistoryShatter(progress float32, key string) n.Effect {
	seed := uint32(2166136261)
	for i := range len(key) {
		seed = (seed ^ uint32(key[i])) * 16777619
	}
	return Shatter{Progress: progress, Seed: seed}
}

// Erode the actual native content into fine grains, carried to the right.
// Sampling stays on the GPU; there is no screenshot readback or whole-row blur.
type Shatter struct {
	Progress float32
	Seed     uint32
}

// EffectMargin returns zero because Shatter only samples within the element's
// own box; particles paint freely onto the parent painter without inflating the
// offscreen texture or eating into the 8192 px GPU layer ceiling.
func (Shatter) EffectMargin() n.Point { return n.Point{} }

func (fx Shatter) PaintEffect(p *n.Painter, box n.Rect, content *n.Bitmap, inset n.Point) {
	if box.W <= 0 || box.H <= 0 || fx.Progress >= 1 || content == nil {
		return
	}
	w, h := content.Size()
	if w <= 0 || h <= 0 {
		return
	}
	sx := p.Scale()
	if sx <= 0 {
		sx = float32(w) / box.W
	}
	sy := sx
	clampSrc := func(x, y, rw, rh float32) n.Rect {
		srcX := max(float32(0), inset.X+x*sx)
		srcY := max(float32(0), inset.Y+y*sy)
		srcW := min(rw*sx, float32(w)-srcX)
		srcH := min(rh*sy, float32(h)-srcY)
		return n.Rect{X: srcX, Y: srcY, W: max(0, srcW), H: max(0, srcH)}
	}
	opaque := n.ImageOptions{Tint: n.RGBA(255, 255, 255, 1)}
	// Map the intact canvas 1:1 onto the window's pixel grid so subpixel
	// element heights do not get stretched by ImageRegion's dst snapping.
	layerX, layerY := box.X-inset.X/sx, box.Y-inset.Y/sy
	layerW, layerH := float32(w)/sx, float32(h)/sy
	if fx.Progress <= 0 {
		p.ImageRegion(content, n.Rect{W: float32(w), H: float32(h)}, n.Rect{X: layerX, Y: layerY, W: layerW, H: layerH}, opaque)
		return
	}

	grain := shatterGrainSize(box.W, box.H)
	// Columns strictly left of xStart have age >= 1 for every u <= 1.
	// Columns at or right of xStatic have age <= 0 for every u >= 0, so the
	// entire un-eroded right portion draws in one pixel-exact ImageRegion call.
	ixStart := uint32(0)
	if fx.Progress > .52 {
		ixStart = uint32(max(0, math.Floor(float64(box.W*(fx.Progress-.52)/.48/grain))))
	}
	ixStatic := uint32(math.Ceil(float64(box.W*(fx.Progress/.48)/grain)))
	xStatic := min(box.W, float32(ixStatic)*grain)
	activeEnd := xStatic
	if xStatic < box.W {
		xPx := max(float32(0), min(float32(w), float32(math.Round(float64(inset.X+xStatic*sx)))))
		if xPx < float32(w) {
			src := n.Rect{X: xPx, Y: 0, W: float32(w) - xPx, H: float32(h)}
			dst := n.Rect{X: layerX + xPx/sx, Y: layerY, W: (float32(w) - xPx) / sx, H: layerH}
			p.ImageRegion(content, src, dst, opaque)
		}
		activeEnd = max(0, (xPx-inset.X)/sx)
	}

	invW := 1 / box.W
	for y, iy := float32(0), uint32(0); y < box.H; y, iy = y+grain, iy+1 {
		gh := min(grain, box.H-y)
		for ix := ixStart; ; ix++ {
			x := float32(ix) * grain
			if x >= activeEnd {
				break
			}
			hash := (ix*374761393 + iy*668265263) ^ fx.Seed
			hash = (hash ^ (hash >> 13)) * 1274126177
			u, v := float32(hash&65535)/65535, float32((hash>>16)&65535)/65535
			delay := .48*(x*invW) + .12*u
			age := (fx.Progress - delay) / .40
			if age >= 1 {
				continue
			}
			k := max(float32(0), age)
			alpha := float32(1)
			if k > .58 {
				q := (k - .58) / .42
				alpha = 1 - q*q*(3-2*q)
			}
			dx, dy := float32(0), float32(0)
			if k > 0 {
				curl := float32(math.Sin(float64(k*8 + u*6.28)))
				dx = (58+88*u)*k + curl*8*k
				dy = (v-.64)*46*k - 18*k*k
			}
			gw := min(grain, activeEnd-x)
			scale := float32(1)
			if k > 0 {
				scale = max(.30, 1-.65*k)
			}
			src := clampSrc(x, y, gw, gh)
			dst := n.Rect{X: box.X + x + dx + gw*(1-scale)/2, Y: box.Y + y + dy + gh*(1-scale)/2, W: gw * scale, H: gh * scale}
			p.ImageRegion(content, src, dst, n.ImageOptions{Tint: n.RGBA(255, 255, 255, alpha)})
		}
	}
}
