package ui

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestAutomaticCompactionOwnsLiveProgressAndElapsed(t *testing.T) {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	start := time.Now().Add(-13*time.Minute - 51*time.Second)
	d.Turns[0].StartedAt = &start
	d.Items = []domain.Item{{ID: "u", Kind: "user", TurnID: "t", Text: "继续", Final: true}, {ID: "compact", Kind: "activity", Presentation: "compaction", TurnID: "t", Name: "正在压缩上下文", Status: "running"}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if slices.Contains(ui.Texts(), "进行中") || slices.Contains(ui.Texts(), "正在准备") {
		t.Error("compression duplicates the ordinary live footer", ui.Texts())
	}
	label, _ := ui.Find("正在压缩上下文")
	clock, ok := ui.Find("运行时长")
	if !ok || math.Abs(float64(clock.Y-label.Y)) > 1 {
		t.Error("compression has no elapsed clock on its own row", label, clock, ok)
	}
	d.Items[1].Final, d.Items[1].Status, d.Items[1].Name = true, "completed", "上下文已压缩"
	d.Items = append(d.Items, domain.Item{ID: "reply", Kind: "assistant", TurnID: "t", Text: "继续回复"})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("正在回复") || !ui.HasText("上下文已压缩") {
		t.Fatal("completed compression did not hand activity back to the reply", ui.Texts())
	}
	label, _ = ui.Find("正在回复")
	clock, ok = ui.Find("运行时长")
	if !ok || math.Abs(float64(clock.Y-label.Y)) > 1 {
		t.Fatal("clock remains on the completed compression row", label, clock)
	}
}

func TestInactiveCompactionDoesNotHideCurrentProgress(t *testing.T) {
	for _, state := range []string{"completed", "other-turn", "child", "later-reply"} {
		t.Run(state, func(t *testing.T) {
			v := liveCompletionModel()
			d := v.Model.Details["s"]
			item := domain.Item{ID: "compact", Kind: "activity", Presentation: "compaction", TurnID: "t", Name: "正在压缩上下文"}
			switch state {
			case "completed":
				item.Final = true
			case "other-turn":
				item.TurnID = "old"
			case "child":
				item.WorkID = "child"
			}
			d.Items = []domain.Item{item}
			if state == "later-reply" {
				d.Items = append(d.Items, domain.Item{ID: "reply", Kind: "assistant", TurnID: "t", Text: "继续回复"})
			}
			v.Model.Details["s"] = d
			ui := n.NewTester(func(c *n.Context) { v.turnFooter(c, c.Theme(), d.Turns[0]) }, 620, 100)
			if !ui.HasText("进行中") && !ui.HasText("正在回复") {
				t.Fatal("inactive compression hides current-turn activity", ui.Texts())
			}
		})
	}
}

func TestNativeCompactionClockAndSingleStatus(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("manual=%v/dark=%v", manual, dark), func(t *testing.T) {
				v := liveCompletionModel()
				d := v.Model.Details["s"]
				start := time.Now().Add(-13*time.Minute - 51*time.Second)
				d.Turns[0].StartedAt = &start
				d.Items = []domain.Item{{ID: "compact", Kind: "activity", Presentation: "compaction", TurnID: "t", Name: "正在压缩上下文", Status: "running"}}
				if manual {
					d.Turns[0].Presentation = "compaction"
					v.Model.Snapshot.Sessions[0].ThreadID = "source"
					v.Model.Snapshot.Sessions[0].ContextAction = &domain.ContextAction{OperationID: "manual", SourceRef: "source", TurnID: "t", State: "running"}
					d.Items = nil // Accepted turn, source item has not arrived yet.
				}
				v.Model.Details["s"] = d
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Padding(20).Background(c.Theme().Background).Children(func() {
						for _, row := range v.contextRows(v.transcriptRows(d.Items)) {
							v.item(c.Key(row.key), c.Theme(), row.item)
						}
						if !manual {
							v.turnFooter(c, c.Theme(), d.Turns[0])
						}
					})
				}, 620, 130)
				ui.SetDark(dark)
				start = ui.Now().Add(-13*time.Minute - 51*time.Second)
				ui.Frame()
				ui.Advance(250 * time.Millisecond)
				saveNativeSkillImage(t, ui, fmt.Sprintf("compaction-manual-%v-dark-%v.png", manual, dark))
				label, _ := ui.Find("正在压缩上下文")
				clock, ok := ui.Find("运行时长")
				if !ok || math.Abs(float64(clock.Y-label.Y)) > 1 {
					t.Error("native clock is not beside compression status", label, clock, ok)
				}
				if slices.Contains(ui.Texts(), "进行中") {
					t.Error("native view paints two live statuses")
				}
				before := contextPixels(ui, clock)
				ui.Advance(time.Second)
				if bytes.Equal(before, contextPixels(ui, clock)) {
					t.Error("compression clock does not tick")
				}
				if manual {
					// Adopt the actual source item without replacing the elapsed origin.
					d.Items = []domain.Item{{ID: "source-item", Kind: "activity", Presentation: "compaction", TurnID: "t", Name: "正在压缩上下文", Status: "running"}}
					v.Model.Details["s"] = d
					ui.Frame()
					adopted, ok := ui.Find("运行时长")
					if !ok || adopted != clock {
						t.Error("receipt-to-source handoff moves or drops the clock", clock, adopted)
					}
				}
				clock, _ = ui.Find("运行时长")
				before = contextPixels(ui, clock)
				v.Model.ConnectionEpoch = "reconnected"
				ui.Frame()
				if !bytes.Equal(before, contextPixels(ui, clock)) {
					t.Error("reconnecting resets the existing compression clock")
				}
				if !manual {
					accepted := ui.Now()
					d.Items = append(d.Items, domain.Item{ID: "inserted", Kind: "user", TurnID: "t", Text: "继续插入", SentAt: &accepted, Final: true})
					v.Model.Details["s"] = d
					ui.Frame()
					if bytes.Equal(before, contextPixels(ui, clock)) {
						t.Error("compression bypasses the accepted insertion's elapsed reset")
					}
				}
				d.Turns[0].StartedAt = nil
				for i := range d.Items {
					d.Items[i].SentAt = nil
				}
				v.Model.Details["s"] = d
				ui.Frame()
				if _, ok := ui.Find("运行时长"); ok {
					t.Error("unknown worker time is fabricated from UI mount time")
				}
			})
		}
	}
}
