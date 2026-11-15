package ui

import (
	"bytes"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

// The live tool summary is one sweep, including metadata drawn by the clock.
// Sampling only the clock's final unit excludes ordinary ticking digits.
func TestToolSummaryShimmerCoversMetadataWithoutRestarting(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			for _, folded := range []bool{false, true} {
				t.Run(fmt.Sprintf("dark=%v/reduced=%v/folded=%v", dark, reduced, folded), func(t *testing.T) {
					v := toolTailFixture(9)
					row := transcriptRow{group: "tools", count: 9, issues: 1, item: domain.Item{TurnID: "t"}}
					if v.Model.ToolDisclosure(row.group).Closed() != folded {
						v.Model.SetToolDisclosure(row.group, v.Model.ToolDisclosure(row.group).Toggle())
					}
					v.runningGroup = row.group
					ui := n.NewTester(func(c *n.Context) {
						if v.runningProgress == nil {
							v.runningProgress = &runningProgress{started: c.Now(), label: "进行中"}
						}
						n.Column(c).Fill().Padding(16).Children(func() { v.toolGroup(c, c.Theme(), row, 0) })
					}, 640, 56)
					ui.SetDark(dark)
					ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
					ui.Advance(500 * time.Millisecond)
					labels := []string{"进行中", "运行时长"}
					if folded {
						labels = append(labels, "1 项未成功")
					}
					regions := make([]n.Rect, len(labels))
					before := make([][]byte, len(labels))
					changed := make([]bool, len(labels))
					for i, label := range labels {
						r, ok := ui.Find(label)
						if !ok {
							t.Fatal("missing summary metadata", label)
						}
						if label == "运行时长" {
							r.X += r.W - 11
							r.W = 11
						}
						regions[i], before[i] = r, contextPixels(ui, r)
					}
					for frame := 0; frame < 28; frame++ {
						saveNativeSkillImage(t, ui, fmt.Sprintf("summary-dark-%v-reduced-%v-folded-%v-%03d.png", dark, reduced, folded, frame))
						ui.Advance(80 * time.Millisecond)
						for i, r := range regions {
							changed[i] = changed[i] || !bytes.Equal(before[i], contextPixels(ui, r))
						}
					}
					for i, label := range labels {
						if changed[i] == reduced {
							t.Errorf("metadata shimmer changed=%v, reduced motion=%v: %s", changed[i], reduced, label)
						}
					}
					// Same-width updates leave other text at exactly the same sweep
					// position; remounting/restarting the effect changes these pixels.
					ui.Advance(420 * time.Millisecond)
					ui.Frame()
					phase, _ := ui.Find("进行中")
					header, _ := ui.Find("9 次工具调用")
					header.W = phase.X - header.X
					pixels := contextPixels(ui, header)
					v.runningProgress.label = "思考中"
					ui.Frame()
					if !bytes.Equal(pixels, contextPixels(ui, header)) {
						t.Error("phase update restarted the count sweep")
					}
				})
			}
		}
	}
}

func TestInspectorSectionIconsAndDivider(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		m.Inspectors["s"] = true
		d := m.Details["s"]
		d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "task", Status: "pending"}}}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		panel, _ := ui.Find("更改侧栏")
		im := ui.Image()
		want := uint8(0xe3)
		if dark {
			want = 0x34
		}
		for _, y := range []int{400, 800} {
			if got := im.RGBAAt(int(panel.X), y); got.R != want {
				t.Fatal("section covers sidebar divider", dark, got)
			}
		}
		firstHeight := 0
		for i, label := range []string{"文件更改", "任务"} {
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("missing section title", label)
			}
			bg := im.RGBAAt(int(r.X), int(r.Y))
			minX, minY, maxX, maxY := 28, 28, -1, -1
			for py := 2; py < 26; py++ {
				for px := 2; px < 26; px++ {
					p := im.RGBAAt(int(r.X)+px, int(r.Y)+py)
					delta := int(p.R) - int(bg.R)
					if delta > 35 || delta < -35 {
						minX = min(minX, px)
						maxX = max(maxX, px)
						minY = min(minY, py)
						maxY = max(maxY, py)
					}
				}
			}
			if maxX < 0 || math.Abs(float64(minX+maxX)/2-13.5) > 1 {
				t.Fatal("section glyph not centered", dark, label, minX, maxX)
			}
			if i == 0 {
				firstHeight = maxY - minY + 1
			} else if delta := maxY - minY + 1 - firstHeight; delta > 1 || delta < -1 {
				t.Fatal("section glyph heights differ", dark, firstHeight, maxY-minY+1)
			}
		}
	}
}

func TestShimmerMovesOnlyActiveTextAndHonorsReducedMotion(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			m := layoutModel()
			m.Inspectors["s"] = true
			d := m.Details["s"]
			d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: "正在执行较长任务，验证文字的 Shimmer 流动效果", Status: "running"}, {Text: "尚未执行的任务", Status: "pending"}}}
			d.Items[0].TurnID = "t"
			d.Turns = []domain.Turn{{ID: "t", Status: "running"}}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(func(c *n.Context) {
				v.View(c)
				// Coexist with a tracked native drawing: its partial repaint
				// must not freeze the separate text effects.
				n.Column(c).Absolute().Left(0).Top(0).Size(1, 1).Draw(func(p *n.Painter, r n.Rect) { p.Fill(r, n.RGB(0, 128, 0), 0); p.AnimationFrameRate(30) })
			}, 1440, 900)
			ui.SetDark(dark)
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(300 * time.Millisecond)
			capture := func(label string) []byte {
				r, ok := ui.Find(label)
				if !ok {
					t.Fatal("missing shimmer target", label)
				}
				im := ui.Image()
				var pixels []byte
				for y := int(r.Y); y < int(r.Y+r.H); y++ {
					for x := int(r.X); x < int(r.X+r.W); x++ {
						p := im.RGBAAt(x, y)
						pixels = append(pixels, p.R, p.G, p.B, p.A)
					}
				}
				return pixels
			}
			for _, label := range []string{d.Plan.Steps[0].Text, "正在回复", d.Plan.Steps[1].Text} {
				before := capture(label)
				changed := false
				for i := 0; i < 5; i++ {
					ui.Advance(240 * time.Millisecond)
					changed = changed || !bytes.Equal(before, capture(label))
				}
				want := !reduced && label != d.Plan.Steps[1].Text
				if changed != want {
					t.Fatal("wrong active/reduced-motion shimmer behavior", dark, reduced, label, changed)
				}
			}
		}
	}
}

func TestTaskMarkersAndHeaderAlignToVisibleChineseAndLatinText(t *testing.T) {
	for _, label := range []string{"国国国", "HHH", "国 H 国", strings.Repeat("国", 40)} {
		m := layoutModel()
		m.Inspectors["s"] = true
		d := m.Details["s"]
		d.Plan = &domain.Plan{TurnID: "t", Steps: []domain.PlanStep{{Text: label, Status: "pending"}}}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.Advance(300 * time.Millisecond)
		center := func(r n.Rect) float32 {
			im := ui.Image()
			minY, maxY := im.Bounds().Max.Y, -1
			bg := im.RGBAAt(int(r.X+r.W/2), int(r.Y+1))
			for y := int(r.Y); y < int(r.Y+r.H); y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					p := im.RGBAAt(x, y)
					if abs := int(p.R) - int(bg.R); abs > 30 || abs < -30 {
						minY = min(minY, y)
						maxY = max(maxY, y)
					}
				}
			}
			if maxY < 0 {
				t.Fatal("missing visible ink", r)
			}
			return float32(minY+maxY) / 2
		}
		text, _ := ui.Find(label)
		text.H = min(text.H, 20)
		dot, _ := ui.Find("任务待处理")
		if delta := center(dot) - center(text); delta > 1 || delta < -1 {
			t.Fatal("status dot does not align to text ink", label, delta, dot, text)
		}
		header, _ := ui.Find("任务标题")
		icon, _ := ui.Find("任务")
		icon.X += 6
		icon.Y += 6
		icon.W, icon.H = 16, 16
		if delta := center(icon) - center(header); delta > 1 || delta < -1 {
			t.Fatal("header icon does not align to title ink", delta, icon, header)
		}
	}
}
