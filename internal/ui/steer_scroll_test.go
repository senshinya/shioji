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
