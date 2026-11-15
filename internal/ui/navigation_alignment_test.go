package ui

import (
	"fmt"
	"image"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestWorkspaceHeadingSharesTheWorkspaceRowVisibleEdges(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, width := range []int{940, 1280} {
				for _, device := range []string{"MacMini at Home", "这台 Mac"} {
					t.Run(fmt.Sprintf("dark=%v/scale=%g/width=%d/device=%s", dark, scale, width, device), func(t *testing.T) {
						m := navigationFixture()
						m.LocalDeviceName = device
						m.Snapshot.Workspaces[0].Name = "shioji"
						v := New(m, nil, "")
						ui := n.NewTester(v.View, width, 600)
						ui.SetDark(dark)
						ui.SetScale(scale)
						ui.Advance(time.Second)
						caption, _ := ui.Find("工作区")
						workspace, _ := ui.Find("shioji")
						deviceBox, _ := ui.Find(device)
						add, _ := ui.Find("创建工作区")
						list, _ := ui.Find("工作区与会话")
						im := ui.Image()
						bg := im.RGBAAt(int((caption.X+caption.W/2)*scale), int((caption.Y+caption.H/2)*scale))
						ink := func(r n.Rect) image.Rectangle {
							t.Helper()
							left, top, right, bottom := im.Bounds().Max.X, im.Bounds().Max.Y, -1, -1
							for y := int(r.Y * scale); y < int((r.Y+r.H)*scale); y++ {
								for x := int(r.X * scale); x < int((r.X+r.W)*scale); x++ {
									p := im.RGBAAt(x, y)
									if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
										left, top, right, bottom = min(left, x), min(top, y), max(right, x), max(bottom, y)
									}
								}
							}
							if right < left {
								t.Fatal("missing native ink", r)
							}
							return image.Rect(left, top, right+1, bottom+1)
						}
						labelInk := ink(caption)
						arrowInk := ink(n.Rect{X: workspace.X, Y: workspace.Y, W: 20, H: workspace.H})
						plusInk, deviceInk := ink(add), ink(deviceBox)
						saveNativeSkillImage(t, ui, fmt.Sprintf("workspace-edges-dark-%v-scale-%g-width-%d-%s.png", dark, scale, width, device))
						if delta := float64(labelInk.Min.X-arrowInk.Min.X) / float64(scale); math.Abs(delta) > 1 {
							t.Errorf("heading and disclosure have different visible left edges: %g DIP", delta)
						}
						if delta := float64(plusInk.Max.X-deviceInk.Max.X) / float64(scale); math.Abs(delta) > 1 {
							t.Errorf("plus and device have different visible right edges: %g DIP", delta)
						}
						// The list is inset by the panel's 8 DIP padding and 1 DIP border.
						left := float64(labelInk.Min.X)/float64(scale) - float64(list.X-9)
						right := float64(list.X+list.W+9) - float64(plusInk.Max.X)/float64(scale)
						if math.Abs(left-right) > 1 {
							t.Errorf("heading has unequal visible sidebar margins: left=%g right=%g", left, right)
						}
					})
				}
			}
		}
	}
}

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
