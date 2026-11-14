package ui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func recentHomeFixture(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	m := layoutModel()
	m.LocalDeviceID = "device-a"
	m.HomeHarnesses = map[string]string{"w": "alpha"}
	config := domain.RunConfig{Model: "a", Effort: "medium", Permission: "read-only"}
	m.Configs["home"] = config
	ready := domain.Readiness{HarnessID: "alpha", Harnesses: []domain.HarnessInfo{
		{ID: "alpha", Name: "Alpha Agent", State: "ready", Permissions: []string{"read-only"}, Models: []domain.Model{{ID: "a", Name: "A Standard", Default: true, Efforts: []string{"medium", "high"}, DefaultEffort: "medium"}, {ID: "a-plus", Name: "A Plus", Efforts: []string{"medium", "high"}, DefaultEffort: "medium"}}},
		{ID: "beta", Name: "Beta Agent", State: "ready", Permissions: []string{"read-only"}, Models: []domain.Model{{ID: "b", Name: "B Standard", Default: true, Efforts: []string{"low", "high"}, DefaultEffort: "low"}, {ID: "b-pro", Name: "B Pro", Efforts: []string{"low", "high"}, DefaultEffort: "low"}}},
	}}
	old := m.Snapshot.Sessions[0]
	old.Title, old.HarnessID, old.Config = "较早会话 · Alpha", "alpha", config
	d := m.Details["s"]
	d.Session = old
	m.Details["s"] = d
	m.DeviceStates = map[string]*app.DeviceState{
		"local":  {Connected: true, ConnectionEpoch: "a", WorkerMinor: 14, Readiness: ready, Snapshot: domain.Snapshot{DeviceID: "device-a", Generation: "a", Workspaces: []domain.Workspace{{ID: "w", DeviceID: "device-a", Name: "本机项目"}, {ID: "same-device", DeviceID: "device-a", Name: "本机另一项目"}}, Sessions: []domain.Session{old, {ID: "latest-a", WorkspaceID: "same-device", Title: "最新会话 · Beta", HarnessID: "beta", Status: "completed", Config: domain.RunConfig{Model: "b-pro", Effort: "high", Permission: "read-only"}}}}},
		"remote": {Connected: true, ConnectionEpoch: "b", WorkerMinor: 14, Readiness: ready, Snapshot: domain.Snapshot{DeviceID: "device-b", Generation: "b", Workspaces: []domain.Workspace{{ID: "remote-workspace", DeviceID: "device-b", Name: "远程项目"}}, Sessions: []domain.Session{{ID: "latest-b", WorkspaceID: "remote-workspace", Title: "远程最新会话", HarnessID: "alpha", Status: "completed", Config: domain.RunConfig{Model: "a-plus", Effort: "high", Permission: "read-only"}}}}},
	}
	m.Connections = []app.DeviceConnection{{ID: "remote", DeviceID: "device-b", Name: "远程 Mac"}}
	m.RefreshDevices()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	return v, ui
}

func TestNewSessionDefaultsFollowItsDevice(t *testing.T) {
	v, ui := recentHomeFixture(t)
	ui.Click("新建会话")
	ui.Advance(time.Second)
	if got := v.Model.Config(); v.Model.HarnessID() != "beta" || got.Model != "b-pro" || got.Effort != "high" {
		t.Fatal("new session uses stale workspace choices", v.Model.HarnessID(), got)
	}
	ui.Click("运行配置")
	ui.Advance(time.Second)
	if !ui.HasText("Beta Agent") || !ui.HasText("b-pro ›") || !ui.HasText("high ›") {
		t.Fatal("native menu does not reflect inherited configuration", ui.Texts())
	}
	ui.Key(0, n.KeyEscape)
	ui.Click("选择工作区")
	ui.Advance(time.Second)
	ui.Key(0, n.KeyDown)
	ui.Key(0, n.KeyDown)
	ui.Key(0, n.KeyEnter)
	ui.Advance(time.Second)
	if got := v.Model.Config(); v.Model.WorkspaceID != "remote-workspace" || v.Model.HarnessID() != "alpha" || got.Model != "a-plus" || got.Effort != "high" {
		t.Fatal("workspace picker did not adopt the owning device's defaults", v.Model.WorkspaceID, v.Model.HarnessID(), got)
	}
}
