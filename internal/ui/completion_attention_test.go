package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
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

// Native component recording uses deterministic worker projections. The actual
// two-client RPC and durable journal are covered in internal/worker.
func TestCompletionAttentionWorkerReadAndNativeRecording(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%v", dark), func(t *testing.T) {
			m := parallelModel()
			m.WorkerMinor = 39
			s := m.Snapshot.Sessions[1]
			s.Revision = 1
			s.Completion = &domain.ReplyCompletion{ID: "old", TurnID: "old", Order: 1, CompletedAt: time.Now().Add(-time.Hour), Unread: true}
			snapshot := m.Snapshot
			snapshot.Sessions[1] = s
			m.ApplySnapshot(snapshot)
			a := parallelModel()
			rawSnapshot, _ := json.Marshal(snapshot)
			var separate domain.Snapshot
			if err := json.Unmarshal(rawSnapshot, &separate); err != nil {
				t.Fatal(err)
			}
			a.ApplySnapshot(separate)
			controller := app.NewController("")
			viewA := New(a, controller, "")
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			frame := 0
			dir := os.Getenv("SHIOJI_ATTENTION_RECORDING")
			if dir != "" {
				t.Setenv("SHIOJI_SKILL_SNAPSHOTS", fmt.Sprintf("%s/dark-%v", dir, dark))
			}
			capture := func() {
				if dir == "" {
					ui.Advance(time.Second)
					return
				}
				for range 24 {
					ui.Advance(time.Second / 12)
					saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
					frame++
				}
			}
			capture()
			if _, available := v.attentionSession(); available {
				t.Fatal("completed before launch was attention")
			}
			s.Revision = 2
			s.Completion = &domain.ReplyCompletion{ID: "new", TurnID: "new", Order: 2, CompletedAt: time.Now().Add(time.Second), Unread: true}
			if err := m.ApplyEvent(domain.Event{Sequence: 1, SessionID: s.ID, Type: "turn", Session: &s, Turn: &domain.Turn{ID: "new", Order: 2, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			capture()
			if next, available := v.attentionSession(); !available || next.ID != "other" {
				t.Fatal("new completion absent")
			}
			// A's navigation submits an exact-completion acknowledgement.
			a.ApplySnapshot(domain.Snapshot{Sequence: 1, Workspaces: separate.Workspaces, Sessions: []domain.Session{separate.Sessions[0], s}})
			viewA.navigateSession(s.ID, s.WorkspaceID)
			var readJob app.Job
			for len(controller.Jobs) > 0 {
				job := <-controller.Jobs
				if job.Method == "session.completion.read" {
					readJob = job
				}
			}
			if readJob.Apply == nil {
				t.Fatal("navigation did not submit worker acknowledgement")
			}
			read := s
			c := *s.Completion
			read.Completion = &c
			read.Completion.Unread, read.Revision = false, 3
			raw, _ := json.Marshal(read)
			readJob.Apply(a, raw, nil)
			if !m.HasCompletedReply("other") {
				t.Fatal("clients share local state before worker event")
			}
			capture()
			// The same persisted worker event arrives at B and removes attention.
			if err := m.ApplyEvent(domain.Event{Sequence: 2, SessionID: read.ID, Type: "session", Session: &read}); err != nil {
				t.Fatal(err)
			}
			v.nextAttention()
			capture()
			if _, available := v.attentionSession(); available || m.SessionID != "s" || m.Drafts["session:s"] != "left draft" {
				t.Fatal("shared read retained attention or changed current draft")
			}
		})
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
