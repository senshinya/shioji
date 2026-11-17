package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// A receipt may overtake the event stream or arrive after it. Neither ordering
// may remount the already visible status or restart the departing message.
func TestSendReceiptHandoffKeepsNativeRows(t *testing.T) {
	for _, home := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			for _, reduced := range []bool{false, true} {
				for _, delay := range []time.Duration{0, 100 * time.Millisecond, 300 * time.Millisecond, 900 * time.Millisecond} {
					for _, eventFirst := range []bool{false, true} {
						name := fmt.Sprintf("home=%v/dark=%v/reduced=%v/delay=%s/event-first=%v", home, dark, reduced, delay, eventFirst)
						t.Run(name, func(t *testing.T) {
							v, ui := readySendFixture(t, home)
							ui.SetDark(dark)
							ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
							job := sendFromFixture(t, v, ui)
							ui.Advance(delay)
							in := job.Input.(domain.Submission)
							id := in.SessionID
							if id == "" {
								id = "created"
							}
							session := domain.Session{ID: id, WorkspaceID: in.WorkspaceID, Status: domain.ExecutionRunning, Config: in.Config}
							ack, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: id, State: domain.OperationAccepted})
							accept := func() {
								if home && !eventFirst {
									v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, session)
								}
								job.Apply(v.Model, ack, nil)
								ui.Frame()
							}
							if !eventFirst {
								accept()
							}
							before, _ := ui.Find("用户消息气泡")
							composer, _ := ui.Find("消息编辑器")
							copyBefore, _ := ui.Find("复制消息")
							editBefore, _ := ui.Find("修改并重新开始")
							motion := v.sent
							var started time.Time
							if motion != nil {
								started = motion.started
							}
							at := v.homeSend.sentAt.Add(time.Minute)
							item := domain.Item{ID: "input:" + in.OperationID, TurnID: "next-turn", Kind: "user", Text: in.Text, Status: "sent", SentAt: &at, Final: true}
							op := domain.Operation{ID: in.OperationID, SessionID: id, State: domain.OperationConfirmed}
							turn := domain.Turn{ID: "next-turn", Status: domain.ExecutionRunning}
							if home {
								// The coherent history load may already have established the
								// new detail before its live confirmation is delivered.
								v.Model.Details[id] = domain.SessionDetail{Session: session}
							}
							if err := v.Model.ApplyEvent(domain.Event{Sequence: v.Model.Sequence + 1, Type: "submission", SessionID: id, Session: &session, Item: &item, Turn: &turn, Operation: &op}); err != nil {
								t.Fatal(err)
							}
							ui.Frame()
							after, ok := ui.Find("用户消息气泡")
							nextComposer, _ := ui.Find("消息编辑器")
							copyAfter, _ := ui.Find("复制消息")
							editAfter, _ := ui.Find("修改并重新开始")
							if !ok || math.Abs(float64(after.Y-before.Y)) > 1 || math.Abs(float64(after.X-before.X)) > 1 || nextComposer != composer {
								t.Fatal("authoritative event moved the message or composer at the same animation time", before, after, composer, nextComposer)
							}
							if copyAfter != copyBefore || editAfter != editBefore {
								t.Fatal("receipt moved or reinserted the message action slots", copyBefore, copyAfter, editBefore, editAfter)
							}
							if key := v.toolArrivals.footerKeys["next-turn"]; key != "home-send-status:"+in.OperationID {
								t.Fatal("receipt remounted the visible waiting status", key)
							}
							if v.sent != nil && (v.sent != motion || v.sent.started != started) || delay < sentScrollDuration && v.sent == nil {
								t.Fatal("event restarted the send animation")
							}
							if eventFirst {
								accept()
								late, _ := ui.Find("用户消息气泡")
								if late != after {
									t.Fatal("late command receipt moved the authoritative message", after, late)
								}
							}
							for range 30 {
								ui.Advance(time.Second / 60)
								if !ui.HasText("进行中") || v.composerDocuments[v.Model.DraftKey()].doc.Text().String() != "" {
									t.Fatal("receipt caused a blank status or restored outgoing text")
								}
							}
						})
					}
				}
			}
		}
	}
}

func TestHomeConfirmationBeforeHistoryKeepsTheMovingPreview(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	ui.Advance(100 * time.Millisecond)
	in := job.Input.(domain.Submission)
	before, _ := ui.Find("用户消息气泡")
	session := domain.Session{ID: "created", WorkspaceID: in.WorkspaceID, Status: domain.ExecutionRunning, Config: in.Config}
	op := domain.Operation{ID: in.OperationID, SessionID: session.ID, State: domain.OperationConfirmed}
	if err := v.Model.ApplyEvent(domain.Event{Sequence: v.Model.Sequence + 1, Type: "submission", SessionID: session.ID, Session: &session, Operation: &op}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	after, ok := ui.Find("用户消息气泡")
	if !ok || after != before || !ui.HasText("进行中") {
		t.Fatal("confirmation removed the preview before history arrived", before, after)
	}
	ui.Advance(200 * time.Millisecond)
	before, _ = ui.Find("用户消息气泡")
	item := domain.Item{ID: "input:" + in.OperationID, TurnID: "next-turn", Kind: "user", Text: in.Text, Final: true}
	v.Model.Details[session.ID] = domain.SessionDetail{Session: session, Items: []domain.Item{item}, Turns: []domain.Turn{{ID: "next-turn", Status: domain.ExecutionRunning}}}
	ui.Frame()
	after, ok = ui.Find("用户消息气泡")
	if !ok || after != before {
		t.Fatal("history loading restarted the moving preview", before, after)
	}
}
