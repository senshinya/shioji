package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"testing"
	"time"
)

func deviceUIModel() *app.Model {
	m := layoutModel()
	m.LocalDeviceID = "A"
	m.Connections = []app.DeviceConnection{{ID: "air", Name: "MacBook Air", DeviceID: "B", Home: "/Users/remote", SSH: transport.SSH{Target: "remote@air", Worker: "/worker"}}}
	m.ApplyDeviceUpdate("local", func(p *app.Model) {
		p.Connected = true
		p.ConnectionEpoch = "a"
		p.ApplySnapshot(domain.Snapshot{DeviceID: "A", Generation: "a", Workspaces: []domain.Workspace{{ID: "w", DeviceID: "A", Name: "Local"}}})
	})
	m.ApplyDeviceUpdate("air", func(p *app.Model) {
		p.Connected = true
		p.ConnectionEpoch = "b"
		p.Readiness = domain.Readiness{State: "ready"}
		p.ApplySnapshot(domain.Snapshot{DeviceID: "B", Generation: "b", Workspaces: []domain.Workspace{{ID: "wb", DeviceID: "B", Name: "Remote"}}})
	})
	return m
}
func TestDevicesPageUsesCompactCollectionAndSelectedSSHDetails(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1440, 900)
	if _, ok := ui.Find("设备列表"); !ok {
		t.Fatal("device collection missing")
	}
	if (!ui.HasText("remote") || !ui.HasText("air")) || !ui.HasText("MacBook Air") || !ui.HasText("SSH") {
		t.Fatal("selected device connection details missing")
	}
	if _, ok := ui.Find("添加设备"); !ok {
		t.Fatal("add device missing from toolbar")
	}
}
func TestRemoteWorkspaceInputRoutesOnlyToOwningDevice(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID = "wb"
	m.SessionID = ""
	m.ActiveConnection = "air"
	m.RefreshDevices()
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1440, 900)
	v.createWorkspace()
	ui.Frame()
	if _, ok := ui.Find("远程主目录"); !ok {
		t.Fatal("remote workspace opened local folder picker")
	}
	ui.Click("远程主目录")
	ui.Type("/Users/remote/project")
	ui.Click("创建远程工作区")
	var found *app.Job
	for len(c.Jobs) > 0 {
		j := <-c.Jobs
		if j.Method == "workspace.create" {
			found = &j
		}
	}
	if found == nil || found.ConnectionID != "air" || found.ConnectionEpoch != "b" {
		t.Fatal("workspace request not bound to Air", found, "path", v.remoteWorkspacePath, "error", v.remoteWorkspaceError, "busy", v.remoteWorkspaceBusy)
	}
	in := found.Input.(protocol.WorkspaceInput)
	if len(in.Folders) != 1 || in.Folders[0] != "/Users/remote/project" {
		t.Fatal("remote path reinterpreted", in)
	}
}

func TestRemoteWorkspaceResultDoesNotNavigateAfterDialogDismissal(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID = "wb"
	m.SessionID = ""
	m.ActiveConnection = "air"
	m.RefreshDevices()
	c := app.NewController("")
	v := New(m, c, "")
	v.remoteWorkspaceOpen = true
	v.createWorkspaceOn("air", protocol.WorkspaceInput{Name: "New", Folders: []string{"/Users/remote/new"}})
	var job app.Job
	for len(c.Jobs) > 0 {
		j := <-c.Jobs
		if j.Method == "workspace.create" {
			job = j
		}
	}
	if job.Apply == nil {
		t.Fatal("missing create request")
	}
	v.remoteWorkspaceOpen = false
	m.WorkspaceID = "w"
	m.ActiveConnection = "local"
	b, _ := json.Marshal(domain.Workspace{ID: "new", DeviceID: "B", Name: "New", Folders: []string{"/Users/remote/new"}})
	job.Apply(m, b, nil)
	if m.WorkspaceID != "w" || m.ActiveConnection != "local" {
		t.Fatal("late workspace result steals navigation", m.WorkspaceID, m.ActiveConnection)
	}
}

func TestDisconnectedConversationDoesNotClaimLiveExecution(t *testing.T) {
	m := layoutModel()
	m.Connected = false
	d := m.Details["s"]
	d.Items[0].TurnID = "t"
	now := time.Now()
	d.Turns = []domain.Turn{{ID: "t", Status: "running", StartedAt: &now}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if !ui.HasText("连接已断开，执行状态待核对") {
		t.Fatal("disconnected turn still presented as live")
	}
	if _, ok := ui.Find("进行中状态"); ok {
		t.Fatal("disconnected turn still pulses green")
	}
}

func TestOldWorkspaceResultCannotCloseNewDeviceDialog(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID = "w"
	m.SessionID = ""
	m.ActiveConnection = "local"
	m.RefreshDevices()
	c := app.NewController("")
	v := New(m, c, "")
	v.beginWorkspace("air")
	v.createWorkspaceOn("air", protocol.WorkspaceInput{Name: "Old", Folders: []string{"/Users/remote/old"}})
	var old app.Job
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "workspace.create" {
			old = job
		}
	}
	v.remoteWorkspaceOpen = false
	v.beginWorkspace("air")
	v.remoteWorkspacePath = "/Users/remote/new"
	v.remoteWorkspaceBusy = true
	b, _ := json.Marshal(domain.Workspace{ID: "old", DeviceID: "B", Name: "Old", Folders: []string{"/Users/remote/old"}})
	old.Apply(m, b, nil)
	if !v.remoteWorkspaceOpen || !v.remoteWorkspaceBusy || v.remoteWorkspacePath != "/Users/remote/new" || m.WorkspaceID != "w" {
		t.Fatal("old callback changed new dialog or navigation")
	}
}

func TestGlobalSearchCanUseOnlineDeviceWhileSelectedDeviceOffline(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID = "wb"
	m.SessionID = ""
	m.ActiveConnection = "air"
	m.ApplyDeviceUpdate("air", func(p *app.Model) { p.Connected = false; p.ConnectionEpoch = "" })
	c := app.NewController("")
	v := New(m, c, "")
	v.searchOpen = true
	v.searchText = "find across devices"
	n.NewTester(v.View, 1440, 900)
	found := false
	for len(c.Jobs) > 0 {
		if j := <-c.Jobs; j.Method == "session.search" {
			found = true
		}
	}
	if !found {
		t.Fatal("offline selection blocks global search of online devices")
	}
}

func TestNarrowDeviceViewCanReturnToCollectionAndSelectDevice(t *testing.T) {
	m := deviceUIModel()
	v := New(m, nil, "")
	v.devices = true
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 850, 650)
	if _, ok := ui.Find("返回设备列表"); !ok {
		t.Fatal("narrow device detail has no Back")
	}
	ui.Click("返回设备列表")
	ui.Frame()
	if _, ok := ui.Find("设备列表"); !ok {
		t.Fatal("Back does not reveal device collection")
	}
	if err := ui.Click("选择设备：MacBook Air"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if _, ok := ui.Find("返回设备列表"); !ok {
		t.Fatal("device selection does not reopen detail")
	}
}
