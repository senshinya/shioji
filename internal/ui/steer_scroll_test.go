package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func steerScrollFixture(expanded, tools, reduced bool) (*View, *n.Tester) {
	v, ui := nativeQueueFixture()
	ui.SetDark(true)
	ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
	d := v.Model.Details["s"]
	d.Items = nil
	for i := 0; i < 28; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("history-", i), Kind: "assistant", Text: fmt.Sprint("历史段落 ", i, "。", strings.Repeat("保留既有阅读内容。", 8)), Final: true})
	}
	if tools {
		d.Items = append(d.Items, domain.Item{ID: "tool", TurnID: "active", Kind: "tool", Presentation: "command", Name: "正在执行的命令", Text: strings.Repeat("工具输出\n", 8), Status: "running"})
	}
	for i := len(d.Queue.Items); i < 8; i++ {
		d.Queue.Items = append(d.Queue.Items, domain.QueueItem{ID: fmt.Sprint("queue-", i), Input: domain.Submission{OperationID: fmt.Sprint("dispatch-", i), Text: fmt.Sprint("待发送消息 ", i+1)}})
	}
	v.Model.Details["s"] = d
	v.Model.QueueExpanded["s"] = expanded
	ui.Frame()
	ui.Advance(time.Second)
	return v, ui
}

func TestSteerSettlesAtNaturalEndBeforeAnyManualScroll(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		for _, tools := range []bool{false, true} {
			for _, eventFirst := range []bool{false, true} {
				for _, reduced := range []bool{false, true} {
					t.Run(fmt.Sprintf("queue=%v/tools=%v/event-first=%v/reduced=%v", expanded, tools, eventFirst, reduced), func(t *testing.T) {
						v, ui := steerScrollFixture(expanded, tools, reduced)
						record := os.Getenv("SHIOJI_STEER_SCROLL_MOTION") != "" && expanded && tools && !eventFirst && !reduced
						frame := 0
						advance := func(frames int) {
							if !record {
								ui.Advance(time.Duration(frames) * time.Second / 60)
								return
							}
							for i := 0; i < frames; i++ {
								saveNativeSkillImage(t, ui, fmt.Sprintf("steer-frame-%03d.png", frame))
								frame++
								ui.Advance(time.Second / 60)
							}
						}
						ui.Click("消息输入")
						ui.Type("进行中插入的消息")
						ui.Key(n.Cmd, n.KeyEnter)
						var job app.Job
						for len(v.Controller.Jobs) > 0 {
							current := <-v.Controller.Jobs
							if current.Method == "turn.steer" {
								job = current
							}
						}
						if job.Apply == nil {
							t.Fatal("missing real steer command")
						}
						in := job.Input.(protocol.SteerInput).Input
						op := domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"}
						body, _ := json.Marshal(op)
						item := domain.Item{ID: "input:" + in.OperationID, TurnID: "active", Kind: "user", Text: in.Text, Status: "sent", Final: true}
						receipt := func() { job.Apply(v.Model, body, nil); ui.Frame() }
						event := func() {
							v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "s", Operation: &op, Item: &item})
							ui.Frame()
						}
						if eventFirst {
							event()
							receipt()
						} else {
							receipt()
							event()
						}
						advance(60)
						footer, ok := ui.Find("回复信息")
						message, messageOK := ui.Find("进行中插入的消息")
						dock, dockOK := ui.Find("会话底部面板")
						if !ok || !messageOK || !dockOK || footer.Y+footer.H > dock.Y {
							t.Fatal("inserted message or latest progress is hidden", message, footer, dock)
						}
						if !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() {
							t.Error("steer does not resume following the measured content end")
						}
						if expanded && tools && !eventFirst && !reduced {
							saveNativeSkillImage(t, ui, "steer-settled.png")
						}
						ui.Scroll(800, 300, 0, -1)
						ui.Frame()
						advance(24)
						after, ok := ui.Find("回复信息")
						if !ok || math.Abs(float64(after.Y-footer.Y)) > 2 {
							t.Errorf("one-pixel wheel movement repairs a stale send anchor: before=%+v after=%+v dock=%+v", footer, after, dock)
						}
						if expanded && tools && !eventFirst && !reduced {
							saveNativeSkillImage(t, ui, "steer-after-wheel.png")
						}
					})
				}
			}
		}
	}
}

func TestImmediateQueuedSteerSettlesAtNaturalEnd(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		v, ui := steerScrollFixture(true, true, reduced)
		if err := ui.Click("立即发送待发消息 1"); err != nil {
			t.Fatal(err)
		}
		ui.Frame()
		if reduced && !v.lists["s"].AtEnd() {
			t.Fatal("reduced-motion queue insertion did not reach the end immediately")
		}
		var send app.Job
		for len(v.Controller.Jobs) > 0 {
			job := <-v.Controller.Jobs
			if job.Method == "queue.send" {
				send = job
			}
		}
		if send.Apply == nil || send.Input.(protocol.QueueInput).TurnID != "active" {
			t.Fatal("queue action did not target the active turn")
		}
		q := v.Model.Queue()
		input := q.Items[0].Input
		d := v.Model.Details["s"]
		d.Items = append(d.Items, domain.Item{ID: "input:" + input.OperationID, TurnID: "active", Kind: "user", Text: input.Text, Status: "sent", Final: true})
		v.Model.Details["s"] = d
		q.Items = q.Items[1:]
		q.Revision++
		body, _ := json.Marshal(q)
		send.Apply(v.Model, body, nil)
		ui.Frame()
		ui.Advance(time.Second)
		footer, ok := ui.Find("回复信息")
		if !ok {
			t.Fatal("queued steer lost the live footer")
		}
		ui.Scroll(800, 300, 0, -1)
		ui.Frame()
		ui.Advance(400 * time.Millisecond)
		after, ok := ui.Find("回复信息")
		if !ok || math.Abs(float64(after.Y-footer.Y)) > 2 {
			t.Error("queued steer needs a manual scroll to reach its stable position", footer, after)
		}
	}
}

func TestSteerKeepsExpandedToolGroupThroughSend(t *testing.T) {
	for _, theme := range []struct {
		name          string
		dark, reduced bool
	}{{"light", false, false}, {"dark", true, false}, {"reduced", false, true}} {
		t.Run(theme.name, func(t *testing.T) {
			v, ui := steerScrollFixture(false, true, theme.reduced)
			ui.SetDark(theme.dark)
			record := os.Getenv("SHIOJI_STEER_SCROLL_MOTION") != "" && !theme.reduced
			capture := func(frame int) {
				if record {
					saveNativeSkillImage(t, ui, fmt.Sprintf("steer-insert-%s-%03d.png", theme.name, frame))
				}
			}
			// Close and reopen via the heading to exercise a real reader choice.
			for range 2 {
				feedbackClick(t, ui, "1 次工具调用")
				ui.Advance(250 * time.Millisecond)
			}
			emptyDock, _ := ui.Find("会话底部面板")
			feedbackClick(t, ui, "消息输入")
			message := "继续检查这组工具的输出"
			if theme.reduced {
				message = strings.TrimSpace(strings.Repeat(message+"\n", 4))
			}
			ui.Type(message)
			ui.Advance(time.Second)
			tool, ok := ui.Find("正在执行的命令")
			if !ok {
				t.Fatal("tool group was not expanded before sending")
			}
			capture(0)

			ui.Key(n.Cmd, n.KeyEnter)
			if theme.reduced {
				dock, _ := ui.Find("会话底部面板")
				if dock.H > emptyDock.H+1 {
					t.Errorf("first insertion frame still reserves the multiline draft: dock=%+v empty=%+v", dock, emptyDock)
				}
			}
			job := feedbackJob(t, v, "turn.steer")
			in := job.Input.(protocol.SteerInput).Input
			op := domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationConfirmed}
			item := domain.Item{ID: "input:" + in.OperationID, TurnID: "active", Kind: "user", Text: in.Text, Status: "sent", Final: true}
			body, _ := json.Marshal(op)
			// Leave time for the optimistic row to render before the worker
			// confirms the insertion. Both stages must preserve the group.
			previousY := tool.Y
			missing, reversed := false, false
			for frame := range 36 {
				if frame == 6 {
					job.Apply(v.Model, body, nil)
					v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "s", Operation: &op, Item: &item})
				}
				ui.Advance(time.Second / 60)
				if frame == 0 && theme.reduced {
					footer, visible := ui.Find("回复信息")
					dock, dockOK := ui.Find("会话底部面板")
					if !v.lists["s"].AtEnd() || !visible || !dockOK || footer.Y+footer.H > dock.Y {
						t.Errorf("reduced-motion insertion did not reach the end immediately: footer=%+v dock=%+v", footer, dock)
					}
				}
				current, visible := ui.Find("正在执行的命令")
				missing = missing || !visible
				reversed = reversed || visible && current.Y > previousY+1
				if visible {
					previousY = current.Y
				}
				capture(frame + 1)
			}
			if missing {
				t.Error("expanded tool row disappeared during insertion")
			}
			if reversed {
				t.Error("preceding tool row moved down during insertion")
			}
			bubble := v.historyRowBounds[item.ID]
			dock, dockOK := ui.Find("会话底部面板")
			if !dockOK || bubble.H <= 0 || bubble.Y+bubble.H > dock.Y || !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() {
				t.Errorf("inserted message did not settle above the dock: message=%+v dock=%+v", bubble, dock)
			}
		})
	}
}

func TestSteerScrollDoesNotReverseAsMessageEnters(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			checkMultilineSteerScroll(t, dark)
		})
	}
}

func checkMultilineSteerScroll(t *testing.T, dark bool) {
	t.Helper()
	v, ui := steerScrollFixture(true, false, false)
	ui.SetDark(dark)
	d := v.Model.Details["s"]
	reply := &d.Items[len(d.Items)-1]
	reply.TurnID = "active"
	replyID := reply.ID
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)

	feedbackClick(t, ui, "消息输入")
	ui.Type(strings.TrimSpace(strings.Repeat("追加一条关于当前回复的指令。\n", 7)))
	ui.Advance(time.Second)
	start := v.historyRowBounds[replyID]
	ui.Key(n.Cmd, n.KeyEnter)
	job := feedbackJob(t, v, "turn.steer")
	in := job.Input.(protocol.SteerInput).Input
	ui.Frame()
	op := domain.Operation{ID: in.OperationID, SessionID: "s", State: domain.OperationConfirmed}
	body, _ := json.Marshal(op)
	job.Apply(v.Model, body, nil)
	item := domain.Item{ID: "input:" + in.OperationID, TurnID: "active", Kind: "user", Text: in.Text, Status: "sent", Final: true}
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "submission", SessionID: "s", Operation: &op, Item: &item})

	previous := start
	ui.Frame()
	for frame := range 60 {
		current := v.historyRowBounds[replyID]
		if current.H <= 0 || current.Y > previous.Y+1 {
			t.Errorf("frame %d: preceding reply moved down during insertion: Y=%g → %g", frame, previous.Y, current.Y)
		}
		if current.Y < previous.Y-28 {
			t.Errorf("frame %d: preceding reply jumped during insertion: Y=%g → %g", frame, previous.Y, current.Y)
		}
		saveNativeSkillImage(t, ui, fmt.Sprintf("steer-handoff-%v-%03d.png", dark, frame))
		previous = current
		ui.Advance(time.Second / 60)
	}
	dock, _ := ui.Find("会话底部面板")
	message := v.historyRowBounds[item.ID]
	if start.Y-previous.Y < 32 || message.H <= 0 || message.Y+message.H > dock.Y || !v.lists["s"].FollowingEnd() {
		t.Fatalf("insertion did not scroll the conversation to its end: Y=%g → %g", start.Y, previous.Y)
	}
}
