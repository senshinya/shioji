package ui

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/app"
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

func TestSendingAtBottomDoesNotRewindTheVisibleHistory(t *testing.T) {
	for _, count := range []int{28, 240, 1200} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			const marker = "发送前一直可见的最后一段回复。"
			v, ui, job := sendFixture(t, false, func(m *app.Model) {
				d := m.Details["s"]
				d.Items = nil
				for i := 0; i < count; i++ {
					d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("past-", i), Kind: "assistant", Text: fmt.Sprintf("历史回复 %d。%s", i, strings.Repeat("不同长度的原生回复内容。", 4+i%9)), Final: true})
				}
				d.Items = append(d.Items, domain.Item{ID: "stable-tail", Kind: "assistant", Text: marker, Final: true})
				m.Details["s"] = d
			})
			ui.SetDark(false)
			ui.Advance(time.Second)
			state := v.lists["s"]
			before, ok := ui.Find(marker)
			if !ok || !state.AtEnd() || !state.FollowingEnd() {
				t.Fatal("fixture is not settled at bottom", before, state.AtEnd(), state.FollowingEnd())
			}
			if dir := os.Getenv("SHIOJI_SEND_SCROLL_EVIDENCE"); dir != "" && count == 240 {
				os.MkdirAll(dir, 0700)
				f, e := os.Create(filepath.Join(dir, "initial.png"))
				if e != nil {
					t.Fatal(e)
				}
				e = png.Encode(f, ui.Image())
				f.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			acceptSend(t, v, job, true)
			worst := float32(0)
			for frame := 0; frame < 50; frame++ {
				ui.Frame()
				now, visible := ui.Find(marker)
				if !visible {
					t.Errorf("frame %d rewound so far that the previously visible tail disappeared", frame)
					break
				}
				worst = max(worst, now.Y-before.Y)
				if dir := os.Getenv("SHIOJI_SEND_SCROLL_EVIDENCE"); dir != "" && count == 240 {
					os.MkdirAll(dir, 0700)
					f, e := os.Create(filepath.Join(dir, fmt.Sprintf("frame-%03d.png", frame)))
					if e != nil {
						t.Fatal(e)
					}
					e = png.Encode(f, ui.Image())
					f.Close()
					if e != nil {
						t.Fatal(e)
					}
				}
				ui.Advance(time.Second / 60)
			}
			if worst > 2 {
				t.Errorf("sending at the bottom moved existing content down before returning: %g DIP", worst)
			}
			if !state.AtEnd() || !state.FollowingEnd() {
				t.Error("send did not settle at the natural bottom")
			}
		})
	}
}
