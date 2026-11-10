package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func workspaceToolFixture(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 26
	info := setupCatalog()[0]
	info.Capabilities.ToolServiceInspection = true
	info.Capabilities.ToolServices = true
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	v := New(m, app.NewController(""), "")
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == "w" {
			w.Folders = []string{"/owning/project", "/owning/notes"}
			m.Snapshot.Workspaces[0] = w
			v.editWorkspace(w)
		}
	}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(800).Height(650).Children(func() { v.workspaceSettingsView(c, c.Theme()) })
	}, 850, 700)
	ui.Advance(time.Second)
	if err := ui.Click("MCP"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	return v, ui
}
func workspaceToolCatalog() domain.ToolServiceInspection {
	return domain.ToolServiceInspection{Directory: "/owning/project", Entries: []domain.InspectedToolService{
		{ToolService: domain.ToolService{ID: "project", Name: "filesystem", State: domain.ToolServiceUnknown, Configuration: &domain.ToolServiceConfiguration{Enabled: true, Transport: "command", Command: "mcp-files"}}, EntryID: "source-project", Scope: domain.ToolServiceProject, ConfigurationPath: "/owning/project/.codex/config.toml"},
		{ToolService: domain.ToolService{ID: "docs", Name: "Context7", State: domain.ToolServiceUnknown, Configuration: &domain.ToolServiceConfiguration{Enabled: true, Transport: "http", URL: "https://example.test/mcp"}}, EntryID: "source-global", Scope: domain.ToolServiceUser, ConfigurationPath: "/owning/user/config.toml"},
	}}
}
func TestWorkspaceToolsNavigation(t *testing.T) {
	v, ui := workspaceToolFixture(t)
	job := welcomeJob(t, v.Controller, "runtime.tools.inspect")
	in := job.Input.(protocol.ToolInspectionInput)
	if in.WorkspaceID != "w" || in.HarnessID != "custom" || job.ConnectionID != "local" || in.SessionID != "" {
		t.Fatal(in)
	}
	body, _ := json.Marshal(workspaceToolCatalog())
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("已配置") || ui.HasText("已连接") {
		t.Fatal("configuration claims connection", ui.Texts())
	}
	if _, ok := ui.Find("启停 MCP：filesystem"); ok {
		t.Fatal("project exposes a write")
	}
	if err := ui.Click("查看 MCP：filesystem"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("/owning/project/.codex/config.toml") {
		t.Fatal("actual config path missing", ui.Texts())
	}
	if err := ui.Click("返回 MCP 列表"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	u := v.workspaceToolsFor(v.workspaceEditor, "custom")
	u.query = "Context7"
	v.workspaceEditor.Workspace.Name = "unsaved"
	ui.Frame()
	if err := ui.Click("在设备设置中管理 MCP"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.skillReturn == nil || v.skillReturn.editor.tab != "MCP" {
		t.Fatal("not routed to native MCP management")
	}
	v.returnSkillWorkspace()
	if v.workspaceEditor.Workspace.Name != "unsaved" || v.workspaceToolsFor(v.workspaceEditor, "custom").query != "Context7" {
		t.Fatal("return lost draft or query")
	}
}
func TestWorkspaceToolsIgnoresReplacedSelectionReply(t *testing.T) {
	v, _ := workspaceToolFixture(t)
	job := welcomeJob(t, v.Controller, "runtime.tools.inspect")
	u := v.workspaceToolsFor(v.workspaceEditor, "custom")
	v.selectWorkspaceToolScope(u, "", "/another/directory")
	body, _ := json.Marshal(workspaceToolCatalog())
	job.Apply(v.Model, body, nil)
	if len(u.catalog.Entries) != 0 {
		t.Fatal("old query overwrote the new directory")
	}
}

func TestWorkspaceToolsRetainsValidScopeAcrossReconnect(t *testing.T) {
	v, _ := workspaceToolFixture(t)
	u := v.workspaceToolsFor(v.workspaceEditor, "custom")
	u.directory = "/owning/notes"
	u.query = "docs"
	u.detailID = "opaque"
	v.deviceState("local").ConnectionEpoch = "reconnected"
	next := v.workspaceToolsFor(v.workspaceEditor, "custom")
	if next == u || next.directory != "/owning/notes" || next.query != "docs" || next.detailID != "opaque" {
		t.Fatal("reconnect lost reading scope", next)
	}
}

func TestWorkspaceToolsOfflineNeverClaimsLiveConnection(t *testing.T) {
	v, ui := workspaceToolFixture(t)
	welcomeJob(t, v.Controller, "runtime.tools.inspect")
	u := v.workspaceToolsFor(v.workspaceEditor, "custom")
	u.session = "existing"
	u.phase = toolServiceLoaded
	u.catalog = workspaceToolCatalog()
	u.catalog.RuntimeKnown = true
	for i := range u.catalog.Entries {
		u.catalog.Entries[i].State = domain.ToolServiceReady
	}
	v.deviceState("local").Connected = false
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("已连接") {
		t.Fatal("offline view claims live observation", ui.Texts())
	}
}

func TestWorkspaceToolsHistoryContextChangeRejectsOldObservation(t *testing.T) {
	v, ui := workspaceToolFixture(t)
	welcomeJob(t, v.Controller, "runtime.tools.inspect")
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: "inspected", WorkspaceID: "w", HarnessID: "custom", ThreadID: "old-source", Context: domain.LaunchContext{CWD: "/owning/project"}})
	u := v.workspaceToolsFor(v.workspaceEditor, "custom")
	v.selectWorkspaceToolScope(u, "inspected", "")
	ui.Frame()
	old := welcomeJob(t, v.Controller, "runtime.tools.inspect")
	for i := range v.Model.Snapshot.Sessions {
		if v.Model.Snapshot.Sessions[i].ID == "inspected" {
			v.Model.Snapshot.Sessions[i].ThreadID = "new-source"
		}
	}
	ui.Frame()
	body, _ := json.Marshal(workspaceToolCatalog())
	old.Apply(v.Model, body, nil)
	if len(u.catalog.Entries) != 0 {
		t.Fatal("replaced history received a stale MCP observation")
	}
	select {
	case next := <-v.Controller.Jobs:
		if next.Method != "runtime.tools.inspect" {
			t.Fatal(next.Method)
		}
	default:
		t.Fatal("new source was not inspected")
	}
}
