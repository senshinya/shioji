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

func TestSidebarReloadKeepsSelectionAndDraftAndTargetsClickedSession(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 45
	m.Readiness.Capabilities.Continue = true
	m.Readiness.Capabilities.Reload = true
	m.Snapshot.Sessions[0].ThreadID = "original-thread"
	m.Snapshot.Sessions[0].Revision = 9
	other := m.Snapshot.Sessions[0]
	other.ID, other.Title, other.ThreadID = "other", "Other conversation", "other-thread"
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, other)
	m.Drafts["session:s"] = "keep current draft"
	m.Drafts["session:other"] = "keep other draft"
	c := app.NewController("")
	ui := n.NewTester(New(m, c, "").View, 1440, 900)
	if err := ui.RightClick("Other conversation"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("重新加载"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for len(c.Jobs) > 0 {
		j := <-c.Jobs
		if j.Method != "session.reload" {
			continue
		}
		b, _ := json.Marshal(j.Input)
		var in struct {
			SessionID, OperationID string
			Revision               int64
		}
		if err := json.Unmarshal(b, &in); err != nil {
			t.Fatal(err)
		}
		if in.SessionID != "other" || in.OperationID == "" || in.Revision != 9 || j.ConnectionID != "local" {
			t.Fatal("reload targeted another session", string(b), j.ConnectionID)
		}
		found = true
	}
	if !found || m.SessionID != "s" || m.Drafts["session:s"] != "keep current draft" || m.Drafts["session:other"] != "keep other draft" {
		t.Fatal("reload lost its command, selection or draft")
	}
}

func TestSidebarReloadDisabledForBusyOfflineAndOldWorkers(t *testing.T) {
	for _, state := range []string{"running", "unknown", "pending", "offline", "old-worker", "continue-only"} {
		t.Run(state, func(t *testing.T) {
			m := layoutModel()
			m.WorkerMinor = 45
			m.Readiness.Capabilities.Continue = true
			m.Readiness.Capabilities.Reload = true
			s := m.Snapshot.Sessions[0]
			s.ThreadID = "source"
			s.Revision = 7
			if state == "running" || state == "unknown" {
				s.Status = domain.ExecutionState(state)
			}
			m.Snapshot.Sessions[0] = s
			d := m.Details[s.ID]
			d.Session = s
			if state == "pending" {
				d.Requests = []domain.PendingRequest{{ID: "q", Kind: "questions", State: "pending"}}
			}
			m.Details[s.ID] = d
			if state == "offline" {
				m.Connected = false
			}
			if state == "continue-only" {
				m.Readiness.Capabilities.Reload = false
			}
			if state == "old-worker" {
				m.WorkerMinor = 44
			}
			c := app.NewController("")
			ui := n.NewTester(New(m, c, "").View, 1440, 900)
			if err := ui.RightClick("Layout"); err != nil {
				t.Fatal(err)
			}
			_ = ui.ChooseMenuItem("重新加载")
			for len(c.Jobs) > 0 {
				if job := <-c.Jobs; job.Method == "session.reload" {
					t.Fatal("disabled reload executed")
				}
			}
		})
	}
}

func TestSidebarReloadUncertainDeliveryOnlyQueriesOriginalOperation(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 45
	m.Readiness.Capabilities.Continue = true
	m.Readiness.Capabilities.Reload = true
	s := m.Snapshot.Sessions[0]
	s.ThreadID = "source"
	s.Revision = 7
	m.Snapshot.Sessions[0] = s
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.RightClick("Layout")
	ui.ChooseMenuItem("重新加载")
	job := historyJob(t, v, "session.reload")
	job.Apply(m, nil, errors.New("connection lost before receipt"))
	pending := v.sessionReloads[s.ID]
	if pending == nil {
		t.Fatal("uncertain reload lost its operation")
	}
	pending.queryAfter = time.Time{}
	ui.RightClick("Layout")
	query := historyJob(t, v, "session.reload.operation")
	if query.Input != job.Input {
		t.Fatal("reconnect created a new reload")
	}
	result := domain.SessionReloadResult{Operation: domain.SessionReloadOperation{Input: job.Input.(domain.SessionReloadRequest), State: "completed"}, Session: s}
	b, _ := json.Marshal(result)
	query.Apply(m, b, nil)
	if v.sessionReloads[s.ID] != nil {
		t.Fatal("confirmed receipt stayed pending")
	}
}

func TestSidebarReloadUsesWorkspaceOwningDevice(t *testing.T) {
	m := layoutModel()
	m.LocalDeviceID = "A"
	m.WorkerMinor = 45
	m.Readiness.Capabilities.Continue = true
	m.Readiness.Capabilities.Reload = true
	m.Connections = []app.DeviceConnection{{ID: "air", DeviceID: "B"}}
	m.Snapshot.DeviceID = "A"
	m.Snapshot.Workspaces[0].DeviceID = "A"
	m.ApplyDeviceUpdate("local", func(p *app.Model) {
		p.Connected = true
		p.WorkerMinor = 45
		p.Readiness = m.Readiness
		p.ApplySnapshot(m.Snapshot)
	})
	s := domain.Session{ID: "remote", WorkspaceID: "remote-workspace", ThreadID: "remote-source", Title: "Remote conversation", Revision: 8, Status: "completed", Context: domain.LaunchContext{DeviceID: "B"}}
	m.ApplyDeviceUpdate("air", func(p *app.Model) {
		p.Connected = true
		p.WorkerMinor = 45
		p.Readiness = m.Readiness
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Workspaces: []domain.Workspace{{ID: s.WorkspaceID, DeviceID: "B", Name: "Remote workspace"}}, Sessions: []domain.Session{s}})
	})
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1440, 900)
	if err := ui.RightClick(s.Title); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("重新加载"); err != nil {
		t.Fatal(err)
	}
	job := historyJob(t, v, "session.reload")
	if job.ConnectionID != "air" || job.Input.(domain.SessionReloadRequest).SessionID != "remote" || m.SessionID != "s" {
		t.Fatal("remote reload changed selection or used the controller Mac", job.ConnectionID, job.Input, m.SessionID)
	}
}
