package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func skillDeviceUI(t *testing.T) (*View, *deviceAgentEditor, *n.Tester) {
	t.Helper()
	m := deviceUIModel()
	m.DeviceStates["local"].WorkerMinor = 25
	v := New(m, app.NewController(""), "")
	info := setupCatalog()[0]
	info.Capabilities.SkillManagement = true
	info.Capabilities.ToolServices = true
	e := agentEditor([]domain.HarnessInfo{info})
	e.edit(info)
	e.tab = "Skills"
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(700).Height(650).Children(func() { v.agentConfiguration(c, c.Theme(), "local", true, e, info, false, func() {}) })
	}, 850, 700)
	ui.Advance(time.Second)
	return v, e, ui
}
func TestSkillManagementDeviceTabQueriesDeviceAndRetainsRowsOnRefreshFailure(t *testing.T) {
	v, _, ui := skillDeviceUI(t)
	if _, ok := ui.Find("Skills 列表"); !ok {
		t.Fatal("device Skills page missing", ui.Texts())
	}
	job := welcomeJob(t, v.Controller, "runtime.skills.list")
	in := job.Input.(protocol.SkillManagementInput)
	if in.WorkspaceID != "" || in.HarnessID != "custom" || job.ConnectionID != "local" {
		t.Fatal("device scope changed", in)
	}
	body, _ := json.Marshal(domain.SkillInventory{Revision: "one", Entries: []domain.ManagedSkill{{SkillReference: domain.SkillReference{ID: "opaque", Name: "check"}, Description: "Check real edits", Scope: domain.SkillUser, Directory: "/device/skills", Path: "/device/skills/check/SKILL.md", Enabled: true, CanEnable: true}}})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("查看 Skill：check"); err != nil {
		t.Fatal(err)
	}
	read := welcomeJob(t, v.Controller, "runtime.skills.read")
	doc, _ := json.Marshal(domain.SkillDocument{ID: "opaque", Text: "# Instructions\n\nActual body."})
	read.Apply(v.Model, doc, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("Actual body.") {
		t.Fatal("native Skill content absent", ui.Texts())
	}
	if err := ui.Click("返回 Skills 列表"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("刷新 Skills"); err != nil {
		t.Fatal(err)
	}
	welcomeJob(t, v.Controller, "runtime.skills.list").Apply(v.Model, nil, errors.New("offline"))
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("check") {
		t.Fatal("refresh failure erased existing rows")
	}
	if err := ui.Click("启停 Skill：check"); err == nil {
		select {
		case j := <-v.Controller.Jobs:
			if j.Method == "runtime.skills.change" {
				t.Fatal("unverified catalog remained writable")
			}
		default:
		}
	}
}
func TestSkillManagementWorkspaceUsesAcceptedContextAndInheritedEntriesAreReadOnly(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 25
	info := setupCatalog()[0]
	info.Capabilities.SkillManagement = true
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	v := New(m, app.NewController(""), "")
	var workspace domain.Workspace
	for _, w := range m.Snapshot.Workspaces {
		if w.ID == "w" {
			workspace = w
			break
		}
	}
	workspace.Name = "红黑树"
	workspace.Folders = []string{"/owning/project"}
	v.editWorkspace(workspace)
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(800).Height(650).Children(func() { v.workspaceSettingsView(c, c.Theme()) })
	}, 850, 700)
	ui.Advance(time.Second)
	v.workspaceEditor.Workspace.Name = "unsaved name"
	if err := ui.Click("Skills"); err != nil {
		t.Fatal("workspace Skills tab missing", err)
	}
	ui.Advance(time.Second)
	job := welcomeJob(t, v.Controller, "runtime.skills.list")
	in := job.Input.(protocol.SkillManagementInput)
	if in.WorkspaceID != "w" || job.ConnectionID != "local" {
		t.Fatal("workspace query lost ownership", in)
	}
	body, _ := json.Marshal(domain.SkillInventory{Revision: "one", Entries: []domain.ManagedSkill{{SkillReference: domain.SkillReference{ID: "project", Name: "tree-check"}, Scope: domain.SkillProject, Directory: "/owning/project/.agents/skills", Enabled: true, CanEnable: true}, {SkillReference: domain.SkillReference{ID: "global", Name: "design"}, Scope: domain.SkillUser, Directory: "/owning/user/skills", Enabled: true, CanEnable: true}}})
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("启停 Skill：design"); ok {
		t.Fatal("inherited item exposes a global switch in a project page")
	}
	if _, ok := ui.Find("启停 Skill：tree-check"); !ok {
		t.Fatal("actual supported project switch missing")
	}
	if err := ui.Click("在设备设置中管理 Skills"); err != nil {
		t.Fatal(err)
	}
	if !v.devices {
		t.Fatal("inherited management did not open device settings")
	}
	if err := ui.Click("常规"); err == nil {
		ui.Advance(time.Second)
	}
	// Workspace editor is retained independently while its global management page is open.
}

func TestSkillManagementDeviceReturnPreservesWorkspaceDraftAndReadingState(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 25
	info := setupCatalog()[0]
	info.Capabilities.SkillManagement = true
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	v := New(m, app.NewController(""), "")
	var w domain.Workspace
	for _, candidate := range m.Snapshot.Workspaces {
		if candidate.ID == "w" {
			w = candidate
		}
	}
	v.editWorkspace(w)
	original := v.workspaceEditor
	original.Workspace.Name = "unsaved name"
	original.Tab = "Skills"
	original.Harness = info.ID
	u := v.skillManagementFor("local", info.ID, w.ID)
	u.query = "check"
	u.listScroll.Y = 88
	v.openSkillDeviceManagement(u, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("返回工作区 Skills"); err != nil {
		t.Fatal("no direct return to owning workspace", err, ui.Texts())
	}
	ui.Advance(time.Second)
	if v.devices || v.workspaceEditor != original || v.workspaceEditor.Workspace.Name != "unsaved name" || u.query != "check" {
		t.Fatal("management return lost the original workspace draft/search")
	}
}

func TestSkillManagementLateInventoryCannotReplaceReconnectedCatalog(t *testing.T) {
	v, _, ui := skillDeviceUI(t)
	old := welcomeJob(t, v.Controller, "runtime.skills.list")
	v.Model.DeviceStates["local"].ConnectionEpoch = "new"
	ui.Frame()
	next := welcomeJob(t, v.Controller, "runtime.skills.list")
	late, _ := json.Marshal(domain.SkillInventory{Entries: []domain.ManagedSkill{{SkillReference: domain.SkillReference{ID: "old", Name: "STALE_CATALOG"}}}})
	old.Apply(v.Model, late, nil)
	ui.Frame()
	if ui.HasText("STALE_CATALOG") {
		t.Fatal("old connection catalog replaced new state")
	}
	current, _ := json.Marshal(domain.SkillInventory{Entries: []domain.ManagedSkill{{SkillReference: domain.SkillReference{ID: "new", Name: "CURRENT_CATALOG"}}}})
	next.Apply(v.Model, current, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("CURRENT_CATALOG") {
		t.Fatal(ui.Texts())
	}
}

func TestSkillManagementEnableFailurePreservesConfirmedValueUntilRefresh(t *testing.T) {
	v, _, ui := skillDeviceUI(t)
	entry := domain.ManagedSkill{SkillReference: domain.SkillReference{ID: "opaque", Name: "check"}, Scope: domain.SkillUser, Directory: "/skills", Enabled: true, CanEnable: true}
	body, _ := json.Marshal(domain.SkillInventory{Revision: "revision", Entries: []domain.ManagedSkill{entry}})
	welcomeJob(t, v.Controller, "runtime.skills.list").Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("启停 Skill：check"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "runtime.skills.change")
	ui.Frame()
	if !ui.HasText("正在停用") {
		t.Fatal("the changed Skill row does not show progress", ui.Texts())
	}
	in := job.Input.(protocol.SkillManagementInput)
	if in.ID != "opaque" || in.Enabled || in.Revision != "revision" || in.WorkspaceID != "" {
		t.Fatal("toggle changed a different source", in)
	}
	job.Apply(v.Model, nil, errors.New("unknown save"))
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("保存未确认，原状态保留。请刷新核对") {
		t.Fatal(ui.Texts())
	}
	if err := ui.Click("刷新 Skills"); err != nil {
		t.Fatal(err)
	}
	entry.Enabled = false
	body, _ = json.Marshal(domain.SkillInventory{Revision: "new", Entries: []domain.ManagedSkill{entry}})
	welcomeJob(t, v.Controller, "runtime.skills.list").Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("保存未确认，原状态保留。请刷新核对") {
		t.Fatal("confirmed refresh left stale failure")
	}
}

func openManagedDetailFixture(t *testing.T) (*View, *n.Tester, []byte) {
	t.Helper()
	v, _, ui := skillDeviceUI(t)
	body, _ := json.Marshal(domain.SkillInventory{Revision: "one", Entries: []domain.ManagedSkill{{SkillReference: domain.SkillReference{ID: "same-path", Name: "check"}, Scope: domain.SkillUser, Directory: "/skills", Enabled: true, CanEnable: true}}})
	welcomeJob(t, v.Controller, "runtime.skills.list").Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("查看 Skill：check")
	ui.Advance(time.Second)
	doc, _ := json.Marshal(domain.SkillDocument{ID: "same-path", Text: "ORIGINAL_SKILL_BODY"})
	welcomeJob(t, v.Controller, "runtime.skills.read").Apply(v.Model, doc, nil)
	ui.Frame()
	ui.Advance(time.Second)
	return v, ui, body
}
func TestSkillManagementDetailShowsSaveRecoveryAndOfflineCache(t *testing.T) {
	v, ui, body := openManagedDetailFixture(t)
	ui.Click("启停 Skill：check")
	welcomeJob(t, v.Controller, "runtime.skills.change").Apply(v.Model, nil, errors.New("uncertain save"))
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("保存未确认，原状态保留。请刷新核对") {
		t.Fatal("detail save error is invisible", ui.Texts())
	}
	if err := ui.Click("刷新 Skills"); err != nil {
		t.Fatal("detail lacks refresh recovery", err)
	}
	welcomeJob(t, v.Controller, "runtime.skills.list").Apply(v.Model, body, nil)
	ui.Frame()
	doc, _ := json.Marshal(domain.SkillDocument{ID: "same-path", Text: "CONFIRMED_SKILL_BODY"})
	welcomeJob(t, v.Controller, "runtime.skills.read").Apply(v.Model, doc, nil)
	ui.Frame()
	ui.Advance(time.Second)
	v.Model.DeviceStates["local"].Connected = false
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("设备未连接，显示上次结果") || !ui.HasText("CONFIRMED_SKILL_BODY") {
		t.Fatal("cached detail is presented as live or lost", ui.Texts())
	}
}
func TestSkillManagementRefreshRevalidatesSamePathAndRemovesUnavailableBody(t *testing.T) {
	v, ui, body := openManagedDetailFixture(t)
	v.Model.DeviceStates["local"].ConnectionEpoch = "reconnected"
	ui.Frame()
	welcomeJob(t, v.Controller, "runtime.skills.list").Apply(v.Model, body, nil)
	ui.Frame()
	doc, _ := json.Marshal(domain.SkillDocument{ID: "same-path", Text: "UPDATED_SKILL_BODY"})
	welcomeJob(t, v.Controller, "runtime.skills.read").Apply(v.Model, doc, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("UPDATED_SKILL_BODY") || ui.HasText("ORIGINAL_SKILL_BODY") {
		t.Fatal("same-path content was not revalidated", ui.Texts())
	}
	ui.Click("刷新 Skills")
	empty, _ := json.Marshal(domain.SkillInventory{Revision: "two"})
	welcomeJob(t, v.Controller, "runtime.skills.list").Apply(v.Model, empty, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("UPDATED_SKILL_BODY") {
		t.Fatal("removed Skill still exposes old cached body")
	}
}

func TestSkillManagementDeviceEntrySynchronizesCurrentAgentCatalog(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 25
	info := setupCatalog()[0]
	info.Capabilities.SkillManagement = true
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	v := New(m, app.NewController(""), "")
	var w domain.Workspace
	for _, candidate := range m.Snapshot.Workspaces {
		if candidate.ID == "w" {
			w = candidate
		}
	}
	v.editWorkspace(w)
	v.workspaceEditor.Tab = "Skills"
	v.workspaceEditor.Harness = info.ID
	v.openSkillDeviceManagement(v.skillManagementFor("local", info.ID, w.ID), "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if err := ui.Click("模型"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	changed := info
	changed.Models = []domain.Model{{ID: "new-model", Name: "Model Two"}}
	state.Readiness.Harnesses = []domain.HarnessInfo{changed}
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("Model Two") || ui.HasText("Model One") {
		t.Fatal("scoped device entry retained stale model metadata", ui.Texts())
	}
	state.ConnectionEpoch = "reconnected"
	ui.Frame()
	welcomeJob(t, v.Controller, "runtime.catalog")
}
