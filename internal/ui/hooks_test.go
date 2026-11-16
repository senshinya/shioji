package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func hookUIFixture(t *testing.T, project bool) (*View, *n.Tester) {
	t.Helper()
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerMinor = 46
	info := setupCatalog()[0]
	info.Capabilities.HookManagement = true
	info.Capabilities.HookInspection = true
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	v := New(m, app.NewController(""), "")
	var ui *n.Tester
	if project {
		for _, w := range m.Snapshot.Workspaces {
			if w.ID == "w" {
				v.editWorkspace(w)
			}
		}
		v.workspaceEditor.Tab = "Hooks"
		v.workspaceEditor.Harness = info.ID
		ui = n.NewTester(v.View, 1240, 820)
	} else {
		e := agentEditor([]domain.HarnessInfo{info})
		e.edit(info)
		e.tab = "Hooks"
		ui = n.NewTester(func(c *n.Context) {
			n.Column(c).Width(700).Height(650).Children(func() { v.agentConfiguration(c, c.Theme(), "local", true, e, info, false, func() {}) })
		}, 850, 700)
	}
	ui.Advance(time.Second)
	return v, ui
}
func hookUICatalog(project bool) domain.HookCatalog {
	c := domain.HookCatalog{Revision: "catalog-1", Entries: []domain.HookEntry{{ID: "global", DefinitionRevision: "def-global", Event: "Stop", Kind: "command", Command: "echo finished", Source: "用户", SourcePath: "/device/.codex/hooks.json", Scope: domain.HookUser, Enabled: true, Trust: domain.HookTrusted, CanEnable: !project}}}
	if project {
		c.Entries = append(c.Entries, domain.HookEntry{ID: "project", DefinitionRevision: "def-project", Event: "PreToolUse", Kind: "command", Command: "go test ./...", Source: "项目", SourcePath: "/device/project/.codex/hooks.json", Scope: domain.HookProject, Enabled: false, Trust: domain.HookUntrusted, CanTrust: true, CanEnable: true})
	} else {
		c.Entries[0].Trust = domain.HookUntrusted
		c.Entries[0].Enabled = false
		c.Entries[0].CanTrust = true
	}
	return c
}
func applyHookCatalog(t *testing.T, v *View, ui *n.Tester, project bool) {
	t.Helper()
	b, _ := json.Marshal(hookUICatalog(project))
	welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
}
func TestHooksNativeApproveVersionDoesNotEnable(t *testing.T) {
	v, ui := hookUIFixture(t, false)
	if _, ok := ui.Find("Hooks 列表"); !ok {
		t.Fatal("native Hooks tab absent", ui.Texts())
	}
	applyHookCatalog(t, v, ui, false)
	if err := ui.Click("查看 Hook：Stop"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("echo finished") {
		t.Fatal("review definition absent", ui.Texts())
	}
	if err := ui.Click("信任此版本"); err != nil {
		t.Fatal(err)
	}
	j := welcomeJob(t, v.Controller, "runtime.hooks.change")
	in := j.Input.(domain.HookRequest)
	if in.Enabled || in.Action != domain.HookTrustDefinition || in.DefinitionRevision != "def-global" || in.Revision != "catalog-1" || in.OperationID == "" {
		t.Fatal("approval changed identity or enabled state", in)
	}
	receipt, _ := json.Marshal(domain.HookOperation{Input: in, State: domain.OperationConfirmed, Saved: true})
	j.Apply(v.Model, receipt, nil)
	ui.Frame()
	c := hookUICatalog(false)
	c.Entries[0].Trust = domain.HookTrusted
	c.Entries[0].CanTrust = false
	body, _ := json.Marshal(c)
	welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("信任此版本"); ok {
		t.Fatal("already trusted version can be trusted again")
	}
}
func TestHooksWorkspaceInheritanceAndReturnPreserveDraft(t *testing.T) {
	v, ui := hookUIFixture(t, true)
	applyHookCatalog(t, v, ui, true)
	if _, ok := ui.Find("启停 Hook：Stop"); ok {
		t.Fatal("global switch visible in workspace")
	}
	if _, ok := ui.Find("启停 Hook：PreToolUse"); !ok {
		t.Fatal("project switch absent", ui.Texts())
	}
	original := v.workspaceEditor
	original.Workspace.Name = "unsaved workspace"
	if err := ui.Click("在设备设置中管理 Hooks"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("返回工作区 Hooks"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	if v.devices || v.workspaceEditor != original || original.Workspace.Name != "unsaved workspace" || original.Tab != "Hooks" {
		t.Fatal("return discarded draft/context")
	}
}
func TestHooksUnknownSaveQueriesReceiptWithoutReplayingApproval(t *testing.T) {
	v, ui := hookUIFixture(t, false)
	applyHookCatalog(t, v, ui, false)
	ui.Click("查看 Hook：Stop")
	ui.Advance(time.Second)
	ui.Click("信任此版本")
	j := welcomeJob(t, v.Controller, "runtime.hooks.change")
	in := j.Input.(domain.HookRequest)
	j.Apply(v.Model, nil, json.Unmarshal([]byte("bad"), new(any)))
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("刷新 Hooks"); err != nil {
		t.Fatal(err)
	}
	q := welcomeJob(t, v.Controller, "runtime.hooks.operation")
	if q.Input.(domain.HookRequest) != in {
		t.Fatal("recovery changed approval identity")
	}
}

func TestHooksSessionMenuUsesAcceptedSessionAndReturnsToComposer(t *testing.T) {
	for _, width := range []int{1240, 850, 640} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := deviceUIModel()
			m.WorkerMinor = 46
			m.Readiness.Capabilities.HookManagement = true
			m.Readiness.Capabilities.HookInspection = true
			info := setupCatalog()[0]
			info.Capabilities.HookManagement = true
			info.Capabilities.HookInspection = true
			m.Readiness.HarnessID = info.ID
			m.Readiness.Harnesses = []domain.HarnessInfo{info}
			m.DeviceStates["local"].Readiness = m.Readiness
			m.DeviceStates["local"].WorkerMinor = 46
			s := domain.Session{ID: "s", WorkspaceID: "w", HarnessID: info.ID, ThreadID: "opaque-thread", Context: domain.LaunchContext{CWD: "/accepted/project"}, Status: "completed"}
			m.Snapshot.Sessions = []domain.Session{s}
			m.SessionID = "s"
			m.WorkspaceID = "w"
			m.Details["s"] = domain.SessionDetail{Session: s}
			m.Drafts["session:s"] = "keep my draft"
			v := New(m, app.NewController(""), "")
			ui := n.NewTester(v.View, width, 820)
			ui.Advance(time.Second)
			ui.Click("运行配置")
			ui.Advance(time.Second)
			if err := ui.Click("Hooks"); err != nil {
				t.Fatal("Hooks entry absent", err, ui.Texts())
			}
			ui.Advance(time.Second)
			job := welcomeJob(t, v.Controller, "runtime.hooks.list")
			in := job.Input.(domain.HookRequest)
			if in.SessionID != "s" || in.WorkspaceID != "w" {
				t.Fatal("session hook inspection lost accepted context", in)
			}
			c := hookUICatalog(true)
			body, _ := json.Marshal(c)
			job.Apply(m, body, nil)
			ui.Frame()
			ui.Advance(time.Second)
			panel, _ := ui.Find("运行配置菜单")
			if panel.X < 0 || panel.X+panel.W > float32(width) || panel.Y < 0 || panel.Y+panel.H > 820 {
				t.Fatal("Hooks popover overflows window", panel)
			}
			saveNativeSkillImage(t, ui, fmt.Sprintf("hooks-session-%d.png", width))
			if err := ui.Click("在设备设置中管理 Hooks"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			// Actual catalog must offer this harness in order to enter global management.
			if !v.devices {
				t.Fatal("session cannot enter global management")
			}
			v.returnSkillWorkspace()
			ui.Frame()
			ui.Advance(time.Second)
			if v.workspaceEditor != nil || v.skillReturn != nil || m.SessionID != "s" || m.Drafts["session:s"] != "keep my draft" {
				t.Fatal("return replaced the session/draft")
			}
		})
	}
}
func TestHooksMissingReceiptAllowsFreshReviewWithoutResending(t *testing.T) {
	v, ui := hookUIFixture(t, false)
	applyHookCatalog(t, v, ui, false)
	ui.Click("查看 Hook：Stop")
	ui.Advance(time.Second)
	ui.Click("信任此版本")
	change := welcomeJob(t, v.Controller, "runtime.hooks.change")
	change.Apply(v.Model, nil, fmt.Errorf("connection lost"))
	ui.Frame()
	ui.Click("刷新 Hooks")
	query := welcomeJob(t, v.Controller, "runtime.hooks.operation")
	query.Apply(v.Model, nil, &protocol.RemoteError{Code: "notFound", Message: "not persisted"})
	ui.Frame()
	read := welcomeJob(t, v.Controller, "runtime.hooks.list")
	body, _ := json.Marshal(hookUICatalog(false))
	read.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("信任此版本"); err != nil {
		t.Fatal(err)
	}
	next := welcomeJob(t, v.Controller, "runtime.hooks.change")
	if next.Input.(domain.HookRequest).OperationID == change.Input.(domain.HookRequest).OperationID {
		t.Fatal("fresh review replayed unaccepted approval")
	}
}

func TestHooksLateResultsAndOfflineCatalogCannotAuthorize(t *testing.T) {
	v, ui := hookUIFixture(t, false)
	old := welcomeJob(t, v.Controller, "runtime.hooks.list")
	v.Model.DeviceStates["local"].ConnectionEpoch = "replacement"
	ui.Frame()
	next := welcomeJob(t, v.Controller, "runtime.hooks.list")
	body, _ := json.Marshal(hookUICatalog(false))
	old.Apply(v.Model, body, nil)
	ui.Frame()
	if _, ok := ui.Find("查看 Hook：Stop"); ok {
		t.Fatal("old connection populated new catalog")
	}
	next.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	v.Model.DeviceStates["local"].Connected = false
	ui.Frame()
	ui.Advance(time.Second)
	ui.Click("启停 Hook：Stop")
	select {
	case j := <-v.Controller.Jobs:
		if j.Method == "runtime.hooks.change" {
			t.Fatal("offline catalog authorized a change")
		}
	default:
	}
	if !ui.HasText("设备未连接，显示上次结果") {
		t.Fatal(ui.Texts())
	}
}
func TestHooksLargeCatalogScrollAndSearchKeepFixedToolbar(t *testing.T) {
	v, ui := hookUIFixture(t, false)
	catalog := hookUICatalog(false)
	for i := 0; i < 60; i++ {
		catalog.Entries = append(catalog.Entries, domain.HookEntry{ID: fmt.Sprint(i), Event: fmt.Sprintf("Tool%02d", i), Scope: domain.HookUser, Source: "用户", Kind: "command", Command: "true", Trust: domain.HookPolicy})
	}
	b, _ := json.Marshal(catalog)
	welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	search, _ := ui.Find("搜索 Hooks")
	list, _ := ui.Find("Hooks 列表")
	ui.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 5000)
	ui.Advance(time.Second)
	after, _ := ui.Find("搜索 Hooks")
	if search.Y != after.Y {
		t.Fatal("toolbar scrolls with catalog", search, after)
	}
	if _, ok := ui.Find("查看 Hook：Tool59"); !ok {
		t.Fatal("last hook is not reachable")
	}
	u := v.hookManagementFor("local", "custom", "", "")
	u.query = "no such hook"
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("没有匹配的 Hook") {
		t.Fatal("empty search feedback absent", ui.Texts())
	}
}

func TestHooksUnknownOwnershipDoesNotClaimGlobalInheritance(t *testing.T) {
	v, ui := hookUIFixture(t, true)
	catalog := domain.HookCatalog{Revision: "one", Entries: []domain.HookEntry{{ID: "ambiguous", DefinitionRevision: "d", Event: "PreToolUse", Scope: domain.HookUnknown, Source: "其他来源", Kind: "command", Command: "inspect only", Trust: domain.HookUntrusted, ReadOnlyReason: "当前来源只读"}}}
	body, _ := json.Marshal(catalog)
	welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("全局 · 继承") {
		t.Fatal("unproven ownership labelled global")
	}
	ui.Click("查看 Hook：PreToolUse")
	ui.Advance(time.Second)
	if _, ok := ui.Find("在设备设置中管理"); ok {
		t.Fatal("ambiguous source routed to global management")
	}
}

func TestHooksIndeterminateReceiptAllowsReviewOfChangedDefinition(t *testing.T) {
	v, ui := hookUIFixture(t, false)
	applyHookCatalog(t, v, ui, false)
	ui.Click("查看 Hook：Stop")
	ui.Advance(time.Second)
	ui.Click("信任此版本")
	change := welcomeJob(t, v.Controller, "runtime.hooks.change")
	in := change.Input.(domain.HookRequest)
	unknown, _ := json.Marshal(domain.HookOperation{Input: in, State: domain.OperationIndeterminate})
	change.Apply(v.Model, unknown, nil)
	ui.Frame()
	ui.Click("刷新 Hooks")
	query := welcomeJob(t, v.Controller, "runtime.hooks.operation")
	query.Apply(v.Model, unknown, nil)
	ui.Frame()
	read := welcomeJob(t, v.Controller, "runtime.hooks.list")
	c := hookUICatalog(false)
	c.Revision = "catalog-2"
	c.Entries[0].DefinitionRevision = "changed-definition"
	c.Entries[0].Command = "echo revised"
	body, _ := json.Marshal(c)
	read.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	u := v.hookManagementFor("local", "custom", "", "")
	if len(u.unconfirmed) != 1 || u.unconfirmed[0] != in {
		t.Fatal("original uncertain receipt identity lost")
	}
	if !ui.HasText("echo revised") {
		t.Fatal("replacement definition cannot be inspected", ui.Texts())
	}
	ui.Click("信任此版本")
	next := welcomeJob(t, v.Controller, "runtime.hooks.change")
	fresh := next.Input.(domain.HookRequest)
	if fresh.OperationID == in.OperationID || fresh.DefinitionRevision != "changed-definition" {
		t.Fatal("fresh review replayed old grant", fresh)
	}
}
func TestHooksOwnershipGroupsHaveUniqueStableHeaders(t *testing.T) {
	v, ui := hookUIFixture(t, true)
	catalog := domain.HookCatalog{Revision: "r"}
	for i, scope := range []domain.HookScope{domain.HookUnknown, domain.HookUser, domain.HookUnknown, domain.HookUser} {
		catalog.Entries = append(catalog.Entries, domain.HookEntry{ID: fmt.Sprint(i), Event: fmt.Sprint(i), Scope: scope, Source: "fixture", Kind: "command", Command: "true", Trust: domain.HookUntrusted})
	}
	body, _ := json.Marshal(catalog)
	welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	counts := map[string]int{}
	for _, text := range ui.Texts() {
		counts[text]++
	}
	if counts["其他来源"] != 1 || counts["全局 · 继承"] != 1 {
		t.Fatal("ownership groups repeat", counts)
	}
}
