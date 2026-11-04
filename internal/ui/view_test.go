package ui

import (
	native "github.com/egoist/mygo/ui"
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
