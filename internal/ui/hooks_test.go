package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
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

func TestHooksNativeReviewTrustsOnlySelectedDefinitionsWithoutReload(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		dark          bool
	}{{1240, 820, false}, {1240, 820, true}, {640, 760, true}} {
		name := fmt.Sprintf("%d-dark-%v", tc.width, tc.dark)
		t.Run(name, func(t *testing.T) {
			v, ui := hookUIFixture(t, true)
			v.Model.DeviceStates["local"].WorkerMinor = 49
			ui.SetSize(tc.width, tc.height)
			ui.SetDark(tc.dark)
			catalog := hookUICatalog(true)
			catalog.Entries[0].Trust = domain.HookUntrusted
			catalog.Entries[0].CanTrust = true
			catalog.Entries = append(catalog.Entries,
				domain.HookEntry{ID: "session-start", DefinitionRevision: "start-1", Event: "SessionStart", Scope: domain.HookProject, Kind: "command", Command: "printf '%s\\n' 'session started'", Source: "项目", SourcePath: "/device/project/.codex/hooks.json", Matcher: "startup", Enabled: true, Trust: domain.HookModified, CanTrust: true},
				domain.HookEntry{ID: "post-tool", DefinitionRevision: "post-1", Event: "PostToolUse", Scope: domain.HookProject, Kind: "command", Command: "true # post-tool", Source: "项目", SourcePath: "/device/project/.codex/hooks.json", Enabled: true, Trust: domain.HookUntrusted, CanTrust: true},
			)
			body, _ := json.Marshal(catalog)
			welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, body, nil)
			ui.Frame()
			ui.Advance(time.Second)
			if err := ui.Click("审核待审批 Hooks"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !ui.HasText("go test ./...") || !ui.HasText("printf '%s\\n' 'session started'") || !ui.HasText("startup") || ui.HasText("echo finished") {
				t.Fatal("review must show full owned definitions", ui.Texts())
			}
			saveNativeSkillImage(t, ui, "hooks-review-"+name+".png")
			trustAllBefore, _ := ui.Find("信任全部（3）")
			if err := ui.Click("选择 Hook：PreToolUse"); err != nil {
				t.Fatal(err)
			}
			if err := ui.Click("选择 Hook：SessionStart"); err != nil {
				t.Fatal(err)
			}
			trustAllAfter, found := ui.Find("信任全部（3）")
			if !found || math.Abs(float64(trustAllBefore.X-trustAllAfter.X)) > 1 {
				t.Fatal("changing the selection moved the neighboring trust action", trustAllBefore, trustAllAfter)
			}
			saveNativeSkillImage(t, ui, "hooks-review-selected-"+name+".png")
			if err := ui.Click("信任所选（2）"); err != nil {
				t.Fatal(err)
			}
			job := welcomeJob(t, v.Controller, "runtime.hooks.change")
			request, _ := json.Marshal(job.Input)
			var sent struct {
				Action, Revision string
				Enabled          bool
				Definitions      []struct{ ID, DefinitionRevision string }
			}
			if err := json.Unmarshal(request, &sent); err != nil {
				t.Fatal(err)
			}
			if sent.Action != "trustDefinitions" || sent.Revision != "catalog-1" || sent.Enabled || len(sent.Definitions) != 2 ||
				sent.Definitions[0].ID != "project" || sent.Definitions[0].DefinitionRevision != "def-project" ||
				sent.Definitions[1].ID != "session-start" || sent.Definitions[1].DefinitionRevision != "start-1" {
				t.Fatal("approval did not preserve the selected versions", string(request))
			}
			catalog.Revision = "catalog-2"
			for _, i := range []int{1, 2} {
				catalog.Entries[i].Trust = domain.HookTrusted
				catalog.Entries[i].CanTrust = false
			}
			body, _ = json.Marshal(map[string]any{"input": job.Input, "state": domain.OperationConfirmed, "saved": true, "catalog": catalog})
			job.Apply(v.Model, body, nil)
			ui.Frame()
			ui.Advance(time.Second)
			if ui.HasText("go test ./...") || !ui.HasText("true # post-tool") {
				t.Fatal("review did not apply the receipt in place", ui.Texts())
			}
			saveNativeSkillImage(t, ui, "hooks-review-saved-"+name+".png")
			select {
			case next := <-v.Controller.Jobs:
				t.Fatalf("unexpected follow-up job after confirmed save: %s", next.Method)
			default:
			}
		})
	}
}

func TestHooksNativeTrustAllSubmitsEveryReviewableVersion(t *testing.T) {
	v, ui := hookUIFixture(t, true)
	v.Model.DeviceStates["local"].WorkerMinor = protocol.HookBatchMinor
	ui.SetSize(640, 760)
	catalog := hookUICatalog(true)
	catalog.Entries[0].Trust = domain.HookUntrusted
	catalog.Entries[0].CanTrust = true
	catalog.Entries = append(catalog.Entries,
		domain.HookEntry{ID: "post-tool", DefinitionRevision: "post-1", Event: "PostToolUse", Scope: domain.HookProject, Kind: "command", Command: "true # post-tool", SourcePath: "/device/project/.codex/hooks.json", Enabled: true, Trust: domain.HookModified, CanTrust: true},
		domain.HookEntry{ID: "read-only", DefinitionRevision: "policy-1", Event: "SessionStart", Scope: domain.HookProject, Kind: "command", Command: "true # read-only", Trust: domain.HookPolicy},
	)
	body, _ := json.Marshal(catalog)
	welcomeJob(t, v.Controller, "runtime.hooks.list").Apply(v.Model, body, nil)
	ui.Frame()
	if err := ui.Click("审核待审批 Hooks"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	saveNativeSkillImage(t, ui, "hooks-review-trust-all.png")
	if err := ui.Click("信任全部（2）"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "runtime.hooks.change")
	in := job.Input.(domain.HookRequest)
	want := []domain.HookDefinitionRef{{ID: "post-tool", DefinitionRevision: "post-1"}, {ID: "project", DefinitionRevision: "def-project"}}
	if in.Action != domain.HookTrustDefinitions || in.Revision != "catalog-1" || in.WorkspaceID != "w" ||
		in.Enabled || len(in.Definitions) != 2 || in.Definitions[0] != want[0] || in.Definitions[1] != want[1] {
		t.Fatal("trust all did not submit exactly the reviewable versions", in)
	}
	catalog.Revision = "catalog-2"
	for _, i := range []int{1, 2} {
		catalog.Entries[i].Trust = domain.HookTrusted
		catalog.Entries[i].CanTrust = false
	}
	body, _ = json.Marshal(domain.HookOperation{Input: in, State: domain.OperationConfirmed, Saved: true, Catalog: &catalog})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	if !ui.HasText("没有待审核的 Hooks") || ui.HasText("信任全部（2）") {
		t.Fatal("review did not clear the approved items in place", ui.Texts())
	}
	select {
	case next := <-v.Controller.Jobs:
		t.Fatalf("unexpected follow-up after trusting all: %s", next.Method)
	default:
	}
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
	if !q.Input.(domain.HookRequest).Equal(in) {
		t.Fatal("recovery changed approval identity")
	}
}

func TestHooksStayOutOfRunMenuAndUseSelectedSession(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1240, 850, 640} {
			t.Run(fmt.Sprintf("%d-dark-%v", width, dark), func(t *testing.T) {
				m := deviceUIModel()
				info := setupCatalog()[0]
				info.Capabilities.HookManagement = true
				info.Capabilities.HookInspection = true
				for _, state := range m.DeviceStates {
					state.WorkerMinor = 46
					state.Readiness = domain.Readiness{State: domain.RuntimeReady, HarnessID: info.ID, Capabilities: info.Capabilities, Harnesses: []domain.HarnessInfo{info}}
				}
				active := domain.Session{ID: "s", WorkspaceID: "w", HarnessID: info.ID, Title: "当前对话", Status: "completed"}
				target := domain.Session{ID: "target", WorkspaceID: "wb", HarnessID: info.ID, Title: "检查 Git worktree", ThreadID: "opaque-thread", Context: domain.LaunchContext{CWD: "/accepted/project"}, Status: "completed"}
				m.DeviceStates["local"].Snapshot.Sessions = []domain.Session{active}
				m.DeviceStates["air"].Snapshot.Sessions = []domain.Session{target}
				m.RefreshDevices()
				m.Details["s"] = domain.SessionDetail{Session: active, HistoryLoaded: true}
				m.Drafts["session:s"] = "keep my draft"
				v := New(m, app.NewController(""), "")
				ui := n.NewTester(v.View, width, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if err := ui.Click("运行配置"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				if !ui.HasText("模型") || ui.HasText("Hooks") {
					t.Fatal("run menu should show model choices without Hooks", ui.Texts())
				}
				for len(v.Controller.Jobs) > 0 {
					if j := <-v.Controller.Jobs; j.Method == "runtime.hooks.list" {
						t.Fatal("opening run configuration read Hooks", j.Input)
					}
				}
				name := fmt.Sprintf("%d-dark-%v.png", width, dark)
				saveNativeSkillImage(t, ui, "run-menu-"+name)
				ui.Key(0, n.KeyEscape)
				row, ok := ui.Find(target.Title)
				if !ok {
					t.Fatal("target session is absent from sidebar", ui.Texts())
				}
				ui.RightClickAt(row.X+8, row.Y+row.H/2)
				if err := ui.Click("会话 Hooks"); err != nil {
					t.Fatal(err, ui.Texts())
				}
				ui.Advance(time.Second)
				job := welcomeJob(t, v.Controller, "runtime.hooks.list")
				in := job.Input.(domain.HookRequest)
				if job.ConnectionID != "air" || in.SessionID != "target" || in.WorkspaceID != "wb" || in.HarnessID != "custom" {
					t.Fatal("session hook inspection lost owning device or accepted context", job.ConnectionID, in)
				}
				c := hookUICatalog(true)
				body, _ := json.Marshal(c)
				job.Apply(m, body, nil)
				ui.Frame()
				ui.Advance(time.Second)
				panel, found := ui.Find("Hooks 列表")
				if !found || panel.X < row.X+row.W || panel.X+panel.W > float32(width) || panel.Y < 0 || panel.Y+panel.H > 820 {
					t.Fatal("Hooks list escapes the content pane", row, panel)
				}
				saveNativeSkillImage(t, ui, "session-hooks-"+name)
				if err := ui.Click("在设备设置中管理 Hooks"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				global := welcomeJob(t, v.Controller, "runtime.hooks.list")
				if scope := global.Input.(domain.HookRequest); global.ConnectionID != "air" || scope.WorkspaceID != "" || scope.SessionID != "" {
					t.Fatal("global Hooks left the selected device or retained the session scope", global.ConnectionID, scope)
				}
				if err := ui.Click("返回会话 Hooks"); err != nil {
					t.Fatal(err, ui.Texts())
				}
				ui.Advance(time.Second)
				returned := welcomeJob(t, v.Controller, "runtime.hooks.list")
				if returned.ConnectionID != "air" || !returned.Input.(domain.HookRequest).Equal(in) {
					t.Fatal("device management did not return to the original session scope", returned.ConnectionID, returned.Input)
				}
				if err := ui.Click("返回"); err != nil {
					t.Fatal(err, ui.Texts())
				}
				ui.Advance(time.Second)
				if _, visible := ui.Find("消息输入"); !visible || m.SessionID != "s" || m.Drafts["session:s"] != "keep my draft" {
					t.Fatal("return replaced the session/draft")
				}
				ui.Key(n.Cmd, n.KeyN)
				if err := ui.Click("运行配置"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				if !ui.HasText("模型") || ui.HasText("Hooks") {
					t.Fatal("home run menu should show model choices without Hooks", ui.Texts())
				}
			})
		}
	}
}

func TestArchivedConversationCanStillInspectItsHooks(t *testing.T) {
	m := deviceUIModel()
	info := setupCatalog()[0]
	info.Capabilities.HookManagement = true
	state := m.DeviceStates["local"]
	state.Readiness.Harnesses = []domain.HarnessInfo{info}
	state.WorkerMinor = protocol.HookBatchMinor
	session := domain.Session{ID: "s", WorkspaceID: "w", Title: "归档任务", HarnessID: info.ID, Archived: true, Context: domain.LaunchContext{CWD: "/accepted/project"}}
	state.Snapshot.Sessions = []domain.Session{session}
	m.RefreshDevices()
	m.Details["s"] = domain.SessionDetail{Session: session, HistoryLoaded: true}
	v := New(m, app.NewController(""), "")
	v.openArchive()
	ui := n.NewTester(v.View, 1240, 820)
	if err := ui.Click("查看归档会话：归档任务"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("工作区目录"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("会话 Hooks"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job := welcomeJob(t, v.Controller, "runtime.hooks.list")
	if in := job.Input.(domain.HookRequest); in.SessionID != "s" || in.WorkspaceID != "w" {
		t.Fatal("archive inspection lost the accepted session", in)
	}
	catalog := hookUICatalog(true)
	body, _ := json.Marshal(catalog)
	job.Apply(m, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("审核待审批 Hooks"); ok {
		t.Fatal("archive exposes batch approval")
	}
	if err := ui.Click("返回"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if err := ui.Click("返回已归档"); err != nil {
		t.Fatal("Hooks displaced archive navigation", err, ui.Texts())
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
	if len(u.unconfirmed) != 1 || !u.unconfirmed[0].Equal(in) {
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
