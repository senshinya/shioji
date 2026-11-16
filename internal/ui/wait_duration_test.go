package ui

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestWaitDurationStaysBesideHeading(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{280, 768} {
			for _, final := range []bool{false, true} {
				t.Run(fmt.Sprintf("dark=%v/width=%d/final=%v", dark, width, final), func(t *testing.T) {
					v := New(layoutModel(), nil, "")
					status := "running"
					if final {
						status = "completed"
					}
					item := domain.Item{ID: "wait", Kind: "tool", Presentation: "wait", Name: "等待", Status: status, Final: final, Parts: []domain.ContentPart{{Kind: "text", Label: "时长", Text: "45.0 秒"}}}
					ui := n.NewTester(func(c *n.Context) {
						theme := applyAppearance(c, v.Model.Preferences.Normalized())
						n.Column(c).Fill().Background(theme.Background).Padding(20).Gap(8).Children(func() { v.activityItem(c, theme, item) })
					}, width, 180)
					ui.SetDark(dark)
					ui.Advance(time.Second)
					if dir := os.Getenv("SHIOJI_WAIT_CAPTURE"); dir != "" && !final {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(dir, fmt.Sprintf("wait-%d-dark-%v.png", width, dark)))
						if err != nil {
							t.Fatal(err)
						}
						if err := png.Encode(f, ui.Image()); err != nil {
							t.Fatal(err)
						}
						f.Close()
					}
					label, ok := ui.Find("等待")
					if !ok {
						t.Fatal("missing wait heading")
					}
					if ui.HasText("时长") {
						t.Fatal("duration still has a separate detail label")
					}
					value, ok := ui.Find("45.0 秒")
					if !ok {
						t.Fatal("missing duration value")
					}
					headingWidth, _ := n.ShapeText("等待", n.Font{Family: v.Model.Preferences.InterfaceFont, Size: 12}, 0).Size()
					if math.Abs(float64(value.X-(label.X+headingWidth+6))) > 1 || math.Abs(float64((value.Y+value.H)-(label.Y+label.H))) > 1 {
						t.Fatalf("duration was stacked instead of inline: label=%+v value=%+v", label, value)
					}
					if value.X+value.W > float32(width)-19 {
						t.Fatal("duration escaped content edge", value)
					}
					if err := ui.Click("等待"); err != nil {
						t.Fatal(err)
					}
					ui.Advance(time.Second)
					if v.Model.Collapsed["activity:"+v.Model.SessionID+"/wait"] {
						t.Fatal("wait heading still toggles a detail area")
					}
					if !ui.HasText("45.0 秒") {
						t.Fatal("duration disappeared after clicking heading")
					}
					ui.Click("等待")
					ui.Advance(time.Second)
					if !ui.HasText("45.0 秒") {
						t.Fatal("duration was lost after repeated heading clicks")
					}
				})
			}
		}
	}
}

// Catch mixed-size status text and timeline glyphs centered on the line box
// instead of the first line's visible text, including Retina rounding.
func TestWaitTimelineUsesAlignedUniformTypography(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, name := range []string{"等待", "HHH", "等待 H"} {
				for _, final := range []bool{false, true} {
					t.Run(fmt.Sprintf("dark=%v/scale=%g/name=%s/final=%v", dark, scale, name, final), func(t *testing.T) {
						v := New(layoutModel(), nil, "")
						status := "running"
						if final {
							status = "completed"
						}
						item := domain.Item{ID: "wait", Kind: "tool", Presentation: "wait", Name: name, Status: status, Final: final, Parts: []domain.ContentPart{{Kind: "text", Label: "时长", Text: "30.0 秒"}}}
						ui := n.NewTester(func(c *n.Context) {
							theme := applyAppearance(c, v.Model.Preferences.Normalized())
							n.Column(c).Fill().Background(theme.Background).Padding(20).Children(func() {
								n.Column(c).FillWidth().PaddingStart(24).Children(func() {
									v.toolTimelineItem(c, theme, transcriptRow{item: item})
								})
							})
						}, 360, 72)
						ui.SetDark(dark)
						ui.SetScale(scale)
						ui.Advance(time.Second)
						saveNativeSkillImage(t, ui, fmt.Sprintf("wait-alignment-dark-%v-scale-%g-name-%s-final-%v.png", dark, scale, name, final))
						caption, ok := ui.Find(activityStatus(status))
						if !ok {
							t.Fatal("missing wait status")
						}
						font := n.Font{Family: v.Model.Preferences.InterfaceFont, Size: 12}
						width, _ := n.ShapeText(activityStatus(status), font, 0).Size()
						if math.Abs(float64(caption.W-width)) > .5 {
							t.Errorf("status does not share the heading's font size: width=%g want=%g", caption.W, width)
						}
						im := ui.Image()
						bg := im.RGBAAt(0, 0)
						center := func(box n.Rect) float64 {
							top, bottom := im.Bounds().Max.Y, -1
							for y := int(box.Y * scale); y < int((box.Y+box.H)*scale); y++ {
								for x := int(box.X * scale); x < int((box.X+box.W)*scale); x++ {
									p := im.RGBAAt(x, y)
									if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
										top, bottom = min(top, y), max(bottom, y)
									}
								}
							}
							if bottom < 0 {
								t.Fatal("missing native ink", box)
							}
							return float64(top+bottom) / (2 * float64(scale))
						}
						icon := n.Rect{X: 20, Y: 20, W: 16, H: 20}
						titleWidth, _ := n.ShapeText(name, font, 0).Size()
						title := n.Rect{X: 44, Y: 20, W: titleWidth, H: 20}
						if delta := center(icon) - center(title); math.Abs(delta) > 1 {
							t.Errorf("timeline icon is offset from title ink by %g DIP", delta)
						}
					})
				}
			}
		}
	}
}
