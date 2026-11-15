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

func TestWaitDurationStaysBesideItsLabel(t *testing.T) {
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
					label, ok := ui.Find("时长")
					if !ok {
						t.Fatal("missing duration label")
					}
					value, ok := ui.Find("45.0 秒")
					if !ok {
						t.Fatal("missing duration value")
					}
					if value.X < label.X+label.W+4 || math.Abs(float64((value.Y+value.H)-(label.Y+label.H))) > 2 {
						t.Fatalf("duration was stacked instead of inline: label=%+v value=%+v", label, value)
					}
					if value.X+value.W > float32(width)-19 {
						t.Fatal("duration escaped content edge", value)
					}
					if err := ui.Click("等待"); err != nil {
						t.Fatal(err)
					}
					ui.Advance(time.Second)
					if ui.HasText("45.0 秒") {
						t.Fatal("duration ignored disclosure close")
					}
					ui.Click("等待")
					ui.Advance(time.Second)
					if !ui.HasText("45.0 秒") {
						t.Fatal("duration was lost on reopening")
					}
				})
			}
		}
	}
}
