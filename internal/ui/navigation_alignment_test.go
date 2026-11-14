package ui

import (
	"fmt"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

// An empty trailing action slot must not supply the row's text baseline.
// Check actual native ink, including hover/pin changes and Retina rendering.
func TestSessionNavigationInkAlignment(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, title := range []string{"HHH", "国国国", "国 H 国"} {
				for _, status := range []string{"running", "completed"} {
					t.Run(fmt.Sprintf("dark=%v/scale=%g/%s/%s", dark, scale, title, status), func(t *testing.T) {
						m := navigationFixture()
						v := New(m, nil, "")
						s := m.Snapshot.Sessions[0]
						s.Title, s.Status = title, domain.ExecutionState(status)
						r := navigationRow{session: s, workspace: m.Snapshot.Workspaces[0], presence: 1}
						ui := n.NewTester(func(c *n.Context) {
							n.Column(c).Fill().Padding(20).Background(c.Theme().Background).Children(func() {
								v.navigationSessionRow(c, c.Theme(), r)
							})
						}, 300, 90)
						ui.SetDark(dark)
						ui.SetScale(scale)
						ui.SetPreferences(n.Preferences{ReduceMotion: true})
						ui.Advance(time.Second)
						row, ok := ui.Find(title)
						if !ok {
							t.Fatal("missing session row")
						}
						inkCenter := func(box n.Rect) float64 {
							t.Helper()
							im := ui.Image()
							bg := im.RGBAAt(int((row.X+2)*scale), int((row.Y+row.H/2)*scale))
							lo, hi := im.Bounds().Max.Y, -1
							for y := int(math.Floor(float64(box.Y * scale))); y < int(math.Ceil(float64((box.Y+box.H)*scale))); y++ {
								for x := int(box.X * scale); x < int((box.X+box.W)*scale); x++ {
									p := im.RGBAAt(x, y)
									if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
										lo, hi = min(lo, y), max(hi, y)
									}
								}
							}
							if hi < 0 {
								t.Fatal("missing native ink", box)
							}
							return float64(lo+hi+1) / (2 * float64(scale))
						}
						var previous float64
						for _, pinned := range []bool{false, true} {
							r.pinned = pinned
							for _, hover := range []bool{false, true, false} {
								if hover {
									ui.Move(row.X+row.W-20, row.Y+row.H/2)
								} else {
									ui.Move(0, 0)
								}
								ui.Frame()
								ui.Advance(time.Second)
								name := fmt.Sprintf("dark-%v-scale-%g-%s-%s-pin-%v-hover-%v.png", dark, scale, title, status, pinned, hover)
								saveNativeSkillImage(t, ui, name)
								text := inkCenter(n.Rect{X: row.X + 36, Y: row.Y, W: 65, H: row.H})
								marker := inkCenter(n.Rect{X: row.X + 20, Y: row.Y, W: 10, H: row.H})
								if delta := text - float64(row.Y+row.H/2); math.Abs(delta) > 2 {
									t.Errorf("title sinks within the session row: delta=%g", delta)
								}
								if delta := marker - text; math.Abs(delta) > 1 {
									t.Errorf("status is offset from title: delta=%g", delta)
								}
								if hover || pinned {
									action := inkCenter(n.Rect{X: row.X + row.W - 26, Y: row.Y + 3, W: 16, H: row.H - 6})
									if delta := action - text; math.Abs(delta) > 1 {
										t.Errorf("trailing action is offset from title: delta=%g", delta)
									}
								}
								if previous != 0 && math.Abs(text-previous) > .5 {
									t.Errorf("hover or pin moves title: before=%g after=%g", previous, text)
								}
								previous = text
							}
						}
					})
				}
			}
		}
	}
}
