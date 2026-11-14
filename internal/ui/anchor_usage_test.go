package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestHistoryEditorFitDoesNotBecomeASendingAnchor(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/reduced=%v", dark, reduced), func(t *testing.T) {
				v, _, item := historyFixture()
				d := v.Model.Details["s"]
				d.Items = append([]domain.Item{
					{ID: "earlier-user", TurnID: "earlier", Kind: "user", Status: "sent", Text: "Earlier prompt", Final: true},
					{ID: "earlier-reply", TurnID: "earlier", Kind: "assistant", Text: "Earlier reply", Final: true},
				}, d.Items...)
				d.Turns = append([]domain.Turn{{ID: "earlier", Status: "completed", Order: -1}}, d.Turns...)
				v.Model.Details["s"] = d
				ui := n.NewTester(v.View, 850, 650)
				ui.SetDark(dark)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				v.lists["s"].KeepRow(4, 450)
				ui.Frame()
				v.beginHistoryEdit(item)
				ui.Frame()
				ui.Advance(time.Second)
				before, ok := ui.Find("历史消息编辑器")
				if !ok {
					t.Fatal("history editor did not open")
				}
				if v.lists["s"].FollowingEnd() {
					t.Error("fitting a history editor activates send/end following")
				}
				if !reduced {
					saveNativeSkillImage(t, ui, fmt.Sprintf("editor-dark-%v-initial.png", dark))
				}
				for i := 0; i < 30; i++ {
					d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("late-", i), TurnID: item.TurnID, Kind: "assistant", Text: fmt.Sprint("历史补到内容 ", i), Final: true})
				}
				v.Model.Details["s"] = d
				ui.Frame()
				ui.Advance(time.Second)
				after, ok := ui.Find("历史消息编辑器")
				if !reduced {
					saveNativeSkillImage(t, ui, fmt.Sprintf("editor-dark-%v-history-growth.png", dark))
				}
				if !ok || math.Abs(float64(after.Y-before.Y)) > 1 || v.lists["s"].FollowingEnd() {
					t.Error("new history pulls the reader away from the inline editor", before, after, ok)
				}
				if v.Model.HistoryEdits["s"].ItemID != item.ID {
					t.Fatal("history update discards the active edit")
				}
			})
		}
	}
}

func TestObservedQueuedInputUsesItsAuthoritativeTurnBoundary(t *testing.T) {
	for _, sameTurn := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("same-turn=%v/reduced=%v", sameTurn, reduced), func(t *testing.T) {
				v, ui := steerScrollFixture(true, true, reduced)
				q := v.Model.Queue()
				input := q.Items[0].Input
				d := v.Model.Details["s"]
				turn := "active"
				if !sameTurn {
					turn = "next-turn"
					d.Items[len(d.Items)-1].Final = true
					d.Turns[0].Status = "completed"
					d.Turns = append(d.Turns, domain.Turn{ID: turn, Status: "running"})
				}
				// The input/queue update is observed from the worker; this client
				// did not click queue.send or record any local dispatch metadata.
				d.Items = append(d.Items, domain.Item{ID: "input:" + input.OperationID, TurnID: turn, Kind: "user", Text: input.Text, Status: "sent", Final: true})
				v.Model.Details["s"] = d
				q.Items = q.Items[1:]
				q.Revision++
				v.Model.SetQueue(q)
				ui.Frame()
				ui.Advance(time.Second)
				if sameTurn {
					footer, ok := ui.Find("回复信息")
					if !ok {
						t.Fatal("observed steer lost the current footer")
					}
					if !reduced {
						saveNativeSkillImage(t, ui, "observed-steer-initial.png")
					}
					ui.Scroll(800, 300, 0, -1)
					ui.Frame()
					ui.Advance(400 * time.Millisecond)
					after, ok := ui.Find("回复信息")
					if !reduced {
						saveNativeSkillImage(t, ui, "observed-steer-after-wheel.png")
					}
					if !ok || math.Abs(float64(after.Y-footer.Y)) > 2 {
						t.Error("another client's queued steer receives new-turn reading space", footer, after)
					}
				} else {
					message, ok := ui.Find(input.Text)
					if !ok || message.Y < 80 || message.Y > 125 {
						t.Fatal("automatic new turn lost its intended reading position", message, ok)
					}
				}
			})
		}
	}
}
