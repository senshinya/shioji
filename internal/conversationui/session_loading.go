package conversationui

import (
	"math"
	"time"

	n "github.com/egoist/mygo/ui"
)

const sessionCurveCycle = 6400 * time.Millisecond

// Rose Curve selected from Paidax01's Math Curve Loaders:
// https://paidax01.github.io/math-curve-loaders/ . Keep the source shape and
// relative motion rates; the approved trace cycle is 6.4 seconds.
func roseCurvePoint(progress, detail float64) (float32, float32) {
	t := progress * math.Pi * 2
	r := (9.2 + .6*detail) * (.72 + .28*detail) * math.Cos(5*t) * 3.25
	return float32(50 + math.Cos(t)*r), float32(50 + math.Sin(t)*r)
}

// PaintSessionLoadingCurve draws the shared Shioji conversation loader.
func PaintSessionLoadingCurve(p *n.Painter, box n.Rect, ink n.Color, elapsed time.Duration, reduced bool) {
	sourceTime := max(0, elapsed.Seconds()) * 5.4 / sessionCurveCycle.Seconds()
	progress := math.Mod(max(0, elapsed.Seconds())/sessionCurveCycle.Seconds(), 1)
	detail := .52 + (math.Sin(sourceTime/4.6*math.Pi*2+.55)+1)/2*.48
	rotation := float32(-math.Mod(sourceTime/28, 1) * 360)
	if reduced {
		detail, rotation = .76, 0
	}
	scale := box.W / 100
	p.Transform(n.Rotation(rotation), box.X+box.W/2, box.Y+box.H/2, func() {
		var path n.Path
		for i := 0; i <= 240; i++ {
			x, y := roseCurvePoint(float64(i)/240, detail)
			if i == 0 {
				path.MoveTo(box.X+x*scale, box.Y+y*scale)
			} else {
				path.LineTo(box.X+x*scale, box.Y+y*scale)
			}
		}
		track := float32(.1)
		if reduced {
			track = .45
		}
		p.StrokePath(&path, 4.5*scale, ink.Alpha(track))
		if !reduced {
			for i := 77; i >= 0; i-- {
				offset := float64(i) / 77
				x, y := roseCurvePoint(math.Mod(progress-offset*.32+1, 1), detail)
				fade := math.Pow(1-offset, .56)
				radius := float32(.9+fade*2.7) * scale
				p.Fill(n.Rect{X: box.X + x*scale - radius, Y: box.Y + y*scale - radius, W: radius * 2, H: radius * 2}, ink.Alpha(float32(.04+fade*.96)), radius)
			}
		}
	})
}
