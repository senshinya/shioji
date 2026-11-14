package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// Capture the full transcript renderer, including list-row spacing and footer
// clearance; directly stacking item renderers omits both layout contracts.
func TestCompactionContinuationTranscriptSpacing(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			v := liveCompletionModel()
			d := v.Model.Details["s"]
			start := time.Now().Add(-13*time.Minute - 51*time.Second)
			d.Turns[0].StartedAt = &start
			d.Items = []domain.Item{{ID: "compact", Kind: "activity", Presentation: "compaction", TurnID: "t", Name: "正在压缩上下文", Status: "running"}}
			v.Model.Details["s"] = d
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			row := v.historyRowBounds["compact"]
			crop := image.Rect(int(row.X), int(row.Y)-12, int(row.X)+620, int(row.Y)+198)
			dir := os.Getenv("SHIOJI_COMPACTION_SPACING_CAPTURE")
			if dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for frame := 0; frame < 162; frame++ {
				if frame == 78 {
					d.Items[0].Final, d.Items[0].Status, d.Items[0].Name = true, "completed", "上下文已压缩"
					d.Items = append(d.Items, domain.Item{ID: "reply", Kind: "assistant", TurnID: "t", Text: "继续回复"})
					v.Model.Details["s"] = d
					ui.Frame()
				}
				if dir != "" {
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("dark-%v-%03d.png", dark, frame)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(f, ui.Image().SubImage(crop))
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
				ui.Advance(time.Second / 60)
			}
			compact, ok := ui.Find("上下文已压缩")
			if !ok {
				t.Fatal("missing completion")
			}
			reply := v.historyRowBounds["reply"]
			status, ok := ui.Find("正在回复")
			if !ok {
				t.Fatal("missing progress")
			}
			first := reply.Y - (compact.Y + compact.H)
			second := status.Y - (reply.Y + reply.H - v.bodyMetadataGap())
			t.Logf("compaction→body: %.1f DIP; body→progress: %.1f DIP", first, second)
			if first < 7.5 || second < v.bodyMetadataGap()-.5 {
				t.Fatal("transcript clearance collapsed", compact, reply, status)
			}
		})
	}
}
