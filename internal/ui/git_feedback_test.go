package ui

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

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
