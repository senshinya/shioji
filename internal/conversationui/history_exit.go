package conversationui

import (
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
)

const (
	HistoryShatterDuration  = 1150 * time.Millisecond
	HistoryCollapseDuration = 200 * time.Millisecond
	HistoryForkFadeDuration = 220 * time.Millisecond
)

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

func (Shatter) EffectMargin() n.Point { return n.Point{X: 160, Y: 160} }
func (fx Shatter) PaintEffect(p *n.Painter, box n.Rect, content *n.Bitmap, inset n.Point) {
	w, h := content.Size()
	sx, sy := float32(w)/(box.W+320), float32(h)/(box.H+320)
	const grain = float32(2.4)
	for y, iy := float32(0), uint32(0); y < box.H; y, iy = y+grain, iy+1 {
		for x, ix := float32(0), uint32(0); x < box.W; x, ix = x+grain, ix+1 {
			hash := (ix*374761393 + iy*668265263) ^ fx.Seed
			hash = (hash ^ (hash >> 13)) * 1274126177
			u, v := float32(hash&65535)/65535, float32((hash>>16)&65535)/65535
			delay := .48*(x/box.W) + .12*u
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
			gw, gh := min(grain, box.W-x), min(grain, box.H-y)
			scale := float32(1)
			if k > 0 {
				scale = max(.30, 1-.65*k)
			}
			src := n.Rect{X: inset.X + x*sx, Y: inset.Y + y*sy, W: gw * sx, H: gh * sy}
			dst := n.Rect{X: box.X + x + dx + gw*(1-scale)/2, Y: box.Y + y + dy + gh*(1-scale)/2, W: gw * scale, H: gh * scale}
			p.ImageRegion(content, src, dst, n.ImageOptions{Tint: n.RGBA(255, 255, 255, alpha)})
		}
	}
}
