package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestSteerAfterProseAndToolsKeepsOneLayoutHandoff(t *testing.T) {
	for _, tc := range []struct{ eventFirst, collapsed bool }{
		{false, false}, {false, true}, {true, false}, {true, true},
	} {
		eventFirst, collapsed := tc.eventFirst, tc.collapsed
		t.Run(fmt.Sprintf("event-first=%v/collapsed=%v", eventFirst, collapsed), func(t *testing.T) {
			v, ui := steerScrollFixture(false, true, false)
			detail := v.Model.Details["s"]
			replyID := detail.Items[len(detail.Items)-2].ID
			detail.Items[len(detail.Items)-2].TurnID = "active"
			v.Model.Details["s"] = detail
			ui.Frame()
			ui.Advance(time.Second)
			if collapsed {
				feedbackClick(t, ui, "1 次工具调用")
				ui.Advance(time.Second)
				v.lists["s"].ScrollToEnd()
				ui.Frame()
			}
			ui.SetDark(!eventFirst)
			feedbackClick(t, ui, "消息输入")
			ui.Type(strings.TrimSpace(strings.Repeat("追加一条关于当前回复的指令。\n", 5)))
			ui.Advance(time.Second)
			before := v.historyRowBounds[replyID]
			outgoing, hasOutgoing := ui.Find("工具组运行状态")
			if before.H == 0 || v.runningGroup == "" || !hasOutgoing {
				t.Fatal("fixture needs a visible reply and a live tool group")
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("prose-tool-%v-%v-000.png", eventFirst, collapsed))
			ui.Key(n.Cmd, n.KeyEnter)
			job := feedbackJob(t, v, "turn.steer")
			in := job.Input.(protocol.SteerInput).Input
			id := "input:" + in.OperationID
			op := domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationConfirmed}
			body, _ := json.Marshal(op)
			sentAt := ui.Now()
			item := domain.Item{ID: id, TurnID: "active", Kind: "user", Text: in.Text, Status: "sent", SentAt: &sentAt, Final: true}
			receipt := func() { job.Apply(v.Model, body, nil) }
			event := func() {
				v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "s", Operation: &op, Item: &item})
			}
			first, second, eventFrame := receipt, event, 12
			if eventFirst {
				first, second, eventFrame = event, receipt, 5
			}
			previous := before
			movingFrames := 0
			for frame := range 45 {
				switch frame {
				case 5:
					first()
					ui.Frame()
				case 12:
					second()
					ui.Frame()
				}
				reply := v.historyRowBounds[replyID]
				input := v.historyRowBounds[id]
				summary := v.historyRowBounds["reply-before:"+id]
				dock, _ := ui.Find("会话底部面板")
				if os.Getenv("SHIOJI_STEER_FOOTER_RECORDING") != "" {
					t.Logf("frame=%02d elapsed=%6.1f replyY=%6.1f inputY=%6.1f inputH=%5.1f summaryH=%5.1f dockY=%6.1f", frame, float64(ui.Now().Sub(sentAt))/float64(time.Millisecond), reply.Y, input.Y, input.H, summary.H, dock.Y)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("prose-tool-%v-%v-%03d.png", eventFirst, collapsed, frame+1))
				if frame < eventFrame {
					if input.H != 0 || math.Abs(float64(reply.Y-before.Y)) > 1 {
						t.Errorf("frame %d: pending operation moved the reply or mounted the input: reply Y %g → %g, input H=%g", frame, before.Y, reply.Y, input.H)
					}
				} else if frame == eventFrame {
					if math.Abs(float64(reply.Y-before.Y)) > 1 {
						t.Errorf("frame %d: the insertion reserved its space in one frame: reply Y %g → %g", frame, before.Y, reply.Y)
					}
					status, visible := ui.Find("工具组运行状态")
					if !visible || math.Abs(float64(status.Y-outgoing.Y)) > 1 {
						t.Errorf("old running indicator did not begin its fade in place: before=%+v after=%+v", outgoing, status)
					}
					if _, visible := ui.Find("用户消息操作"); visible {
						t.Error("message actions appeared at the destination before the bubble")
					}
				} else {
					if reply.H == 0 || reply.Y > previous.Y+1 || reply.Y < previous.Y-28 {
						t.Errorf("frame %d: previous reply did not move smoothly upward: Y %g → %g", frame, previous.Y, reply.Y)
					}
					if reply.Y < previous.Y-.5 {
						movingFrames++
					}
				}
				previous = reply
				ui.Advance(time.Second / 60)
			}
			if input := v.historyRowBounds[id]; input.H == 0 || movingFrames < 5 || !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() {
				t.Errorf("insertion did not expand and settle at the measured end: %d moving frames, input=%+v", movingFrames, input)
			}
			if _, visible := ui.Find("工具组运行状态"); visible || !ui.HasText("复制回复") {
				t.Error("old running indicator did not give way to the reply actions")
			}
			if _, visible := ui.Find("用户消息操作"); !visible {
				t.Error("message actions did not appear after the bubble landed")
			}
		})
	}
}

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
