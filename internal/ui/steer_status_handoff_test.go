package ui

import (
	"encoding/json"
	"fmt"
	"image"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestSteerConfirmationRetainsThePaintedStatusRow(t *testing.T) {
	for _, eventFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("event-first=", eventFirst), func(t *testing.T) {
			v, ui := nativeQueueFixture()
			ui.SetDark(eventFirst)
			detail := v.Model.Details["s"]
			started := ui.Now().Add(-time.Minute)
			detail.Turns[0].StartedAt = &started
			v.Model.Details["s"] = detail
			ui.Frame()
			feedbackClick(t, ui, "消息输入")
			ui.Type("追加一项检查")
			ui.Key(n.Cmd, n.KeyEnter)
			job := feedbackJob(t, v, "turn.steer")
			input := job.Input.(protocol.SteerInput).Input
			ui.Advance(600 * time.Millisecond)

			snapshot := func(stage string) {
				saveNativeSkillImage(t, ui, fmt.Sprintf("event-first-%v-%s.png", eventFirst, stage))
			}
			snapshot("sending")
			footer, shown := ui.Find("回复信息")
			pending, found := ui.Find("正在发送")
			clockBefore, timed := ui.Find("运行时长")
			if !shown || !found || !timed {
				t.Fatal("fixture needs a visible sending caption and clock", ui.Texts())
			}
			count := 0
			for _, label := range ui.Texts() {
				if label == "正在发送" {
					count++
				}
			}
			if count != 1 || pending.Y < footer.Y || pending.Y+pending.H > footer.Y+footer.H {
				t.Fatalf("pending insertion needs one sending caption inside its footer: count=%d caption=%+v footer=%+v", count, pending, footer)
			}
			// Exclude the dot and clock, which may change when the Worker accepts
			// the input. The text itself must still paint on the first frame.
			area := image.Rect(int(pending.X), int(pending.Y), int(pending.X+pending.W), int(pending.Y+pending.H))
			inkBefore := homeStatusInk(ui.Image(), area)
			if inkBefore == 0 {
				t.Fatal("sending caption is not painted")
			}

			receivedAt := ui.Now()
			item := domain.Item{ID: "input:" + input.OperationID, TurnID: "active", Kind: "user", Text: input.Text, Status: "sent", Final: true, SentAt: &receivedAt}
			body, _ := json.Marshal(domain.Operation{ID: input.OperationID, SessionID: "s", State: domain.OperationConfirmed})
			receipt := func() { job.Apply(v.Model, body, nil) }
			event := func() {
				detail := v.Model.Details["s"]
				detail.Items = append(detail.Items, item)
				v.Model.Details["s"] = detail
			}
			first, second := receipt, event
			if eventFirst {
				first, second = event, receipt
			}
			first()
			ui.Frame()
			snapshot("first-confirmation")
			if firstInk := homeStatusInk(ui.Image(), area); firstInk < inkBefore*8/10 {
				t.Errorf("caption flashed on confirmation: ink before=%d after=%d", inkBefore, firstInk)
			}
			second()
			ui.Frame()
			for i := range 12 {
				after, shown := ui.Find("回复信息")
				running, found := ui.Find("进行中")
				clock, timed := ui.Find("运行时长")
				if !shown || !found || !timed || math.Abs(float64(footer.Y-after.Y)) > 1 || math.Abs(float64(clockBefore.Y-clock.Y)) > 1 || running.Y < after.Y || running.Y+running.H > after.Y+after.H {
					t.Fatalf("frame %d moved or hid the existing footer: before=%+v after=%+v clock=%+v → %+v", i, footer, after, clockBefore, clock)
				}
				if ink := homeStatusInk(ui.Image(), area); ink < inkBefore/4 {
					t.Fatalf("frame %d cleared the status caption: ink=%d before=%d", i, ink, inkBefore)
				}
				ui.Advance(time.Second / 60)
			}
			ui.Advance(250 * time.Millisecond)
			snapshot("running")
		})
	}
}
