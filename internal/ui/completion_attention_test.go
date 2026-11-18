package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestNewCompletedReplyIsReadOnlyInVisibleConversations(t *testing.T) {
	for _, tt := range []struct {
		name, session string
		width         int
		secondary     bool
		focusRight    bool
		page          string
		wantUnread    bool
	}{
		{name: "current", session: "s", width: 1240},
		{name: "background", session: "other", width: 1240, wantUnread: true},
		{name: "parallel-left", session: "s", width: 1440, secondary: true, focusRight: true},
		{name: "parallel-right", session: "other", width: 1440, secondary: true},
		{name: "narrow-hidden", session: "other", width: 950, secondary: true, wantUnread: true},
		{name: "narrow-focused", session: "other", width: 950, secondary: true, focusRight: true},
		{name: "narrow-hidden-left", session: "s", width: 950, secondary: true, focusRight: true, wantUnread: true},
		{name: "devices", session: "s", width: 1240, page: "devices", wantUnread: true},
		{name: "workspace-settings", session: "s", width: 1240, page: "workspace", wantUnread: true},
		{name: "archive", session: "s", width: 1240, page: "archive", wantUnread: true},
		{name: "home", session: "s", width: 1240, page: "home", wantUnread: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := parallelModel()
			if tt.secondary {
				m.SecondarySessionID, m.SecondaryWorkspaceID, m.FocusSecondary = "other", "w", tt.focusRight
			}
			v := New(m, nil, "")
			switch tt.page {
			case "devices":
				v.devices = true
			case "workspace":
				v.workspaceEditor = &workspaceEditor{Workspace: m.Snapshot.Workspaces[0]}
			case "archive":
				v.openArchive()
			case "home":
				v.home()
			}
			ui := n.NewTester(v.View, tt.width, 820)
			if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: tt.session, Turn: &domain.Turn{ID: "done", Order: 1, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			if got := m.HasCompletedReply(tt.session); got != tt.wantUnread {
				t.Fatalf("unread = %v, want %v", got, tt.wantUnread)
			}
			if !tt.wantUnread {
				v.home()
				ui.Frame()
				if _, available := v.attentionSession(); available {
					t.Fatal("reply read while visible returned to attention after leaving")
				}
			}
			if m.Drafts["session:s"] != "left draft" || m.Drafts["session:other"] != "right draft" {
				t.Fatal("reading a completion changed composer drafts")
			}
		})
	}
}

func TestVisibleCompletionAcknowledgesWorkerOnceAndKeepsPendingRequests(t *testing.T) {
	m := parallelModel()
	m.WorkerMinor = 39
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1240, 820)
	reads := func() []app.Job {
		var jobs []app.Job
		for len(c.Jobs) > 0 {
			if job := <-c.Jobs; job.Method == "session.completion.read" {
				jobs = append(jobs, job)
			}
		}
		return jobs
	}
	reads()
	s := m.Snapshot.Sessions[0]
	s.Revision = 1
	s.Completion = &domain.ReplyCompletion{ID: "first", TurnID: "done", Order: 1, CompletedAt: time.Now().Add(time.Second), Unread: true}
	c.Updates <- func(m *app.Model) {
		if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "session", SessionID: s.ID, Session: &s}); err != nil {
			t.Fatal(err)
		}
	}
	ui.Frame()
	jobs := reads()
	if len(jobs) != 1 || jobs[0].ConnectionID != "local" || jobs[0].Input.(protocol.CompletionReadInput).CompletionID != "first" || m.HasCompletedReply("s") {
		t.Fatalf("visible completion was not acknowledged exactly once: jobs=%v unread=%v", jobs, m.HasCompletedReply("s"))
	}
	// Failed read synchronization restores attention without a per-frame retry loop.
	c.Updates <- func(m *app.Model) { jobs[0].Apply(m, nil, errors.New("offline")) }
	ui.Frame()
	ui.Frame()
	if !m.HasCompletedReply("s") || m.Error == "" || len(reads()) != 0 {
		t.Fatal("failed acknowledgement lost attention or retried every frame")
	}
	s.Revision = 2
	s.Completion = &domain.ReplyCompletion{ID: "second", TurnID: "done-2", Order: 2, CompletedAt: time.Now().Add(2 * time.Second), Unread: true}
	if err := m.ApplyEvent(domain.Event{Sequence: 2, Type: "session", SessionID: s.ID, Session: &s}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	jobs = reads()
	if len(jobs) != 1 || jobs[0].Input.(protocol.CompletionReadInput).CompletionID != "second" || m.HasCompletedReply("s") {
		t.Fatal("later visible completion was not independently acknowledged")
	}
	for _, kind := range []string{"questions", "approval"} {
		d := m.Details["s"]
		d.Requests = []domain.PendingRequest{{ID: "pending", SessionID: "s", Kind: kind, State: "pending"}}
		m.Details["s"] = d
		if session, available := v.attentionSession(); !available || session.ID != "s" {
			t.Fatalf("reading a completion hid pending %s", kind)
		}
	}
}

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

func TestAttentionShowsOneCountPerNavigableSession(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%v", dark), func(t *testing.T) {
			m := parallelModel()
			m.Snapshot.Sessions[0], m.Snapshot.Sessions[1] = m.Snapshot.Sessions[1], m.Snapshot.Sessions[0]
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 950, 700)
			ui.SetDark(dark)
			original, _ := ui.Find("待我处理")
			if ui.HasText("待处理会话数：") {
				t.Fatal("empty attention should not display a count")
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("attention-empty-dark-%v.png", dark))

			detail := m.Details["s"]
			detail.Requests = []domain.PendingRequest{
				{ID: "approval", SessionID: "s", Kind: "approval", State: domain.RequestPending},
				{ID: "question", SessionID: "s", Kind: "questions", State: domain.RequestSubmitting},
			}
			m.Details["s"] = detail
			if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "other", Turn: &domain.Turn{ID: "done", Order: 1, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			badge, shown := ui.Find("待处理会话数：2")
			if !shown {
				t.Fatalf("two navigable sessions should show 2 exactly once, got %v", ui.Texts())
			}
			entry, _ := ui.Find("待我处理")
			if entry != original || badge.W <= 0 || badge.X+badge.W > entry.X+entry.W || badge.X < entry.X+entry.W-48 {
				t.Fatalf("count should fit at the end of the unchanged row: entry=%+v count=%+v", entry, badge)
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("attention-count-dark-%v.png", dark))
			if err := ui.Click("待我处理"); err != nil || m.SessionID != "s" {
				t.Fatalf("pending requests should take precedence over the earlier unread completion: %s, %v", m.SessionID, err)
			}

			detail.Requests = nil
			m.Details["s"] = detail
			ui.Frame()
			if _, shown := ui.Find("待处理会话数：1"); !shown {
				t.Fatal("resolving requests should leave one unread session")
			}
			if err := ui.Click("待我处理"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if ui.HasText("待处理会话数：") || m.SessionID != "other" {
				t.Fatal("opening the last unread reply should hide the count")
			}
		})
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

func TestSidebarCompletedCheckFollowsUnreadAttention(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark-%v", dark), func(t *testing.T) {
			m := parallelModel()
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			assertCheck := func(stage string, wantGreen bool) {
				t.Helper()
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("completed-check-dark-%v-%s.png", dark, stage))
				mark := sidebarMarkFor(ui, "Another session")
				green, ink := false, false
				bg := mark.RGBAAt(0, 0)
				for y := 0; y < mark.Bounds().Dy(); y++ {
					for x := 0; x < mark.Bounds().Dx(); x++ {
						p := mark.RGBAAt(x, y)
						ink = ink || max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30
						green = green || int(p.G) > int(p.R)+20 && int(p.G) > int(p.B)+10
					}
				}
				if !ink || green != wantGreen {
					t.Errorf("%s: check ink=%v green=%v, want green=%v", stage, ink, green, wantGreen)
				}
			}
			assertCheck("already-read", false)
			if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "other", Turn: &domain.Turn{ID: "done", Order: 1, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			if next, ok := v.attentionSession(); !ok || next.ID != "other" {
				t.Fatal("new completion did not enter attention")
			}
			assertCheck("unread", true)
			if err := ui.Click("Another session"); err != nil {
				t.Fatal(err)
			}
			ui.Frame()
			if _, ok := v.attentionSession(); ok {
				t.Fatal("opened completion remained in attention")
			}
			assertCheck("opened", false)
		})
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
