package ui

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

func TestWorkspaceCreationMenuWorksWhileSelectedDeviceOffline(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID, m.ActiveConnection, m.SessionID = "wb", "air", ""
	m.ApplyDeviceUpdate("air", func(p *app.Model) { p.Connected = false })
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	if err := ui.Click("创建工作区"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("新建工作区到") {
		t.Fatal("global + still depends on the selected connection")
	}
	if _, ok := ui.Find("在 MacBook Air 新建工作区"); !ok {
		t.Fatal("known offline device missing")
	}
	if _, ok := ui.Find("在 " + m.LocalName() + " 新建工作区"); !ok {
		t.Fatal("local device missing")
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(time.Second)
	if ui.HasText("新建工作区到") || !ui.Focused("创建工作区") {
		t.Fatal("Esc did not dismiss and return focus")
	}
}

func TestWorkspaceCreationSelectsAnotherDeviceWithoutNavigating(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID, m.ActiveConnection = "w", "local"
	m.Drafts["home"], m.Drafts["s"] = "home draft", "session draft"
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("创建工作区")
	ui.Advance(time.Second)
	if err := ui.Click("在 MacBook Air 新建工作区"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("新建工作区") || !ui.HasText("MacBook Air") {
		t.Fatal("target not identified in creation form")
	}
	if m.WorkspaceID != "w" || m.ActiveConnection != "local" {
		t.Fatal("selecting device navigated before acceptance")
	}
	v.remoteWorkspacePath = "/Users/remote/project"
	v.submitRemoteWorkspace()
	job := welcomeJob(t, c, "workspace.create")
	if job.ConnectionID != "air" || job.ConnectionEpoch != "b" {
		t.Fatal("wrong owning device", job)
	}
	body, _ := json.Marshal(domain.Workspace{ID: "created", DeviceID: "B", Name: "Project", Folders: []string{"/Users/remote/project"}})
	job.Apply(m, body, nil)
	if m.WorkspaceID != "created" || m.ActiveConnection != "air" || m.SessionID != "" {
		t.Fatal("accepted workspace did not open its home")
	}
	if m.Drafts["home"] != "home draft" || m.Drafts["s"] != "session draft" || len(m.Pending) != 0 {
		t.Fatal("creation changed drafts or created a session")
	}
}

func TestWorkspaceCreationKeyboardStartsOnCurrentDevice(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID, m.ActiveConnection, m.SessionID = "wb", "air", ""
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("创建工作区")
	ui.Advance(time.Second)
	if !ui.Focused("在 MacBook Air 新建工作区") {
		t.Fatal("current device is not initially focused")
	}
	ui.Key(0, n.KeyEnter)
	ui.Advance(time.Second)
	if _, ok := ui.Find("远程主目录"); !ok {
		t.Fatal("Enter did not choose current device")
	}
}

func TestWorkspaceCreationLocalUsesSystemPickerOnLocalConnection(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID, m.ActiveConnection, m.SessionID = "wb", "air", ""
	c := app.NewController("")
	v := New(m, c, "")
	before := openWorkspaceDirectoryDialog
	t.Cleanup(func() { openWorkspaceDirectoryDialog = before })
	called := false
	openWorkspaceDirectoryDialog = func(o mygo.OpenDialogOptions) ([]string, error) {
		called = true
		if !o.Directory || !o.Multiple {
			t.Fatal("local directory picker changed semantics")
		}
		return []string{"/Users/local/project"}, nil
	}
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("创建工作区")
	ui.Advance(time.Second)
	if err := ui.Click("在 " + m.LocalName() + " 新建工作区"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("native picker not used")
	}
	job := welcomeJob(t, c, "workspace.create")
	if job.ConnectionID != "local" || job.ConnectionEpoch != "a" {
		t.Fatal("local choice routed through remote", job)
	}
}

func TestWorkspaceCreationCanceledReconnectCannotOpenLater(t *testing.T) {
	m := deviceUIModel()
	m.ApplyDeviceUpdate("air", func(p *app.Model) { p.Connected = false; p.Error = "offline" })
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("创建工作区")
	ui.Advance(time.Second)
	if err := ui.Click("在 MacBook Air 新建工作区"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, c, "$reconnect")
	if job.ConnectionID != "air" {
		t.Fatal("reconnected selected conversation instead of target")
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(time.Second)
	m.ApplyDeviceUpdate("air", func(p *app.Model) { p.Connected = true; p.ConnectionEpoch = "new" })
	ui.Frame()
	ui.Advance(time.Second)
	if v.remoteWorkspaceOpen {
		t.Fatal("late connection reopened canceled creation")
	}
}

func TestWorkspaceCreationReconnectRetriesInPlace(t *testing.T) {
	m := deviceUIModel()
	m.ApplyDeviceUpdate("air", func(p *app.Model) { p.Connected = false })
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("创建工作区")
	ui.Advance(time.Second)
	if err := ui.Click("在 MacBook Air 新建工作区"); err != nil {
		t.Fatal(err)
	}
	welcomeJob(t, c, "$reconnect")
	m.DeviceStates["air"].Error = "connection refused"
	ui.Advance(11 * time.Second)
	if !ui.HasText("connection refused") {
		t.Fatal("target error not shown in menu")
	}
	if err := ui.Click("重试连接工作区设备"); err != nil {
		t.Fatal(err)
	}
	welcomeJob(t, c, "$reconnect")
	m.ApplyDeviceUpdate("air", func(p *app.Model) { p.Connected = true; p.ConnectionEpoch = "retry" })
	ui.Frame()
	ui.Advance(time.Second)
	if !v.remoteWorkspaceOpen || v.remoteWorkspaceConnection != "air" {
		t.Fatal("successful reconnect did not continue")
	}
}

func TestWorkspaceCreationDraftsDoNotCrossDevices(t *testing.T) {
	m := deviceUIModel()
	m.Connections = append(m.Connections, app.DeviceConnection{ID: "studio", DeviceID: "C", Name: "Studio"})
	m.ApplyDeviceUpdate("studio", func(p *app.Model) { p.Connected = true; p.ConnectionEpoch = "c" })
	v := New(m, app.NewController(""), "")
	v.beginWorkspace("air")
	v.remoteWorkspaceName = "Air draft"
	v.remoteWorkspacePath = "/Users/remote/air"
	v.remoteWorkspaceExtra = "/Users/remote/extra"
	v.remoteWorkspaceOpen = false
	v.beginWorkspace("studio")
	if v.remoteWorkspaceName != "" || v.remoteWorkspacePath != "" || v.remoteWorkspaceExtra != "" {
		t.Fatal("another device inherited remote directories")
	}
	v.remoteWorkspaceName = "Studio draft"
	v.remoteWorkspaceOpen = false
	v.beginWorkspace("air")
	if v.remoteWorkspaceName != "Air draft" || v.remoteWorkspacePath != "/Users/remote/air" {
		t.Fatal("same-device canceled draft lost")
	}
}

func TestWorkspaceCreationRejectsForeignAndStaleReceipts(t *testing.T) {
	for _, mode := range []string{"foreign", "epoch", "dismissed"} {
		t.Run(mode, func(t *testing.T) {
			m := deviceUIModel()
			m.WorkspaceID, m.ActiveConnection = "w", "local"
			c := app.NewController("")
			v := New(m, c, "")
			v.beginWorkspace("air")
			v.createWorkspaceOn("air", protocol.WorkspaceInput{Name: "New", Folders: []string{"/Users/remote/new"}})
			job := welcomeJob(t, c, "workspace.create")
			id := "B"
			switch mode {
			case "foreign":
				id = "C"
			case "epoch":
				m.DeviceStates["air"].ConnectionEpoch = "replacement"
			case "dismissed":
				v.remoteWorkspaceOpen = false
			}
			body, _ := json.Marshal(domain.Workspace{ID: "new", DeviceID: id, Name: "New", Folders: []string{"/Users/remote/new"}})
			job.Apply(m, body, nil)
			if m.WorkspaceID != "w" || m.ActiveConnection != "local" {
				t.Fatal("unrelated receipt stole navigation")
			}
		})
	}
}

func TestWorkspaceCreationErrorStaysInOwningForm(t *testing.T) {
	m := deviceUIModel()
	c := app.NewController("")
	v := New(m, c, "")
	v.beginWorkspace("air")
	v.remoteWorkspacePath = "/Users/remote/new"
	v.submitRemoteWorkspace()
	job := welcomeJob(t, c, "workspace.create")
	job.Apply(m, nil, errors.New("path missing"))
	if !v.remoteWorkspaceOpen || v.remoteWorkspaceBusy || v.remoteWorkspaceError != "path missing" || v.remoteWorkspacePath != "/Users/remote/new" {
		t.Fatal("failure did not preserve editable target form")
	}
}

func TestWorkspaceCreationAddDeviceWaitsForItsWorker(t *testing.T) {
	m := deviceUIModel()
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("创建工作区")
	ui.Advance(time.Second)
	if err := ui.Click("添加设备…"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !v.sshCheck.open() || v.devices {
		t.Fatal("add action did not reuse onboarding in place")
	}
	v.sshRequest = "validated-device"
	v.saveDeviceConnection(app.DeviceConnection{ID: "studio", DeviceID: "C", Name: "Studio", SSH: transport.SSH{Target: "user@studio"}}, false)
	ui.Frame()
	if v.remoteWorkspaceOpen {
		t.Fatal("connection persistence was mistaken for worker readiness")
	}
	m.ApplyDeviceUpdate("studio", func(p *app.Model) {
		p.Connected = true
		p.ConnectionEpoch = "studio-ready"
		p.ApplySnapshot(domain.Snapshot{DeviceID: "C"})
	})
	ui.Frame()
	ui.Advance(time.Second)
	if !v.remoteWorkspaceOpen || v.remoteWorkspaceConnection != "studio" {
		t.Fatal("saved and connected device did not continue creation")
	}
}

func TestWorkspaceCreationCancelAddDoesNotContinueAfterSave(t *testing.T) {
	m := deviceUIModel()
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("创建工作区")
	ui.Advance(time.Second)
	if err := ui.Click("添加设备…"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	result := make(chan error, 1)
	v.sshRequest = "saving"
	v.deviceConnectionSave = &deviceConnectionSave{connection: app.DeviceConnection{ID: "air", DeviceID: "B", Name: "MacBook Air"}, request: "saving", result: result}
	v.dismissDeviceSetup()
	result <- nil
	ui.Frame()
	ui.Advance(time.Second)
	if v.remoteWorkspaceOpen || ui.HasText("新建工作区到") {
		t.Fatal("canceled save resumed creation")
	}
}

func TestWorkspaceCreationLocalPickerRejectsChangedConnection(t *testing.T) {
	m := deviceUIModel()
	c := app.NewController("")
	v := New(m, c, "")
	before := openWorkspaceDirectoryDialog
	t.Cleanup(func() { openWorkspaceDirectoryDialog = before })
	openWorkspaceDirectoryDialog = func(mygo.OpenDialogOptions) ([]string, error) {
		m.DeviceStates["local"].ConnectionEpoch = "restarted"
		return []string{"/Users/local/project"}, nil
	}
	v.beginWorkspace("local")
	for len(c.Jobs) > 0 {
		if job := <-c.Jobs; job.Method == "workspace.create" {
			t.Fatal("picker from old connection was submitted to replacement")
		}
	}
}

func TestWorkspaceCreationDismissedMalformedReceiptDoesNotShowError(t *testing.T) {
	m := deviceUIModel()
	c := app.NewController("")
	v := New(m, c, "")
	v.beginWorkspace("air")
	v.createWorkspaceOn("air", protocol.WorkspaceInput{Name: "New", Folders: []string{"/Users/remote/new"}})
	job := welcomeJob(t, c, "workspace.create")
	v.remoteWorkspaceOpen = false
	job.Apply(m, []byte("malformed"), nil)
	if m.Error != "" {
		t.Fatal("dismissed receipt changed current error", m.Error)
	}
}

func TestWorkspaceCreationCanceledAddIgnoresLateSaveFailure(t *testing.T) {
	m := deviceUIModel()
	v := New(m, app.NewController(""), "")
	v.workspaceCreationUI().adding = true
	result := make(chan error, 1)
	v.sshCheck, v.sshRequest = sshSaving, "saving"
	v.deviceConnectionSave = &deviceConnectionSave{connection: app.DeviceConnection{ID: "air", DeviceID: "B"}, request: "saving", result: result}
	v.dismissDeviceSetup()
	result <- errors.New("disk full")
	v.syncDeviceConnectionSave()
	if v.sshCheck.open() {
		t.Fatal("late save failure reopened canceled onboarding")
	}
}

func TestWorkspaceCreationReplacedLocalSuccessOnlyUpdatesCatalog(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID, m.ActiveConnection, m.SessionID = "wb", "air", ""
	c := app.NewController("")
	v := New(m, c, "")
	before := openWorkspaceDirectoryDialog
	t.Cleanup(func() { openWorkspaceDirectoryDialog = before })
	openWorkspaceDirectoryDialog = func(mygo.OpenDialogOptions) ([]string, error) { return []string{"/Users/local/project"}, nil }
	v.beginWorkspace("local")
	job := welcomeJob(t, c, "workspace.create")
	v.createWorkspace()
	m.ApplyDeviceUpdate("air", func(p *app.Model) { p.Connected = false })
	v.selectWorkspaceDevice("air")
	body, _ := json.Marshal(domain.Workspace{ID: "new-local", DeviceID: "A", Name: "Local", Folders: []string{"/Users/local/project"}})
	job.Apply(m, body, nil)
	if m.WorkspaceID != "wb" || m.ActiveConnection != "air" {
		t.Fatal("old local success stole navigation from remote retry")
	}
	found := false
	for _, w := range m.DeviceStates["local"].Snapshot.Workspaces {
		if w.ID == "new-local" {
			found = true
		}
	}
	if !found {
		t.Fatal("late accepted local workspace was lost from owning catalog")
	}
}

func TestWorkspaceCreationEpochChangeReleasesCurrentForm(t *testing.T) {
	m := deviceUIModel()
	c := app.NewController("")
	v := New(m, c, "")
	v.beginWorkspace("air")
	v.remoteWorkspacePath = "/Users/remote/new"
	v.submitRemoteWorkspace()
	job := welcomeJob(t, c, "workspace.create")
	m.DeviceStates["air"].ConnectionEpoch = "replacement"
	job.Apply(m, nil, errors.New("notSent"))
	if v.remoteWorkspaceBusy || v.remoteWorkspaceError == "" || !v.remoteWorkspaceOpen || v.remoteWorkspacePath != "/Users/remote/new" {
		t.Fatal("epoch rejection left form stuck or lost draft")
	}
}
