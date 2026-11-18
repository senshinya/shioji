package ui

import (
	"errors"
	"image"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestSessionStopShowsStoppedBeforeWorkerReceipt(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		name := "confirmed"
		if rejected {
			name = "rejected"
		}
		t.Run(name, func(t *testing.T) {
			m := layoutModel()
			m.Readiness.Capabilities.Interrupt = true
			detail := m.Details["s"]
			detail.Session.Status = domain.ExecutionRunning
			detail.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning}}
			detail.Items = []domain.Item{
				{ID: "prompt", TurnID: "t", Kind: "user", Text: "Check the current result", Final: true},
				{ID: "reply", TurnID: "t", Kind: "assistant", Text: "Partial reply"},
			}
			m.Details["s"] = detail
			m.Snapshot.Sessions[0] = detail.Session
			v := New(m, app.NewController(""), "")
			ui := n.NewTester(v.View, 1280, 800)
			ui.Advance(time.Second)

			button, _ := ui.Find("停止当前轮")
			footer, ok := ui.Find("回复信息")
			if !ok {
				t.Fatal("test turn has no reply footer")
			}
			runningInk := func() bool {
				img := ui.Image()
				for y := int(footer.Y); y < int(footer.Y+footer.H); y++ {
					for x := int(footer.X); x < int(footer.X+footer.W); x++ {
						pixel := img.RGBAAt(x, y)
						if int(pixel.G) > int(pixel.R)+10 && int(pixel.G) > int(pixel.B)+10 {
							return true
						}
					}
				}
				return false
			}
			if !runningInk() {
				t.Fatal("test turn has no painted running indicator")
			}
			if err := ui.Click("停止当前轮"); err != nil {
				t.Fatal(err)
			}
			job := historyJob(t, v, "turn.interrupt")
			if in := job.Input.(map[string]string); in["sessionId"] != "s" || in["turnId"] != "t" {
				t.Fatal("Stop targeted the wrong turn", in)
			}
			saveNativeSkillImage(t, ui, "stop-before-receipt-"+name+".png")
			if _, shown := ui.Find("回复状态：已停止"); !shown || ui.HasText("正在停止") {
				t.Fatal("click did not show the stopped reply immediately", ui.Texts())
			}
			if _, send := ui.Find("发送"); !send {
				t.Fatal("click did not restore the send control immediately", ui.Texts())
			}
			if runningInk() {
				t.Fatal("the previous running indicator still paints over the stopped footer")
			}
			if _, running := ui.Find("会话运行状态：s"); running || !m.Active() {
				t.Fatal("Stop should end the running indicator without changing execution state")
			}
			ui.ClickAt(button.X+button.W/2, button.Y+button.H/2)
			for len(v.Controller.Jobs) > 0 {
				if next := <-v.Controller.Jobs; next.Method == "turn.interrupt" {
					t.Fatal("repeated click dispatched a second interrupt")
				}
			}
			detail.Items[1].Text = "Partial reply with a late delta"
			m.Details["s"] = detail
			ui.Frame()
			ui.Advance(time.Second)
			if !ui.HasText("Partial reply") || ui.HasText("Partial reply with a late delta") {
				t.Fatal("late output streamed during Stop", ui.Texts())
			}
			if rejected {
				job.Apply(m, nil, errors.New("stop rejected"))
			} else {
				job.Apply(m, nil, nil)
				ui.Frame()
				if _, shown := ui.Find("回复状态：已停止"); !shown || !m.Active() {
					t.Fatal("request acceptance changed the stopped presentation or execution state", ui.Texts())
				}
				detail.Session.Status = domain.ExecutionInterrupted
				detail.Turns[0].Status = domain.ExecutionInterrupted
				detail.Items[1].Text += " and final output"
				detail.Items[1].Final = true
				m.Snapshot.Sessions[0] = detail.Session
				m.Details["s"] = detail
			}
			ui.Frame()
			if ui.HasText("正在停止") || !ui.HasText("Partial reply with a late delta") {
				t.Fatal("the authoritative reply did not return after Stop settled", ui.Texts())
			}
			if rejected {
				if _, running := ui.Find("会话运行状态：s"); !running || !m.Active() {
					t.Fatal("rejected Stop failed to restore the running presentation")
				}
			} else if !ui.HasText("已停止") || !ui.HasText("and final output") {
				t.Fatal("the interrupted result and last output did not appear together", ui.Texts())
			}
		})
	}
}

func TestStopReceiptsDoNotRepaintStoppedTurn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		turnFirst bool
		lateError bool
	}{
		{name: "turn-first", turnFirst: true},
		{name: "session-first"},
		{name: "turn-before-error", turnFirst: true, lateError: true},
		{name: "session-before-error", lateError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := liveCompletionModel()
			m := v.Model
			v.Controller = app.NewController("")
			m.Readiness.Capabilities.Interrupt = true
			detail := m.Details["s"]
			detail.Turns = append([]domain.Turn{{ID: "old", Status: domain.ExecutionCompleted}}, detail.Turns...)
			detail.Items = append(detail.Items, domain.Item{ID: "old-tail", TurnID: "old", Kind: "assistant", Text: "An earlier turn's late output", Final: true})
			m.Details["s"] = detail
			ui := n.NewTester(v.View, 1280, 800)
			ui.Advance(time.Second)
			if err := ui.Click("停止当前轮"); err != nil {
				t.Fatal(err)
			}
			job := historyJob(t, v, "turn.interrupt")
			ui.Advance(time.Second)
			stoppedFrame := copyFrame(ui)
			checkFrame := func(stage string) {
				t.Helper()
				ui.Frame()
				saveNativeSkillImage(t, ui, tc.name+"-"+stage+".png")
				if _, ok := ui.Find("回复状态：已停止"); !ok || ui.HasText("停止当前轮") {
					t.Fatalf("%s reopened the stopped turn: %v", stage, ui.Texts())
				}
				if changed := differentPixels(stoppedFrame, ui.Image(), stoppedFrame.Bounds()); changed != 0 {
					t.Fatalf("%s repainted %d pixels without changing visible data", stage, changed)
				}
			}
			apply := func(e domain.Event) {
				t.Helper()
				e.SessionID, e.Sequence = "s", m.Sequence+1
				if err := m.ApplyEvent(e); err != nil {
					t.Fatal(err)
				}
			}
			turn := detail.Turns[1]
			turn.Status = domain.ExecutionInterrupted
			session := detail.Session
			session.Status = domain.ExecutionInterrupted
			session.Revision++
			if !tc.turnFirst && !tc.lateError {
				job.Apply(m, nil, nil)
				checkFrame("ack")
			}
			events := []domain.Event{{Type: "session", Session: &session}, {Type: "turn", Turn: &turn}}
			if tc.turnFirst {
				events[0], events[1] = events[1], events[0]
			}
			for i, event := range events {
				apply(event)
				checkFrame(event.Type)
				if i == 0 && tc.lateError {
					job.Apply(m, nil, errors.New("turn is not active"))
					checkFrame("late-error")
				}
			}
			if tc.turnFirst && !tc.lateError {
				job.Apply(m, nil, nil)
				checkFrame("ack")
			}

			late := detail.Items[1]
			late.Text, late.Final = "Prefix. Late final text.", true
			in, duration, ended := int64(123), int64(12000), time.Date(2026, 11, 19, 12, 34, 0, 0, time.Local)
			turn.InputTokens, turn.DurationMS, turn.CompletedAt = &in, &duration, &ended
			apply(domain.Event{Type: "item.upsert", Item: &late})
			apply(domain.Event{Type: "turn", Turn: &turn})
			ui.Frame()
			if !ui.HasText(late.Text) || !ui.HasText("输入 123") {
				t.Fatal("final text or metrics are missing", ui.Texts())
			}
			first := copyFrame(ui)
			ui.Advance(500 * time.Millisecond)
			if differentPixels(first, ui.Image(), image.Rect(270, 60, 1250, 580)) != 0 {
				t.Fatal("late output or metadata restarted the reply animation")
			}
		})
	}
}
