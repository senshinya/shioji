package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestExistingSessionSendSettlesAtNaturalEnd(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, eventFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("reduced=%v/event-first=%v", reduced, eventFirst), func(t *testing.T) {
				v, ui, job := sendFixture(t, false)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				d := v.Model.Details["s"]
				d.Items = nil
				for i := 0; i < 28; i++ {
					d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("earlier-", i), Kind: "assistant", Text: strings.Repeat("Earlier conversation. ", 8), Final: true})
				}
				v.Model.Details["s"] = d
				ui.Frame()
				ui.Advance(time.Second)
				if eventFirst {
					in := job.Input.(domain.Submission)
					op := domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"}
					v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "s", Operation: &op, Item: &domain.Item{ID: "input:" + in.OperationID, TurnID: "new", Kind: "user", Text: in.Text, Final: true}})
					ui.Frame()
					ui.Advance(700 * time.Millisecond)
					acceptSend(t, v, job, false)
				} else {
					acceptSend(t, v, job, true)
				}
				ui.Frame()
				ui.Advance(time.Second)
				message, ok := ui.Find("Explain a tree rotation.")
				dock, _ := ui.Find("会话底部面板")
				saveNativeSkillImage(t, ui, fmt.Sprintf("existing-send-reduced-%v-event-first-%v.png", reduced, eventFirst))
				if !ok || message.Y < 200 || message.Y+message.H > dock.Y || !v.lists["s"].AtEnd() {
					t.Fatalf("existing conversation retains artificial top space: message=%+v dock=%+v", message, dock)
				}
				ui.Scroll(800, 350, 0, -1)
				ui.Frame()
				ui.Advance(400 * time.Millisecond)
				after, ok := ui.Find("Explain a tree rotation.")
				if !ok || math.Abs(float64(after.Y-message.Y)) > 2 {
					t.Fatal("one-pixel scroll repairs an artificial send anchor", message, after)
				}
			})
		}
	}
}
