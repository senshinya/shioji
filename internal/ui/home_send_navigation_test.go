package ui

import (
	"testing"

	"shioji/internal/domain"
)

func TestNewSessionWhileFirstSendIsPending(t *testing.T) {
	v, ui, first := sendFixture(t, true)
	in := first.Input.(domain.Submission)
	if err := ui.RightClick("Example"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("新建会话"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()

	if got := v.Model.Drafts["home"]; got != "" {
		t.Errorf("new composer inherited the first message: %q", got)
	}
	if v.Model.HasPendingInput() {
		t.Error("the first message blocks the new composer")
	}

	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("Second conversation draft")
	acceptSend(t, v, first, true)
	ui.Frame()
	if v.Model.SessionID != "" {
		t.Errorf("late first receipt opened session %q", v.Model.SessionID)
	}
	if got := v.Model.Drafts["home"]; got != "Second conversation draft" {
		t.Errorf("late first receipt changed the new draft: %q", got)
	}
	if !v.Model.Resolved[in.OperationID] || !ui.HasText("New turn") {
		t.Error("the first conversation did not reach the sidebar")
	}
}
