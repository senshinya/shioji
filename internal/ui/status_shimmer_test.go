package ui

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestAttachedRuntimeMetadataSweepsWithItsStatus(t *testing.T) {
	glyph := func(t *testing.T, ui *n.Tester, label string, last bool) n.Rect {
		t.Helper()
		r, seen := ui.Find(label)
		if !seen {
			t.Fatalf("missing %s", label)
		}
		if last {
			r.X += r.W - 11
		}
		r.W = 11
		return r
	}
	probe := func(t *testing.T, ui *n.Tester, regions ...n.Rect) {
		base := make([][]byte, len(regions))
		moved := make([]bool, len(regions))
		for i, r := range regions {
			base[i] = contextPixels(ui, r)
		}
		for i := 0; i < 16; i++ {
			ui.Advance(140 * time.Millisecond)
			ui.Frame()
			for j, r := range regions {
				moved[j] = moved[j] || !bytes.Equal(base[j], contextPixels(ui, r))
			}
		}
		for i, changed := range moved {
			if !changed {
				t.Errorf("no sweep reached painted region %d", i)
			}
		}
	}
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("compaction-%v", dark), func(t *testing.T) {
			v := toolTailFixture(1)
			d := v.Model.Details["s"]
			item := domain.Item{ID: "audit-compaction", Kind: "activity", Presentation: "compaction", Name: "正在压缩上下文", TurnID: "t", Status: "running"}
			d.Items = append(d.Items, item)
			d.Turns[0].StartedAt = nil
			var elapsedStart *time.Time
			ui := n.NewTester(func(c *n.Context) {
				if elapsedStart == nil {
					elapsedStart = new(c.Now().Add(-34 * time.Second))
					d.Turns[0].StartedAt = elapsedStart
					v.Model.Details["s"] = d
				}
				n.Column(c).Fill().Padding(12).Background(c.Theme().Background).Children(func() {
					v.contextCompactionItem(c, c.Theme(), item)
				})
			}, 480, 54)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			clock, ok := ui.Find("运行时长")
			if !ok {
				t.Fatal("missing compaction timer")
			}
			font := n.Font{Size: transcriptCaptionSize, Weight: 400, Features: "tnum"}
			width, _ := n.ShapeText("35 秒", font, 0).Size()
			clock.X, clock.W = clock.X+width-11, 11
			probe(t, ui, glyph(t, ui, item.Name, false), clock)
		})
		t.Run(fmt.Sprintf("latest-%v", dark), func(t *testing.T) {
			_, ui := latestMotionFixture()
			ui.SetDark(dark)
			ui.Advance(250 * time.Millisecond)
			row, _ := ui.Find("最新运行状态")
			right := n.Rect{X: row.X + row.W - 19, Y: row.Y, W: 14, H: row.H}
			probe(t, ui, glyph(t, ui, "正在执行命令", false), glyph(t, ui, "运行时长", true), right)
		})
		t.Run(fmt.Sprintf("worklists-%v", dark), func(t *testing.T) {
			v, ui := sessionWorkFixture(t)
			d := v.Model.Details["s"]
			d.Works[0].StartedAt = new(ui.Now().Add(-35 * time.Second))
			d.Works[1].StartedAt = new(ui.Now().Add(-46 * time.Second))
			v.Model.Details["s"] = d
			ui.SetDark(dark)
			ui.Advance(time.Second)
			ui.Frame()
			probe(t, ui, glyph(t, ui, d.Works[0].Title, false), glyph(t, ui, workDuration(d.Works[0], ui.Now()), true),
				glyph(t, ui, d.Works[1].Title, false), glyph(t, ui, workDuration(d.Works[1], ui.Now()), true))
		})
	}
}
