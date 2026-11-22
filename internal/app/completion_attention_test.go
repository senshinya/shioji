package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestCompletedReplyAttentionIsReadOncePerTurn(t *testing.T) {
	m := NewModel()
	m.ApplySnapshot(domain.Snapshot{Sessions: []domain.Session{{ID: "s", Status: "completed"}}})
	if m.HasCompletedReply("s") {
		t.Fatal("loading old completed sessions creates attention")
	}
	complete := domain.Event{Sequence: 1, Type: "turn", SessionID: "s", Turn: &domain.Turn{ID: "t1", Order: 1, Status: "completed"}}
	if err := m.ApplyEvent(complete); err != nil {
		t.Fatal(err)
	}
	if !m.HasCompletedReply("s") {
		t.Fatal("natural completion does not enter attention with notifications disabled")
	}
	m.ReadCompletedReply("s")
	// History eviction and late usage must not make the same reply unread again.
	delete(m.Details, "s")
	complete.Sequence = 2
	if err := m.ApplyEvent(complete); err != nil {
		t.Fatal(err)
	}
	if m.HasCompletedReply("s") {
		t.Fatal("the same completion reappeared after reading")
	}
	complete.Sequence, complete.Turn = 3, &domain.Turn{ID: "t2", Order: 2, Status: "completed"}
	if err := m.ApplyEvent(complete); err != nil {
		t.Fatal(err)
	}
	if !m.HasCompletedReply("s") {
		t.Fatal("a new reply does not get its own attention")
	}
	m.ReadCompletedReply("s")
	complete.Sequence, complete.Turn = 4, &domain.Turn{ID: "t1", Order: 1, Status: "completed"}
	if err := m.ApplyEvent(complete); err != nil {
		t.Fatal(err)
	}
	if m.HasCompletedReply("s") {
		t.Fatal("late completion from an older turn reappeared")
	}
}

func TestCompletedReplyAttentionExcludesNonReplies(t *testing.T) {
	for _, turn := range []domain.Turn{
		{ID: "t", Status: "running"},
		{ID: "t", Status: "interrupted"},
		{ID: "t", Status: "failed"},
		{ID: "t", Status: "unknown"},
		{ID: "t", Status: "completed", Presentation: "compaction"},
	} {
		t.Run(string(turn.Status)+turn.Presentation, func(t *testing.T) {
			m := NewModel()
			if err := m.ApplyEvent(domain.Event{Sequence: 1, SessionID: "s", Type: "turn", Turn: &turn}); err != nil {
				t.Fatal(err)
			}
			if m.HasCompletedReply("s") {
				t.Fatal("non-reply completion entered attention")
			}
		})
	}
}

func TestCompletedReplyAttentionDoesNotSurviveClientRestart(t *testing.T) {
	m := NewModel()
	for i, id := range []string{"read", "unread"} {
		if err := m.ApplyEvent(domain.Event{Sequence: int64(i + 1), SessionID: id, Type: "turn", Turn: &domain.Turn{ID: id, Order: 1, Status: "completed"}}); err != nil {
			t.Fatal(err)
		}
	}
	m.ReadCompletedReply("read")
	path := filepath.Join(t.TempDir(), "client.json")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	restored := NewModel()
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	if restored.HasCompletedReply("unread") || restored.HasCompletedReply("read") {
		t.Fatal("replies completed before app startup entered attention")
	}
	old := time.Now().Add(-time.Hour)
	if err := restored.ApplyEvent(domain.Event{Sequence: 1, SessionID: "read", Type: "turn", Turn: &domain.Turn{ID: "read", Order: 1, Status: "completed", CompletedAt: &old}}); err != nil {
		t.Fatal(err)
	}
	if restored.HasCompletedReply("read") {
		t.Fatal("replayed completion resurrected a read reply after restart")
	}
}

func TestCompletedReplyAttentionStaysOnItsOwningDevice(t *testing.T) {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B"}}
	for key, device := range map[string]string{"local": "A", "air": "B"} {
		m.ApplyDeviceUpdate(key, func(p *Model) {
			p.ApplySnapshot(domain.Snapshot{DeviceID: device, Workspaces: []domain.Workspace{{ID: "w" + device, DeviceID: device}}, Sessions: []domain.Session{{ID: "s" + device, WorkspaceID: "w" + device, Status: "completed"}}})
		})
	}
	m.ApplyDeviceUpdate("air", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "sB", Turn: &domain.Turn{ID: "same-source-id", Order: 1, Status: "completed"}}); err != nil {
			t.Fatal(err)
		}
	})
	if !m.HasCompletedReply("sB") || m.HasCompletedReply("sA") {
		t.Fatal("background device completion was lost or crossed device boundaries")
	}
	m.ReadCompletedReply("sB")
	m.ApplyDeviceUpdate("local", func(p *Model) {
		if err := p.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "sA", Turn: &domain.Turn{ID: "same-source-id", Order: 1, Status: "completed"}}); err != nil {
			t.Fatal(err)
		}
	})
	if !m.HasCompletedReply("sA") || m.HasCompletedReply("sB") {
		t.Fatal("completion read state was shared across devices")
	}
}

func TestCompletedReplyAttentionAfterHistoryReplacement(t *testing.T) {
	m := NewModel()
	for i, e := range []domain.Event{
		{Type: "turn", Turn: &domain.Turn{ID: "old", Order: 3, Status: "completed"}},
		{Type: "history.reset"},
		{Type: "turn", Turn: &domain.Turn{ID: "replacement", Order: 1, Status: "completed"}},
	} {
		e.Sequence, e.SessionID = int64(i+1), "s"
		if err := m.ApplyEvent(e); err != nil {
			t.Fatal(err)
		}
		if i == 1 && m.HasCompletedReply("s") {
			t.Fatal("destroyed suffix kept its completion attention")
		}
	}
	if !m.HasCompletedReply("s") {
		t.Fatal("replacement reply with a reused turn order was suppressed")
	}
}

func TestCompletedReplyAttentionReconnectAfterAnotherClientRewritesHistory(t *testing.T) {
	for _, unread := range []bool{false, true} {
		t.Run(map[bool]string{false: "read", true: "unread"}[unread], func(t *testing.T) {
			m := NewModel()
			before := domain.Session{ID: "s", ThreadID: "old-history", Status: "completed"}
			m.ApplySnapshot(domain.Snapshot{Sessions: []domain.Session{before}})
			if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "turn", SessionID: "s", Session: &before, Turn: &domain.Turn{ID: "old-turn", Order: 5, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			if !unread {
				m.ReadCompletedReply("s")
			}
			path := filepath.Join(t.TempDir(), "client.json")
			if err := m.Save(path); err != nil {
				t.Fatal(err)
			}
			restored := NewModel()
			if err := restored.Load(path); err != nil {
				t.Fatal(err)
			}
			restored.ApplySnapshot(domain.Snapshot{Sequence: 10, Sessions: []domain.Session{before}})
			if restored.HasCompletedReply("s") {
				t.Fatal("restart restored completion attention")
			}
			// A bootstrap snapshot skips the reset journal event from client B.
			replacement := domain.Session{ID: "s", ThreadID: "replacement-history", Status: "running"}
			restored.ApplySnapshot(domain.Snapshot{Sequence: 20, Sessions: []domain.Session{replacement}})
			if restored.HasCompletedReply("s") {
				t.Fatal("old history retained unread completion after snapshot replacement")
			}
			if err := restored.ApplyEvent(domain.Event{Sequence: 21, Type: "turn", SessionID: "s", Session: &replacement, Turn: &domain.Turn{ID: "new-turn", Order: 2, Status: "completed"}}); err != nil {
				t.Fatal(err)
			}
			if !restored.HasCompletedReply("s") {
				t.Fatal("old history watermark suppressed the replacement reply")
			}
		})
	}
}

// Decode the public worker projection so these tests also exercise its wire shape.
func completionSession(t *testing.T, id string, at time.Time, unread bool) domain.Session {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": "s", "threadId": "history", "status": "completed", "completion": map[string]any{"id": id, "historyRef": "history", "turnId": id, "order": 1, "completedAt": at, "unread": unread}})
	if err != nil {
		t.Fatal(err)
	}
	var session domain.Session
	if err = json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestCompletionAttentionUsesWorkerReadStateAndStartupBoundary(t *testing.T) {
	m := NewModel()
	old := completionSession(t, "old", time.Now().Add(-time.Hour), true)
	m.ApplySnapshot(domain.Snapshot{Sessions: []domain.Session{old}})
	if m.HasCompletedReply("s") {
		t.Fatal("pre-startup completion entered attention")
	}
	live := completionSession(t, "live", time.Now().Add(time.Second), true)
	m.ApplySnapshot(domain.Snapshot{Sequence: 5, Sessions: []domain.Session{live}})
	if !m.HasCompletedReply("s") {
		t.Fatal("completion during connection loss was missed by snapshot")
	}
	read := completionSession(t, "live", time.Now().Add(time.Second), false)
	if err := m.ApplyEvent(domain.Event{Sequence: 6, Type: "session", SessionID: "s", Session: &read}); err != nil {
		t.Fatal(err)
	}
	if m.HasCompletedReply("s") {
		t.Fatal("another client's read event did not clear attention")
	}
	// Late usage/completion hydration cannot resurrect an acknowledged reply.
	if err := m.ApplyEvent(domain.Event{Sequence: 7, Type: "turn", SessionID: "s", Session: &read, Turn: &domain.Turn{ID: "live", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	if m.HasCompletedReply("s") {
		t.Fatal("late completion resurrected an acknowledged reply")
	}
}

func TestCompletionStartupBoundaryUsesEachWorkersClock(t *testing.T) {
	for _, offset := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		m := NewModel()
		m.startedAt = time.Now().Add(-time.Minute)
		now := time.Now().Add(offset)
		old := completionSession(t, "old", now.Add(-2*time.Minute), true)
		m.ApplySnapshot(domain.Snapshot{DeviceID: "remote", ObservedAt: now, Sessions: []domain.Session{old}})
		if m.HasCompletedReply("s") {
			t.Fatal("worker clock skew included a pre-startup reply", offset)
		}
		live := completionSession(t, "live", now.Add(-30*time.Second), true)
		m.ApplySnapshot(domain.Snapshot{DeviceID: "remote", Sequence: 2, ObservedAt: now, Sessions: []domain.Session{live}})
		if !m.HasCompletedReply("s") {
			t.Fatal("worker clock skew excluded a post-startup reply", offset)
		}
	}
}

func TestCompletionStartupBoundaryExcludesReadinessAndUIQueueDelay(t *testing.T) {
	m := NewModel()
	now := time.Now()
	m.startedAt = now.Add(-10 * time.Second)
	requestedAt := now.Add(-9 * time.Second)
	workerSnapshotAt := now.Add(-24*time.Hour - 9*time.Second)
	old := completionSession(t, "old", now.Add(-24*time.Hour-11*time.Second), true)
	m.ApplySnapshot(domain.Snapshot{DeviceID: "remote", ObservedAt: workerSnapshotAt, Sessions: []domain.Session{old}}, requestedAt)
	if m.HasCompletedReply("s") {
		t.Fatal("readiness/UI delay admitted a prelaunch reply")
	}
	live := completionSession(t, "live", now.Add(-24*time.Hour-8*time.Second), true)
	m.ApplySnapshot(domain.Snapshot{DeviceID: "remote", Sequence: 2, ObservedAt: now.Add(-24 * time.Hour), Sessions: []domain.Session{live}}, now)
	if !m.HasCompletedReply("s") {
		t.Fatal("later snapshot moved the original launch boundary")
	}
}

func TestAuthoritativeSnapshotClearsRemovedCompletionWithoutChangingSourceID(t *testing.T) {
	m := NewModel()
	m.WorkerMinor = 39
	live := completionSession(t, "removed", time.Now().Add(time.Second), true)
	m.ApplySnapshot(domain.Snapshot{Sessions: []domain.Session{live}})
	if !m.HasCompletedReply("s") {
		t.Fatal("fixture completion missing")
	}
	live.Completion = nil
	m.ApplySnapshot(domain.Snapshot{Sequence: 4, Sessions: []domain.Session{live}})
	if m.HasCompletedReply("s") {
		t.Fatal("authoritative removal kept stale attention")
	}
	if err := m.ApplyEvent(domain.Event{Sequence: 5, SessionID: "s", Type: "turn", Session: &live, Turn: &domain.Turn{ID: "removed", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	if m.HasCompletedReply("s") {
		t.Fatal("source turn bypassed authoritative worker completion state")
	}
}

func TestLegacyClientUnreadStateIsIgnoredOnLaunch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(path, []byte(`{"completedReplies":{"s":{"turnId":"old","unread":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewModel()
	if err := m.Load(path); err != nil {
		t.Fatal(err)
	}
	if m.HasCompletedReply("s") {
		t.Fatal("legacy state restored old completion attention")
	}
}

func TestCompletionReadReceiptKeepsNewerReplyAndFailureRestoresAttention(t *testing.T) {
	m := NewModel()
	live := completionSession(t, "first", time.Now().Add(time.Second), true)
	live.WorkspaceID, live.Revision = "w", 1
	m.ApplySnapshot(domain.Snapshot{Workspaces: []domain.Workspace{{ID: "w"}}, Sessions: []domain.Session{live}})
	job, ok := m.CompletionReadJob("s")
	if !ok || job.ConnectionID != "local" || job.Input.(protocol.CompletionReadInput).CompletionID != "first" {
		t.Fatal("read not routed to owning worker", job)
	}
	if m.HasCompletedReply("s") {
		t.Fatal("opening did not consume local attention")
	}
	job.Apply(m, nil, errors.New("offline"))
	if !m.HasCompletedReply("s") || m.Error == "" {
		t.Fatal("failed acknowledgement silently lost attention")
	}
	job, _ = m.CompletionReadJob("s")
	newer := completionSession(t, "second", time.Now().Add(2*time.Second), true)
	newer.WorkspaceID, newer.Revision = "w", 3
	if err := m.ApplyEvent(domain.Event{Sequence: 1, Type: "session", SessionID: "s", Session: &newer}); err != nil {
		t.Fatal(err)
	}
	live.Completion.Unread, live.Revision = false, 2
	raw, _ := json.Marshal(live)
	job.Apply(m, raw, nil)
	if !m.HasCompletedReply("s") || m.Snapshot.Sessions[0].Completion.ID != "second" {
		t.Fatal("late acknowledgement consumed a newer reply")
	}
}

func TestCompletionReadRoutesToAnAuthenticatedSingleDevice(t *testing.T) {
	model := NewModel()
	owner := domain.Workspace{ID: "workspace", DeviceID: "remote"}
	reply := domain.Session{ID: "session", WorkspaceID: owner.ID, ThreadID: "history", Status: domain.ExecutionCompleted,
		Context: domain.LaunchContext{DeviceID: owner.DeviceID}, Completion: &domain.ReplyCompletion{ID: "reply", HistoryRef: "history",
			CompletedAt: time.Now(), Unread: true}}
	model.ApplySnapshot(domain.Snapshot{DeviceID: "remote", Workspaces: []domain.Workspace{owner}, Sessions: []domain.Session{reply}})
	job, ok := model.CompletionReadJob(reply.ID)
	if !ok || job.ConnectionID != owner.DeviceID || job.Input.(protocol.CompletionReadInput).CompletionID != "reply" {
		t.Fatalf("could not target the owning single-device connection: %+v", job)
	}
	that := reply
	that.Context.DeviceID = "unrelated"
	that.Revision++
	that.Completion = &domain.ReplyCompletion{ID: reply.Completion.ID, HistoryRef: reply.ThreadID}
	raw, err := json.Marshal(that)
	if err != nil {
		t.Fatal(err)
	}
	job.Apply(model, raw, nil)
	if !model.HasCompletedReply(reply.ID) || model.Snapshot.Sessions[0].Context.DeviceID != owner.DeviceID {
		t.Fatal("a receipt with the wrong device identity marked the reply as read")
	}
}

func TestDeviceSelectionKeepsTheLaunchClockBoundary(t *testing.T) {
	model := NewModel()
	clock := model.startedAt.Add(-3 * time.Hour)
	snapshot := domain.Snapshot{DeviceID: "remote", ObservedAt: clock, Sessions: []domain.Session{
		completionSession(t, "old", clock.Add(-2*time.Minute), true),
	}}
	model.CompletionCutoffForSnapshot(snapshot, model.startedAt.Add(time.Minute))
	snapshot.Sessions = append(snapshot.Sessions, completionSession(t, "new", clock.Add(-30*time.Second), true))
	snapshot.Sessions[1].ID = "next"
	selection := model.NewDeviceSelection()
	snapshot.ObservedAt = clock.Add(2 * time.Hour)
	selection.ApplySnapshot(snapshot)
	if selection.HasCompletedReply("s") || !selection.HasCompletedReply("next") {
		t.Fatal("switching devices changed which replies belong to the current launch")
	}
}

func TestCompletionReadRejectsReplyFromAnotherDevice(t *testing.T) {
	m := NewModel()
	s := completionSession(t, "reply", time.Now(), true)
	s.WorkspaceID = "w"
	m.DeviceStates["air"] = &DeviceState{Snapshot: domain.Snapshot{DeviceID: "B", Sessions: []domain.Session{s}}}
	job := Job{Method: "session.completion.read", Input: protocol.CompletionReadInput{SessionID: "s", CompletionID: "reply"}}
	wrong := s
	wrong.Context.DeviceID = "A"
	body, _ := json.Marshal(wrong)
	if m.validateDeviceReply("air", job, body) == nil {
		t.Fatal("accepted another device's completion receipt")
	}
}

func TestCompletionReadReceiptUpdatesOwningDeviceWhileAnotherIsSelected(t *testing.T) {
	m := NewModel()
	m.Connections = []DeviceConnection{{ID: "air", DeviceID: "B"}}
	m.ApplyDeviceUpdate("local", func(p *Model) { p.ApplySnapshot(domain.Snapshot{DeviceID: "A"}) })
	live := completionSession(t, "reply", time.Now().Add(time.Second), true)
	live.WorkspaceID, live.Revision = "wB", 1
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Workspaces: []domain.Workspace{{ID: "wB", DeviceID: "B"}}, Sessions: []domain.Session{live}})
	})
	job, ok := m.CompletionReadJob("s")
	if !ok || job.ConnectionID != "air" {
		t.Fatal("read routed to selected device", job)
	}
	readCompletion := *live.Completion
	live.Completion = &readCompletion
	live.Completion.Unread, live.Revision = false, 2
	raw, _ := json.Marshal(live)
	job.Apply(m, raw, nil)
	m.RefreshDevices()
	if m.DeviceStates["air"].Snapshot.Sessions[0].Completion.Unread || m.HasCompletedReply("s") {
		t.Fatal("read receipt was overwritten by unrelated device refresh")
	}
}
