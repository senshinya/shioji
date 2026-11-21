package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestSteerFinishesReplyFooterBeforeStartingMessageFlight(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			v, ui := nativeQueueFixture()
			if dark {
				v.Model.Preferences.Appearance = "dark"
			}
			detail := v.Model.Details["s"]
			start := ui.Now().Add(-time.Minute)
			detail.Turns[0].StartedAt = &start
			detail.Items[0].TurnID = "active"
			detail.Items[0].Text = "已经完成了第一部分，正在检查剩余结果。"
			detail.Items[0].Final = true
			v.Model.Details["s"] = detail
			ui.Frame()
			ui.Advance(time.Second)
			before := v.historyRowBounds["turn-footer:active"]
			if before.H <= 32 || !ui.HasText("进行中") {
				t.Fatal("fixture did not render the running footer", before)
			}
			record := os.Getenv("SHIOJI_STEER_FOOTER_RECORDING") != ""
			frame := 0
			advance := func(frames int) {
				t.Helper()
				if !record {
					ui.Advance(time.Duration(frames) * (time.Second / 30))
					return
				}
				for range frames {
					ui.Advance(time.Second / 30)
					saveNativeSkillImage(t, ui, fmt.Sprintf("steer-footer-%v-%04d.png", dark, frame))
					frame++
				}
			}

			feedbackClick(t, ui, "消息输入")
			ui.Type("只检查第一部分")
			advance(12)
			ui.Key(n.Cmd, n.KeyEnter)
			job := feedbackJob(t, v, "turn.steer")
			in := job.Input.(protocol.SteerInput).Input
			id := "input:" + in.OperationID
			advance(12)
			pending := v.historyRowBounds["turn-footer:active"]
			inputBeforeConfirmation := v.historyRowBounds[id]
			running, hasRunning := ui.Find("进行中")
			runningWhileWaiting := hasRunning && running.Y >= pending.Y && running.Y+running.H <= pending.Y+pending.H && !ui.HasText("正在发送")

			now := ui.Now()
			item := domain.Item{ID: id, Kind: "user", TurnID: "active", Status: "sent", SentAt: &now, Final: true, Text: in.Text}
			op := domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationConfirmed}
			body, _ := json.Marshal(op)
			job.Apply(v.Model, body, nil)
			ui.Frame()
			inputBeforeItem := v.historyRowBounds[id]
			v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "s", Operation: &op, Item: &item})
			ui.Frame()
			initial := v.historyRowBounds["reply-before:"+id]
			paint := contextPixels(ui, n.Rect{X: initial.X, Y: initial.Y, W: 110, H: 24})
			advance(3)
			mid := v.historyRowBounds["reply-before:"+id]
			midPaint := contextPixels(ui, n.Rect{X: mid.X, Y: mid.Y, W: 110, H: 24})
			inputDuringFooter := v.historyRowBounds[id]
			startedEarly := v.sent == nil || v.sent.phase != sentAwaitingRow
			saveNativeSkillImage(t, ui, fmt.Sprintf("steer-footer-middle-%v.png", dark))
			advance(3)
			settled := v.historyRowBounds["reply-before:"+id]
			inputDuringFlight := v.historyRowBounds[id]
			flew := v.sent != nil && v.sent.phase == sentAnimating
			advance(24)
			saveNativeSkillImage(t, ui, fmt.Sprintf("steer-footer-final-%v.png", dark))

			if inputBeforeConfirmation.H != 0 || inputBeforeItem.H != 0 || pending != before || !runningWhileWaiting {
				t.Errorf("the pending input moved the original footer or began its flight: before=%+v pending=%+v input=%+v receipt=%+v", before, pending, inputBeforeConfirmation, inputBeforeItem)
			}
			if initial.Y != before.Y || initial.H != before.H || mid != initial || inputDuringFooter.H != 0 || startedEarly {
				t.Errorf("footer transition did not stay in the original slot before the input flight: before=%+v initial=%+v mid=%+v input=%+v", before, initial, mid, inputDuringFooter)
			}
			if settled.H <= 32 || inputDuringFlight.Y <= settled.Y || !flew || !ui.HasText("进行中") {
				t.Errorf("input and live progress did not arrive after the footer settled: footer=%+v input=%+v flight=%v", settled, inputDuringFlight, flew)
			}
			if !footerHasInk(paint) || !footerHasInk(midPaint) || bytes.Equal(paint, midPaint) {
				t.Error("the original indicator or its in-place transition left a blank or frozen frame")
			}
		})
	}
}

func footerHasInk(pixels []byte) bool {
	for i := 3; i+2 < len(pixels); i += 3 {
		delta := int(pixels[i]) - int(pixels[0])
		if delta > 8 || delta < -8 {
			return true
		}
	}
	return false
}
