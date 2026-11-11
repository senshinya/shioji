package app

import (
	"path/filepath"
	"shioji/internal/domain"
	"testing"
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

func TestCompletedReplyAttentionSurvivesClientRestart(t *testing.T) {
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
	if !restored.HasCompletedReply("unread") || restored.HasCompletedReply("read") {
		t.Fatal("restart lost unread replies or resurrected read ones")
	}
	if err := restored.ApplyEvent(domain.Event{Sequence: 1, SessionID: "read", Type: "turn", Turn: &domain.Turn{ID: "read", Order: 1, Status: "completed"}}); err != nil {
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
			if restored.HasCompletedReply("s") != unread {
				t.Fatal("ordinary same-history reconnect changed completion read state")
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
