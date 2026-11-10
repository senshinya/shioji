package app

import (
	"fmt"
	"shioji/internal/domain"
	"testing"
)

func TestWorkEventUpdatesRosterWithoutTouchingParentItems(t *testing.T) {
	m := NewModel()
	m.Details["root"] = domain.SessionDetail{Session: domain.Session{ID: "root", Status: "completed"}, Items: []domain.Item{{ID: "main", Kind: "assistant", Text: "parent reply", Final: true}}}
	w := domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "t", Title: "检查边界", State: "running", Revision: 1}
	if err := m.ApplyEvent(domain.Event{Sequence: 1, SessionID: "root", Type: "work.upsert", Work: &w}); err != nil {
		t.Fatal(err)
	}
	if len(m.Details["root"].Works) != 1 {
		t.Fatal("work roster not projected")
	}
	if err := m.ApplyEvent(domain.Event{Sequence: 2, SessionID: "root", Type: "work.item", WorkID: "child", WorkItem: &domain.Item{ID: "child-msg", Kind: "assistant", Text: "child only"}}); err != nil {
		t.Fatal(err)
	}
	if len(m.Details["root"].Items) != 1 || m.Details["root"].Session.Status != "completed" {
		t.Fatal("child event changed parent transcript or completion")
	}
}

func TestWorkSnapshotCannotEraseLaterLiveItems(t *testing.T) {
	m := NewModel()
	m.Details["s"] = domain.SessionDetail{}
	m.BeginWorkRead("s", "w")
	if err := m.ApplyEvent(domain.Event{Sequence: 1, SessionID: "s", Type: "work.item", WorkID: "w", WorkItem: &domain.Item{ID: "a", Text: "live", Order: 1}}); err != nil {
		t.Fatal(err)
	}
	m.MergeWorkDetail(domain.WorkDetail{SessionID: "s", Work: domain.WorkActivity{ID: "w"}, Sequence: 0, Items: []domain.WorkItem{{Item: domain.Item{ID: "a", Text: "old", Order: 1}, Revision: 0}, {Item: domain.Item{ID: "b", Text: "from snapshot", Order: 2}, Revision: 0}}})
	d := m.WorkDetails[m.WorkKey("s", "w")]
	if len(d.Items) != 2 || d.Items[0].Item.Text != "live" || d.Items[1].Item.Text != "from snapshot" {
		t.Fatal("stale snapshot won race", d)
	}
}

func TestDeviceWorkReadCacheRetainsOwningKeysAndBound(t *testing.T) {
	m := NewModel()
	for _, device := range []string{"A", "B"} {
		m.ApplyDeviceUpdate(device, func(p *Model) {
			p.ApplySnapshot(domain.Snapshot{DeviceID: device, Generation: device, Workspaces: []domain.Workspace{{ID: "w" + device, DeviceID: device}}, Sessions: []domain.Session{{ID: "s" + device, WorkspaceID: "w" + device}}})
			p.MergeWorkDetail(domain.WorkDetail{SessionID: "s" + device, Work: domain.WorkActivity{ID: "same"}, Items: []domain.WorkItem{{Item: domain.Item{ID: "item", Text: device}}}})
		})
	}
	if len(m.WorkDetails) != 2 || m.WorkDetails[WorkKey{"A", "sA", "same"}].Items[0].Item.Text != "A" || m.WorkDetails[WorkKey{"B", "sB", "same"}].Items[0].Item.Text != "B" {
		t.Fatal("device projection lost or mixed detail", m.WorkDetails)
	}
	m.ApplyDeviceUpdate("B", func(p *Model) {
		for i := 0; i < 40; i++ {
			p.BeginWorkRead("sB", fmt.Sprint(i))
		}
	})
	if len(m.WorkDetails) != 32 || len(m.workReads) != 32 {
		t.Fatal("projection bypassed cache bound", len(m.WorkDetails), len(m.workReads))
	}
}
