package app

import (
	"shioji/internal/domain"
	"testing"
)

func TestFeedbackFromBackgroundDeviceRetainsItsOwningScope(t *testing.T) {
	m := NewModel()
	m.WorkspaceID = "visible"
	m.SessionID = "reading"
	m.ApplyDeviceUpdate("air", func(p *Model) {
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B"}}, Sessions: []domain.Session{{ID: "sb", WorkspaceID: "wb"}}})
		p.ReportFeedback(Feedback{ID: "operation-1", Title: "操作未完成", SessionID: "sb"})
	})
	if len(m.Feedbacks) != 1 {
		t.Fatal("background feedback was dropped", m.Feedbacks)
	}
	f := m.Feedbacks[0]
	if f.ConnectionID != "air" || f.SessionID != "sb" || f.WorkspaceID != "wb" {
		t.Fatal("feedback borrowed selected scope", f)
	}
	if m.SessionID != "reading" || m.WorkspaceID != "visible" {
		t.Fatal("feedback navigated the client")
	}
}
