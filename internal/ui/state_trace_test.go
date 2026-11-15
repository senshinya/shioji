package ui

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"image"
	"image/color"
	"math"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/testutil"
	"testing"
	"time"
)

func TestStateTraceConversation(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("reduce=%v/dark=%v", reduced, dark), func(t *testing.T) {
				m := layoutModel()
				m.Drafts["session:s"] = "ordinary draft"
				d := m.Details["s"]
				d.Items = []domain.Item{{ID: "u", Kind: "user", Text: "Explain rotation", Status: "sent", Final: true}, {ID: "tool", Kind: "tool", Text: "rotation output", Name: "Run command", Final: true}, {ID: "a", Kind: "assistant", Text: "A **rotation** changes local pointers.\n\n```go\nrotate(root)\n```", Final: true}}
				m.Details["s"] = d
				v := New(m, nil, "")
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				capture := func(step string) {
					geometry := map[string]n.Rect{}
					for _, name := range []string{"对话记录", "消息输入", "会话内查找", "待处理请求卡片", "请求内容区域", "待发送", "会话底部面板"} {
						if r, ok := ui.Find(name); ok {
							r.X = float32(math.Round(float64(r.X)*100) / 100)
							r.Y = float32(math.Round(float64(r.Y)*100) / 100)
							r.W = float32(math.Round(float64(r.W)*100) / 100)
							r.H = float32(math.Round(float64(r.H)*100) / 100)
							geometry[name] = r
						}
					}
					testutil.StateTrace(t, step, struct {
						Texts     []string
						Geometry  map[string]n.Rect
						Draft     string
						Focus     bool
						Pager     int
						Collapsed map[string]bool
					}{ui.Texts(), geometry, m.Drafts["session:s"], ui.Focused("消息输入"), m.Pager["s"], m.Collapsed})
				}
				ui.Advance(time.Second)
				capture("history")
				ui.Key(n.Cmd, n.KeyF)
				ui.Type("rotation")
				ui.Advance(600 * time.Millisecond)
				capture("find")
				ui.Advance(4 * time.Second)
				capture("find-held")
				ui.Key(0, n.KeyEnter)
				ui.Advance(300 * time.Millisecond)
				capture("find-next")
				ui.Key(0, n.KeyEscape)
				ui.Advance(250 * time.Millisecond)
				capture("find-closed")
				d.Requests = []domain.PendingRequest{{ID: "r", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "Continue the rotation?", Custom: true}}}}
				m.Details["s"] = d
				ui.Frame()
				for i := 0; i < 5; i++ {
					ui.Advance(80 * time.Millisecond)
					capture(fmt.Sprint("question-enter-", i))
				}
				d.Requests = append(d.Requests, domain.PendingRequest{ID: "r2", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q2", Text: "Another question?", Custom: true}}})
				m.Details["s"] = d
				ui.Frame()
				ui.Advance(400 * time.Millisecond)
				capture("question-appended")
				m.Collapsed["s"] = true
				ui.Frame()
				ui.Advance(400 * time.Millisecond)
				capture("question-collapsed")
				d.Requests = nil
				m.Details["s"] = d
				ui.Frame()
				ui.Advance(400 * time.Millisecond)
				capture("question-removed")
				d.Queue = domain.QueueState{SessionID: "s", Revision: 1, Items: []domain.QueueItem{{ID: "q", Input: domain.Submission{Text: "queued input"}}}}
				m.Details["s"] = d
				ui.Frame()
				ui.Advance(400 * time.Millisecond)
				capture("queue")
				if err := m.EditQueued("q"); err != nil {
					t.Fatal(err)
				}
				ui.Frame()
				ui.Advance(400 * time.Millisecond)
				capture("queue-edit")
				m.CancelQueueEdit("s")
				ui.Frame()
				ui.Advance(400 * time.Millisecond)
				capture("queue-cancel")
			})
		}
	}
}

// Capture every tested animation frame in memory. No screenshots or acceptance
// resources are written; these observations cover timing as well as end states.
func traceNativeFrame(t *testing.T, step string, v *View, ui *n.Tester) {
	geometry := map[string]n.Rect{}
	for _, name := range []string{"用户消息", "用户消息气泡", "历史消息编辑器", "对话记录", "消息输入", "待处理请求卡片", "请求内容区域", "回答结果", "待发送队列", "会话底部面板", "附件预览", "图片预览内容"} {
		if r, ok := ui.Find(name); ok {
			r.X = float32(math.Round(float64(r.X)*100) / 100)
			r.Y = float32(math.Round(float64(r.Y)*100) / 100)
			r.W = float32(math.Round(float64(r.W)*100) / 100)
			r.H = float32(math.Round(float64(r.H)*100) / 100)
			geometry[name] = r
		}
	}
	testutil.StateTrace(t, step, struct {
		Texts     []string
		Geometry  map[string]n.Rect
		Animating bool
		Draft     string
		Focus     bool
		Clipboard string
		Pixels    string
	}{ui.Texts(), geometry, v.visibleOutputAnimating(), v.Model.Drafts[v.Model.DraftKey()], ui.Focused("消息输入"), ui.Clipboard(), fmt.Sprintf("%x", sha256.Sum256(ui.Image().Pix))})
}

func TestStateTraceReplyReceiptFrames(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprint("reduced=", reduced), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = nil
			d.Session.Status = "running"
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)
			d.Items = []domain.Item{{ID: "receipt", Kind: "request_result", Text: "已提交回答", Final: true, Order: 1, Parts: []domain.ContentPart{{Text: "逐项检查"}}}, {ID: "live", Kind: "assistant", Text: replyBurst, Order: 2}}
			m.Details["s"] = d
			ui.Frame()
			for frame := 0; frame < 75; frame++ {
				if frame == 5 {
					d.Items[1].Text += "\n\n```go\nrotate(root)\n```"
					m.Details["s"] = d
					ui.Frame()
				}
				if frame == 10 {
					d.Items[1].Final = true
					m.Details["s"] = d
					ui.Frame()
				}
				ui.Advance(time.Second / 60)
				traceNativeFrame(t, fmt.Sprint("frame-", frame), v, ui)
			}
			// Reconnect must display saved content immediately, with no replay.
			m.ConnectionEpoch = "reconnected"
			ui.Frame()
			traceNativeFrame(t, "reconnect", v, ui)
		})
	}
}

func TestStateTraceHistoryExitFrames(t *testing.T) {
	for _, action := range []string{"restart", "fork"} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reduced=%v", action, reduced), func(t *testing.T) {
				v, ui, item := historyFixture()
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				ui.Advance(time.Second)
				if action == "restart" {
					v.beginHistoryEdit(item)
					ui.Frame()
					ui.Advance(400 * time.Millisecond)
					v.Model.Drafts["history:s:user"] = "revised question"
					v.restartHistory()
				} else {
					v.forkSession(v.Model.Details["s"].Session, "turn")
				}
				ui.Frame()
				for frame := 0; frame < 50; frame++ {
					ui.Advance(time.Second / 60)
					traceNativeFrame(t, fmt.Sprint("frame-", frame), v, ui)
				}
				var methods []string
				for len(v.Controller.Jobs) > 0 {
					j := <-v.Controller.Jobs
					methods = append(methods, j.Method)
				}
				testutil.StateTrace(t, "dispatched", methods)
			})
		}
	}
}

func TestStateTraceQueueArrivalFrames(t *testing.T) {
	for _, order := range []string{"receipt-first", "row-first", "together"} {
		t.Run(order, func(t *testing.T) {
			m := layoutModel()
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			v.beginQueueArrival("queued", false)
			m.QueueExpanded["s"] = true
			for frame := 0; frame < 36; frame++ {
				if frame == 0 && order != "row-first" || frame == 6 {
					m.Resolved["queued"] = true
				}
				if frame == 0 && order != "receipt-first" || frame == 6 {
					d := m.Details["s"]
					d.Queue = domain.QueueState{SessionID: "s", Revision: 1, Items: []domain.QueueItem{{ID: "queued", Input: domain.Submission{Text: "queued input"}}}}
					m.Details["s"] = d
				}
				ui.Frame()
				ui.Advance(time.Second / 60)
				traceNativeFrame(t, fmt.Sprint("frame-", frame), v, ui)
			}
		})
	}
}

func TestStateTraceQuestionSwitchAndTools(t *testing.T) {
	v := requestMotionModel()
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	d := v.Model.Details["s"]
	d.Requests = append(d.Requests, domain.PendingRequest{ID: "r-added", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q-added", Text: "A second question with a different body height?", Custom: true}}})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	v.Model.Pager["s"] = len(d.Requests[0].Questions)
	for frame := 0; frame < 24; frame++ {
		ui.Advance(time.Second / 60)
		traceNativeFrame(t, fmt.Sprint("question-", frame), v, ui)
	}
	d.Requests = nil
	d.Items = []domain.Item{{ID: "tool", Kind: "tool", Name: "Run command", Text: "Target marker", Final: true}, {ID: "reply", Kind: "assistant", Text: "Following prose", Final: true}}
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	ui.Click("1 次工具调用")
	ui.Advance(200 * time.Millisecond)
	traceNativeFrame(t, "tool-opened", v, ui)
	ui.Key(n.Cmd, n.KeyF)
	ui.Type("marker")
	ui.Advance(800 * time.Millisecond)
	traceNativeFrame(t, "tool-find", v, ui)
	ui.Key(0, n.KeyEscape)
	ui.Advance(300 * time.Millisecond)
	testutil.StateTrace(t, "tool-layout", v.Model.Collapsed)
}

func TestStateTraceListAnchorFrames(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprint("reduced=", reduced), func(t *testing.T) {
			count := 120
			state := n.ListState{FollowEnd: true}
			ui := n.NewTester(func(c *n.Context) {
				state.Key = func(i int) any { return i }
				n.List(c, &state, count, func(i int) { n.Text(c, fmt.Sprintf("Row %d", i)).Height(32) }).Fill()
			}, 400, 300)
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)
			capture := func(step string) {
				first, last := state.Visible()
				testutil.StateTrace(t, step, struct {
					First, Last    int
					Following, End bool
					Texts          []string
					Pixels         string
				}{first, last, state.FollowingEnd(), state.AtEnd(), ui.Texts(), fmt.Sprintf("%x", sha256.Sum256(ui.Image().Pix))})
			}
			state.ScrollToRow(10, 80, n.Animation{Duration: 300 * time.Millisecond, Ease: n.Linear})
			for i := 0; i < 24; i++ {
				ui.Advance(time.Second / 60)
				capture(fmt.Sprint("reading-", i))
			}
			state.KeepRow(10, 80)
			ui.Frame()
			capture("keep")
			state.AnchorRow(100, 100, n.Animation{Duration: 480 * time.Millisecond, Ease: n.Linear})
			for i := 0; i < 36; i++ {
				ui.Advance(time.Second / 60)
				capture(fmt.Sprint("sent-", i))
			}
			count = 145
			ui.Frame()
			capture("grown")
			state.ReleaseRow()
			ui.Frame()
			capture("release")
			state.ScrollToEnd()
			ui.Frame()
			capture("end")
		})
	}
}

func TestStateTracePreviewFrames(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprint("reduced=", reduced), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			ui.Advance(time.Second)
			pixels := image.NewRGBA(image.Rect(0, 0, 64, 64))
			for y := 0; y < 64; y++ {
				for x := 0; x < 64; x++ {
					pixels.SetRGBA(x, y, color.RGBA{R: 90, G: 130, B: 190, A: 255})
				}
			}
			a := domain.Attachment{ID: "preview", Name: "image.png", MIME: "image/png"}
			v.attachmentImages = map[string]*attachmentImage{a.ID: {Image: n.NewBitmap(pixels)}}
			v.Controller = app.NewController(t.TempDir())
			v.StatePath = filepath.Join(t.TempDir(), "state.json")
			v.openAttachment(a, "w", n.Rect{X: 500, Y: 700, W: 36, H: 36})
			// Keep the real dialog lifecycle, but don't deliver asynchronous I/O into
			// the fixed-clock replay. Its source pixels are already present in RAM.
			v.Controller = nil
			ui.Frame()
			for frame := 0; frame < 24; frame++ {
				ui.Advance(time.Second / 60)
				traceNativeFrame(t, fmt.Sprint("open-", frame), v, ui)
			}
			if err := ui.Click("关闭附件预览"); err != nil {
				t.Fatal(err)
			}
			for frame := 0; frame < 24; frame++ {
				ui.Advance(time.Second / 60)
				traceNativeFrame(t, fmt.Sprint("close-", frame), v, ui)
			}
		})
	}
}

func TestStateTraceSSHCancelAndReopen(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.devices = true
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	show := func() {
		t.Helper()
		if err := ui.Click("添加设备"); err != nil {
			t.Fatal(err)
		}
		if err := ui.Click("通过 SSH"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(200 * time.Millisecond)
	}
	show()
	v.sshTarget = "developer@air.local"
	ui.Frame()
	traceNativeFrame(t, "editing", v, ui)
	v.checkSSHDevice()
	ui.Frame()
	traceNativeFrame(t, "checking", v, ui)
	var check app.Job
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "$device.check" {
			check = j
		}
	}
	if check.Apply == nil {
		t.Fatal("missing check")
	}
	v.cancelSSHCheck()
	ui.Frame()
	ui.Advance(150 * time.Millisecond)
	traceNativeFrame(t, "canceled", v, ui)
	show()
	traceNativeFrame(t, "reopen-pending", v, ui)
	check.Apply(v.Model, nil, errors.New("failed at 2026-11-07T12:00:00Z"))
	ui.Frame()
	traceNativeFrame(t, "canceled-receipt", v, ui)
	v.sshTarget = "developer@air.local"
	v.checkSSHDevice()
	ui.Frame()
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "$device.check" {
			check = j
		}
	}
	check.Apply(v.Model, nil, errors.New("failed at 2026-11-07T12:00:00Z"))
	ui.Frame()
	traceNativeFrame(t, "error", v, ui)
}

func TestStateTraceFileDiffLifecycle(t *testing.T) {
	v := New(layoutModel(), app.NewController(""), "")
	v.Model.Inspectors["s"] = true
	take := func(method string) app.Job {
		t.Helper()
		var result app.Job
		for len(v.Controller.Jobs) > 0 {
			j := <-v.Controller.Jobs
			if j.Method == method {
				result = j
			}
		}
		if result.Apply == nil {
			t.Fatalf("missing %s", method)
		}
		return result
	}
	v.requestChanges(true)
	take("git.changes").Apply(v.Model, json.RawMessage(`[{"path":"main.go"}]`), nil)
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	v.openChangedFile("main.go")
	initial := take("git.file")
	ui.Frame()
	ui.Advance(120 * time.Millisecond)
	traceNativeFrame(t, "loading", v, ui)
	initial.Apply(v.Model, json.RawMessage(`{"path":"main.go","old":"before\n","new":"after\n"}`), nil)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	traceNativeFrame(t, "ready", v, ui)
	v.openChangedFile("main.go")
	refresh := take("git.file")
	ui.Frame()
	traceNativeFrame(t, "refreshing", v, ui)
	refresh.Apply(v.Model, nil, errors.New("failed at 2026-11-07T12:00:00Z"))
	ui.Frame()
	traceNativeFrame(t, "refresh-error", v, ui)
	v.requestChanges(true)
	take("git.changes").Apply(v.Model, json.RawMessage(`[{"path":"main.go"}]`), nil)
	ui.Frame()
	traceNativeFrame(t, "cache-after-list-refresh", v, ui)
	v.openChangedFile("other.go")
	other := take("git.file")
	v.openChangedFile("third.go")
	third := take("git.file")
	other.Apply(v.Model, json.RawMessage(`{"path":"other.go","old":"stale","new":"stale"}`), nil)
	ui.Frame()
	traceNativeFrame(t, "stale-ignored", v, ui)
	third.Apply(v.Model, json.RawMessage(`{"path":"main.go","old":"before\n","new":"after\n"}`), nil)
	ui.Frame()
	traceNativeFrame(t, "mismatched-path", v, ui)
	if err := ui.Click("返回更改列表"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	traceNativeFrame(t, "closed", v, ui)
	v.openChangedFile("main.go")
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	traceNativeFrame(t, "reopen-cache", v, ui)
}
