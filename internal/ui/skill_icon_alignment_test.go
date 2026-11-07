package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
)

func TestSkillBookIconAlignsWithChineseAndLatinInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, label := range []string{"HHH", "国国国", "国 H 国", "review-tree", "debug", "skill-creator", "design", "check", "代码审查", "review 代码"} {
			t.Run(fmt.Sprintf("dark=%v/%s", dark, label), func(t *testing.T) {
				tester := n.NewTester(func(c *n.Context) {
					theme := c.Theme()
					font := n.Font{Family: theme.Font, Size: 13, Weight: 400}
					n.Column(c).Fill().Background(theme.Background).Padding(20).Gap(12).Children(func() {
						n.Row(c).Height(26).Gap(8).AlignItems(n.Baseline).Children(func() {
							referenceSymbol(c, skillIcon, label, font, 14, theme.Text).Label("菜单图标")
							n.Text(c, label).FontSize(13).FixedLineHeight(20).Label("菜单文字")
						})
						attachment := (&View{}).referenceAttachment(c, composerReference{skill: app.DraftSkill{Reference: domain.SkillReference{ID: "skill", Name: label}}})
						n.Box(c).Size(attachment.Width, attachment.Height).Draw(attachment.Draw).Label("引用标签")
					})
				}, 300, 150)
				tester.SetDark(dark)
				center := func(box n.Rect) float64 {
					im := tester.Image()
					bg := im.RGBAAt(0, 0)
					top, bottom := im.Bounds().Max.Y, -1
					for y := int(box.Y); y < int(box.Y+box.H); y++ {
						for x := int(box.X); x < int(box.X+box.W); x++ {
							p := im.RGBAAt(x, y)
							d := int(p.R) - int(bg.R)
							if d > 30 || d < -30 {
								top = min(top, y)
								bottom = max(bottom, y)
							}
						}
					}
					if bottom < 0 {
						t.Fatal("missing rendered icon/text ink", box)
					}
					return float64(top+bottom) / 2
				}
				icon, _ := tester.Find("菜单图标")
				text, _ := tester.Find("菜单文字")
				if delta := center(icon) - center(text); math.Abs(delta) > 1 {
					t.Fatal("menu icon is not aligned to visible text", delta, icon, text)
				}
				chip, _ := tester.Find("引用标签")
				chipIcon := chip
				chipIcon.X += 6
				chipIcon.Y += 1
				chipIcon.H -= 2
				chipIcon.W = 14
				// The icon occupies the capsule's stable center. Latin text uses
				// one baseline with descender room, not a per-word ink center.
				if delta := center(chipIcon) - float64(chip.Y+chip.H/2); math.Abs(delta) > 1 {
					t.Fatal("reference icon moved out of its shared slot", delta, chip)
				}
			})
		}
	}
}

// Descenders may extend the word's ink, but must never move its shared letters.
// This checks the actual native attachment painter, rather than centering each
// word's entire bounding box (which hid the baseline regression).
func TestInlineReferenceTextKeepsBaselineAcrossDescenders(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark=%v/scale=%g", dark, scale), func(t *testing.T) {
				labels := []string{"check", "checkg", "checkp", "checky"}
				tester := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Background(c.Theme().Background).Padding(20).Gap(8).Children(func() {
						for _, label := range labels {
							a := (&View{}).referenceAttachment(c, composerReference{skill: app.DraftSkill{Reference: domain.SkillReference{ID: label, Name: label}}})
							n.Box(c).Size(a.Width, a.Height).Draw(a.Draw).Label(label)
						}
					})
				}, 250, 170)
				tester.SetDark(dark)
				tester.SetScale(scale)
				image := tester.Image()
				var expected float32
				for i, label := range labels {
					box, ok := tester.Find(label)
					if !ok {
						t.Fatal("missing native label", label)
					}
					bg := image.RGBAAt(int((box.X+23)*scale), int((box.Y+10)*scale))
					top, bottom := image.Bounds().Max.Y, -1
					for y := int((box.Y + 1) * scale); y < int((box.Y+box.H-1)*scale); y++ {
						for x := int((box.X + 25) * scale); x < int((box.X+30)*scale); x++ {
							p := image.RGBAAt(x, y)
							delta := int(p.R) - int(bg.R)
							if delta > 60 || delta < -60 {
								top = min(top, y)
								bottom = max(bottom, y)
							}
						}
					}
					if bottom < 0 {
						t.Fatal("missing shared letter ink", label)
					}
					center := float32(top+bottom)/(2*scale) - box.Y
					if i == 0 {
						expected = center
					} else if math.Abs(float64(center-expected)) > .5 {
						t.Fatalf("descender moved shared prefix: %s=%g, check=%g DIP", label, center, expected)
					}
				}
			})
		}
	}
}

func TestInlineSkillAndFileIconsHaveComparableVisualSize(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark=%v/scale=%g", dark, scale), func(t *testing.T) {
				tester := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Background(c.Theme().Background).Padding(20).Gap(10).Children(func() {
						for _, skill := range []bool{false, true} {
							ref := composerReference{file: app.DraftAttachment{Attachment: domain.Attachment{ID: "file", Name: "check"}}}
							label := "file"
							if skill {
								ref = composerReference{skill: app.DraftSkill{Reference: domain.SkillReference{ID: "skill", Name: "check"}}}
								label = "skill"
							}
							a := (&View{}).referenceAttachment(c, ref)
							n.Box(c).Size(a.Width, a.Height).Draw(a.Draw).Label(label)
						}
					})
				}, 200, 110)
				tester.SetDark(dark)
				tester.SetScale(scale)
				im := tester.Image()
				bounds := func(label string) n.Rect {
					box, _ := tester.Find(label)
					bg := im.RGBAAt(int((box.X+23)*scale), int((box.Y+10)*scale))
					left, top, right, bottom := im.Bounds().Max.X, im.Bounds().Max.Y, -1, -1
					for y := int((box.Y + 2) * scale); y < int((box.Y+box.H-2)*scale); y++ {
						for x := int((box.X + 4) * scale); x < int((box.X+22)*scale); x++ {
							p := im.RGBAAt(x, y)
							delta := int(p.R) - int(bg.R)
							if delta > 40 || delta < -40 {
								left = min(left, x)
								right = max(right, x)
								top = min(top, y)
								bottom = max(bottom, y)
							}
						}
					}
					if right < 0 {
						t.Fatal("missing icon", label)
					}
					return n.Rect{X: float32(left)/scale - box.X, Y: float32(top)/scale - box.Y, W: float32(right-left+1) / scale, H: float32(bottom-top+1) / scale}
				}
				file, skill := bounds("file"), bounds("skill")
				ratio := skill.W * skill.H / (file.W * file.H)
				if ratio < .8 || ratio > 1.2 {
					t.Fatalf("icon visual footprint differs: skill/file=%g, skill=%+v file=%+v", ratio, skill, file)
				}
				if math.Abs(float64((skill.X+skill.W/2)-(file.X+file.W/2))) > .5 || math.Abs(float64((skill.Y+skill.H/2)-(file.Y+file.H/2))) > .5 {
					t.Fatal("optical size adjustment moved the icon center", skill, file)
				}
			})
		}
	}
}
