package ui

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestContextChangeRefreshesPreviouslyNonGitComposerAndExposesConfirmation(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = protocol.ActiveGitConfirmationMinor
	m.ConnectionEpoch = "git-epoch"
	const draft = "Continue after cloning"
	m.Drafts[m.DraftKey()] = draft
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1100, 680)
	feedbackJob(t, v, "git.status").Apply(m, json.RawMessage(`{}`), nil)
	ui.Frame()
	if ui.HasText("选择分支") {
		t.Fatal("the initial non-Git session shows a branch")
	}

	session := m.Details["s"].Session
	session.ContextChanged = true
	session.Revision++
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "session", SessionID: "s", Session: &session}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	status := json.RawMessage(`{"applicable":true,"branch":"main","head":"abcdef1234","branches":["main"]}`)
	feedbackJob(t, v, "git.status").Apply(m, status, nil)
	ui.Frame()
	ui.Advance(time.Second)
	feedbackClick(t, ui, "选择分支")
	feedbackJob(t, v, "git.status").Apply(m, status, nil)
	ui.Frame()
	ui.Advance(time.Second)
	feedbackClick(t, ui, "确认当前分支")
	job := feedbackJob(t, v, "git.confirm")
	if input, ok := job.Input.(protocol.ConfirmCheckoutInput); !ok || input.SessionID != "s" || input.Branch != "main" || input.Head != "abcdef1234" {
		t.Fatalf("confirmation did not target the checkout shown in the menu: %+v", job.Input)
	}
	if m.Drafts[m.DraftKey()] != draft {
		t.Fatal("opening branch confirmation changed the unsent draft")
	}
}

func TestBackgroundBranchReadDoesNotRepeatTheSameFailure(t *testing.T) {
	m := layoutModel()
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1100, 680)
	failed := historyJob(t, v, "git.status")
	failed.Apply(m, nil, errors.New("Git:  (exec: WaitDelay expired before I/O complete)"))
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("弹出消息") {
		t.Fatalf("background branch read interrupted the conversation: %v", ui.Texts())
	}
	for len(c.Jobs) > 0 {
		if job := <-c.Jobs; job.Method == "git.status" {
			t.Fatal("the same failed read started again on redraw")
		}
	}
}

func TestFirstBranchReadFailureCanBeRetriedInPlace(t *testing.T) {
	m := layoutModel()
	m.SessionID = ""
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1100, 680)
	failed := historyJob(t, v, "git.status")
	failed.Apply(m, nil, errors.New("context deadline exceeded"))
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("无法读取分支") || ui.HasText("弹出消息") {
		t.Fatalf("the first branch read failure should stay on the branch row: %v", ui.Texts())
	}
	if err := ui.Click("重新读取分支"); err != nil {
		t.Fatal(err)
	}
	retry := historyJob(t, v, "git.status")
	if retry.Input != failed.Input {
		t.Fatal("branch retry reads a different workspace")
	}
	b, err := json.Marshal(domain.GitStatus{Applicable: true, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	retry.Apply(m, b, nil)
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	if ui.HasText("无法读取分支") || !ui.HasText("选择分支") {
		t.Fatalf("successful reread did not restore branch selection: %v", ui.Texts())
	}
}
