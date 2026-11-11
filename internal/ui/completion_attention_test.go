package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestAttentionVisitsCompletedRepliesOnceAndPreservesDrafts(t *testing.T) {
	m := parallelModel()
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "other", Turn: &domain.Turn{ID: "done", Order: 1, Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	v := New(m, nil, "")
	v.collapsedWorkspaces["w"] = true
	ui := n.NewTester(v.View, 1240, 820)
	if err := ui.Click("待我处理"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if m.SessionID != "other" || v.collapsedWorkspaces["w"] || m.Drafts["session:s"] != "left draft" || m.Drafts["session:other"] != "right draft" {
		t.Fatal("completed attention failed to navigate or changed drafts")
	}
	v.navigateSession("s", "w")
	v.nextAttention()
	if m.SessionID != "s" {
		t.Fatal("read completed reply appeared in attention again")
	}
}

func TestQuestionsKeepPriorityOverCompletedReplyAttention(t *testing.T) {
	m := parallelModel()
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "s", Turn: &domain.Turn{ID: "done", Order: 1, Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	d := m.Details["other"]
	d.Requests = []domain.PendingRequest{{ID: "q", SessionID: "other", Kind: "questions", State: "pending"}}
	m.Details["other"] = d
	v := New(m, nil, "")
	v.nextAttention()
	if m.SessionID != "other" || !m.HasCompletedReply("s") {
		t.Fatal("completion took priority over a question or was incorrectly consumed")
	}
}

func TestOrdinaryNavigationConsumesCompletionWithoutChangingScroll(t *testing.T) {
	m := parallelModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "other", Turn: &domain.Turn{ID: "done", Order: 1, Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	before, _ := ui.Find("Aligned reply")
	ui.Advance(300 * time.Millisecond)
	after, _ := ui.Find("Aligned reply")
	if before != after || m.SessionID != "s" || !m.HasCompletedReply("other") {
		t.Fatal("background completion moves the current reading position or consumes attention")
	}
	if err := ui.Click("Another session"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if m.SessionID != "other" || m.HasCompletedReply("other") {
		t.Fatal("normal sidebar navigation did not consume completion")
	}
}

func TestCompletionAttentionSkipsArchivedAndActiveSessions(t *testing.T) {
	for _, state := range []string{"archived-session", "archived-workspace", "running"} {
		t.Run(state, func(t *testing.T) {
			m := parallelModel()
			if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "other", Turn: &domain.Turn{ID: "done", Order: 1, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "archived-session":
				m.Snapshot.Sessions[1].Archived = true
			case "archived-workspace":
				m.Snapshot.Workspaces[0].Archived = true
			case "running":
				m.Snapshot.Sessions[1].Status = "running"
			}
			v := New(m, nil, "")
			v.nextAttention()
			if m.SessionID != "s" {
				t.Fatal("attention navigated to an archived or still-running session")
			}
		})
	}
}
