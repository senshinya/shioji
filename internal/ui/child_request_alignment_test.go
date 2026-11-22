package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

// Check visible pixels rather than equal SVG viewBoxes or layout box centers.
func TestChildDetailStatusIconAlignsToItsCaptionInk(t *testing.T) {
	cases := []struct{ state, kind, label string }{{"running", "questions", "等待回答"}, {"running", "approval", "等待授权"}, {"running", "", "进行中"}, {"completed", "", "已完成"}, {"unknown", "", "状态未知"}}
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("dark=%v/scale=%g/%s", dark, scale, tc.label), func(t *testing.T) {
					v, ui := childRequestsFixture(t)
					ui.SetDark(dark)
					ui.SetScale(scale)
					ui.SetPreferences(n.Preferences{ReduceMotion: true})
					d := v.Model.Details["s"]
					d.Works[0].State = tc.state
					if tc.kind == "" {
						d.Requests = nil
					} else {
						d.Requests[0].Kind = tc.kind
					}
					v.Model.Details["s"] = d
					ui.Frame()
					ui.Advance(time.Second)
					if err := ui.Click("查看子 Agent：检查边界条件"); err != nil {
						t.Fatal(err)
					}
					ui.Advance(time.Second)
					caption, ok := ui.Find(tc.label)
					if !ok {
						t.Fatal("missing status caption", ui.Texts())
					}
					icon := n.Rect{X: caption.X - 22, Y: caption.Y, W: 16, H: 20}
					center := func(box n.Rect) float64 {
						im := ui.Image()
						bg := im.RGBAAt(int((caption.X-24)*scale), int((caption.Y+1)*scale))
						top, bottom := im.Bounds().Max.Y, -1
						for y := int(math.Floor(float64(box.Y * scale))); y < int(math.Ceil(float64((box.Y+box.H)*scale))); y++ {
							for x := int(math.Floor(float64(box.X * scale))); x < int(math.Ceil(float64((box.X+box.W)*scale))); x++ {
								p := im.RGBAAt(x, y)
								delta := max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B))
								if delta > 30 {
									top = min(top, y)
									bottom = max(bottom, y)
								}
							}
						}
						if bottom < 0 {
							t.Fatal("missing native status ink", box)
						}
						return float64(top+bottom) / (2 * float64(scale))
					}
					if delta := center(icon) - center(caption); math.Abs(delta) > 1 {
						t.Fatal("status glyph is offset from its caption", delta, icon, caption)
					}
				})
			}
		}
	}
}
func absChannel(a, b uint8) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}

func TestChildWorkListMarkerAlignsToFirstLineInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, name := range []string{"HHH", "国国国", "国 H 国", "国\n第二行"} {
			t.Run(fmt.Sprintf("dark=%v/%s", dark, name), func(t *testing.T) {
				v, ui := childRequestsFixture(t)
				ui.SetDark(dark)
				ui.SetPreferences(n.Preferences{ReduceMotion: true})
				d := v.Model.Details["s"]
				d.Works[0].Title = name
				v.Model.Details["s"] = d
				ui.Frame()
				ui.Advance(time.Second)
				row, ok := ui.Find("查看子 Agent：" + name)
				if !ok {
					t.Fatal("missing work row")
				}
				// The marker and title share the first 20px reading line of this two-line row.
				im := ui.Image()
				bg := im.RGBAAt(int(row.X+1), int(row.Y+1))
				center := func(x, w float32) float64 {
					top, bottom := im.Bounds().Max.Y, -1
					for y := int(row.Y + 8); y < int(row.Y+28); y++ {
						for xx := int(x); xx < int(x+w); xx++ {
							p := im.RGBAAt(xx, y)
							if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
								top = min(top, y)
								bottom = max(bottom, y)
							}
						}
					}
					if bottom < 0 {
						t.Fatal("missing first-line ink")
					}
					return float64(top+bottom) / 2
				}
				if delta := center(row.X+6, 16) - center(row.X+30, 23); math.Abs(delta) > 1 {
					t.Fatal("work marker is offset from first title line", delta)
				}
			})
		}
	}
}

func TestChildWorkStatusKeepsOneColumnAcrossStates(t *testing.T) {
	v, _ := childRequestsFixture(t)
	states := []string{"running", "waiting", "completed", "unknown"}
	var expected float32
	for i, state := range states {
		w := v.Model.Details["s"].Works[0]
		w.State = state
		d := v.Model.Details["s"]
		if state != "waiting" {
			d.Requests = nil
		}
		v.Model.Details["s"] = d
		if state == "waiting" {
			d.Requests = []domain.PendingRequest{{WorkID: w.ID, State: "pending", Kind: "questions"}}
			v.Model.Details["s"] = d
		}
		ui := n.NewTester(func(c *n.Context) {
			theme := c.Theme()
			n.Row(c).Padding(20).Gap(6).AlignItems(n.Baseline).Children(func() {
				v.workStatus(c, theme, w, "国", n.Font{Family: theme.Font, Size: 11, Weight: 400})
				n.Text(c, "国").FontSize(11).FixedLineHeight(20).Label("状态文字")
			})
		}, 200, 80)
		text, _ := ui.Find("状态文字")
		if i == 0 {
			expected = text.X
		} else if text.X != expected {
			t.Fatal("status shape changes move the adjacent text column", state, expected, text.X)
		}
	}
}
