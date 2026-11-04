package ui

import (
	"fmt"
	native "github.com/egoist/mygo/ui"
	"math"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
)

func TestPendingQuestionKeepsOrdinaryComposerVisible(t *testing.T) {
	m := app.NewModel()
	m.Connected = true
	m.WorkspaceID = "w"
	m.SessionID = "s"
	m.Readiness = domain.Readiness{State: "ready", Permissions: []string{"read-only"}, Models: []domain.Model{{ID: "test", Default: true}}}
	m.Snapshot = domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w", Name: "Example"}}, Sessions: []domain.Session{{ID: "s", WorkspaceID: "w", Title: "Task", Status: "running"}}}
	m.Details["s"] = domain.SessionDetail{Session: m.Snapshot.Sessions[0], Requests: []domain.PendingRequest{{ID: "r", State: "pending", Kind: "questions", Title: "需要回答", Questions: []domain.Question{{ID: "q", Text: strings.Repeat("A long question. ", 80), Custom: true}}}}}
	view := New(m, nil, "")
	test := native.NewTester(view.View, 1000, 650)

	rect, ok := test.Find("消息输入")
	if !ok || rect.Y+rect.H > 650 || rect.H < 40 {
		t.Fatalf("ordinary composer hidden: %+v %v", rect, ok)
	}
}
func TestHomeDoesNotCreateSessionWhileRendering(t *testing.T) {
	m := app.NewModel()
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1000, 700)

	if len(m.Snapshot.Sessions) != 0 || len(m.Pending) != 0 {
		t.Fatal("render created an empty session")
	}
	if _, ok := test.Find("创建工作区"); !ok {
		t.Fatal("no workspace setup action")
	}
}

func TestUnknownExecutionIsNotShownAsRunningOrCompleted(t *testing.T) {
	m := app.NewModel()
	m.SessionID = "s"
	m.WorkspaceID = "w"
	s := domain.Session{ID: "s", WorkspaceID: "w", Title: "Needs reconciliation", Status: "unknown"}
	m.Snapshot = domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w", Name: "Test"}}, Sessions: []domain.Session{s}}
	m.Details["s"] = domain.SessionDetail{Session: s, Items: []domain.Item{{ID: "i", TurnID: "t", Kind: "assistant", Text: "Partial"}}, Turns: []domain.Turn{{ID: "t", Status: "unknown"}}}
	v := New(m, nil, "")
	test := native.NewTester(v.View, 1000, 700)
	if test.HasText("工作中") || test.HasText("本轮完成") {
		t.Fatal("unknown execution has a definitive running/completed label")
	}
	if !test.HasText("执行状态待核实") {
		t.Fatal("unknown execution lacks an explicit state")
	}
}

func TestCloseFlushCapturesNativeTextBeforeNextRender(t *testing.T) {
	m := app.NewModel()
	m.Drafts["home"] = "original"
	path := filepath.Join(t.TempDir(), "state.json")
	v := New(m, nil, path)
	defer v.Saver.Close()
	v.draftBindings["home"] = &editBinding{Text: "latest native edit", LastModel: "original"}
	if err := v.Flush(); err != nil {
		t.Fatal(err)
	}
	restored := app.NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if restored.Drafts["home"] != "latest native edit" {
		t.Fatal("quit saved only the previous rendered value")
	}
}

func layoutModel() *app.Model {
	m := app.NewModel()
	m.Connected = true
	m.WorkspaceID = "w"
	m.SessionID = "s"
	m.Readiness = domain.Readiness{State: "ready", Permissions: []string{"read-only"}, Models: []domain.Model{{ID: "gpt-5.4", Default: true}}}
	s := domain.Session{ID: "s", WorkspaceID: "w", Title: "Layout", Status: "completed"}
	m.Snapshot = domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w", Name: "Example"}}, Sessions: []domain.Session{s}}
	m.Details["s"] = domain.SessionDetail{Session: s, Items: []domain.Item{{ID: "i", Kind: "assistant", Text: "Aligned reply"}}}
	return m
}
func TestConversationSharesCenteredReadingColumn(t *testing.T) {
	for _, width := range []int{1440, 950} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			test := native.NewTester(v.View, width, 900)
			reply, _ := test.Find("Aligned reply")
			input, _ := test.Find("消息输入")
			history, _ := test.Find("对话记录")
			if math.Abs(float64(reply.X-(input.X-15))) > 1 {
				t.Fatalf("reply and composer use different columns: reply=%+v input=%+v", reply, input)
			}
			if history.Y != 0 || history.H != 900 {
				t.Fatalf("scrollport does not span the pane: %+v", history)
			}
			if width == 1440 && math.Abs(float64(reply.X-464)) > 1 {
				t.Fatalf("reading column is not centered at frozen width: %+v", reply)
			}
		})
	}
}
func TestShortQuestionDoesNotReserveMultilineAnswerSpace(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Requests = []domain.PendingRequest{{ID: "r", State: "pending", Kind: "questions", Title: "需要回答", Questions: []domain.Question{{ID: "q", Text: "Which next?", Custom: true, Options: []domain.Option{{Label: "One"}, {Label: "Two"}}}}}}
	m.Details["s"] = d
	test := native.NewTester(New(m, nil, "").View, 950, 650)
	answer, _ := test.Find("自定义答案")
	input, _ := test.Find("消息输入")
	if answer.H > 34 || answer.H < 30 {
		t.Fatalf("short answer should start compact: %+v", answer)
	}
	if input.Y+input.H > 650 || input.H < 54 {
		t.Fatalf("composer hidden by question: %+v", input)
	}
}

func TestBottomDockReservesLatestReplyWithGrowingDraft(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	for i := 0; i < 40; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: fmt.Sprintf("Reply %d", i)})
	}
	m.Details["s"] = d

	m.Readiness.Models = []domain.Model{{ID: strings.Repeat("a-long-model-name-", 5), Default: true, Efforts: []string{"medium"}}}
	m.Configs[m.DraftKey()] = domain.RunConfig{Model: m.Readiness.Models[0].ID, Effort: "medium", Permission: "read-only"}
	test := native.NewTester(New(m, nil, "").View, 850, 600)
	m.Drafts[m.DraftKey()] = strings.Repeat("A growing multiline draft\n", 12)
	test.Frame()
	reply, ok := test.Find("Reply 39")
	dock, _ := test.Find("会话底部面板")
	send, sent := test.Find("发送")
	if !ok || reply.Y+reply.H > dock.Y {
		t.Fatalf("latest reply covered by growing dock: reply=%+v dock=%+v", reply, dock)
	}
	if test.FramePending() {
		t.Fatal("dock measurements did not settle")
	}
	if !sent || send.X+send.W > 850 || send.Y+send.H > 600 {
		t.Fatalf("run configuration pushes send outside window: %+v", send)
	}
}

func TestHomeKeepsLongWorkspaceHeadingInsidePane(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	m.Snapshot.Workspaces[0].Name = strings.Repeat("Workspace with a long name ", 10)
	test := native.NewTester(New(m, nil, "").View, 850, 600)
	picker, _ := test.Find("选择工作区")
	end, _ := test.Find("做些什么？")
	if picker.X < 224 || end.X+end.W > 832 {
		t.Fatalf("long workspace heading overflows pane: picker=%+v ending=%+v", picker, end)
	}
}
