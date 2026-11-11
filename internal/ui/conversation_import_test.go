package ui

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestConversationCheckboxAlignsToFirstTitleLine(t *testing.T) {
	_, _, ui := showImport(t)
	checkbox, ok := ui.Find("选择会话：旋转测试")
	if !ok {
		t.Fatal("missing checkbox")
	}
	title, ok := ui.Find("预览会话：旋转测试")
	if !ok {
		t.Fatal("missing title")
	}
	if math.Abs(float64(checkbox.Y+checkbox.H/2-(title.Y+10))) > 2 {
		t.Fatal("checkbox aligns with title and timestamp together", checkbox, title)
	}
	all, _ := ui.Find("选择当前显示")
	tab, _ := ui.Find("全部")
	if all.Y < tab.Y+tab.H+5 {
		t.Fatal("select-all touches tab divider", all, tab)
	}
}

func importModel() *app.Model {
	m := welcomeModel()
	m.FirstRun = false
	m.DeviceStates["local"].WorkerMinor = 32
	m.DeviceStates["local"].Readiness = domain.Readiness{State: "ready", HarnessID: "memory", Models: []domain.Model{{ID: "model", Default: true}}, Permissions: []string{"default"}, Harnesses: []domain.HarnessInfo{{ID: "memory", Name: "Memory Agent", State: "ready", Capabilities: domain.HarnessCapabilities{ConversationImport: true}}}}
	m.RefreshDevices()
	return m
}
func importCatalog() domain.ConversationCatalog {
	return domain.ConversationCatalog{Items: []domain.ConversationCandidate{{SourceRef: "one", HarnessID: "memory", Title: "旋转测试", Directory: "/Users/local/project"}, {SourceRef: "two", HarnessID: "memory", Title: "边界测试", Directory: "/Users/local/project"}, {SourceRef: "three", HarnessID: "memory", Title: "已有会话", Directory: "/Users/local/project", ImportedSessionID: "existing"}}}
}
func showImport(t *testing.T) (*View, *app.Controller, *n.Tester) {
	t.Helper()
	m := importModel()
	c := app.NewController("")
	v := New(m, c, "")
	w := m.DeviceStates["local"].Snapshot.Workspaces[0]
	w.Revision = 1
	w.Folders = []string{"/Users/local/project"}
	m.DeviceStates["local"].Snapshot.Workspaces[0] = w
	m.RefreshDevices()
	v.openConversationImport("local", w, false)
	ui := n.NewTester(v.View, 1120, 840)
	ui.Advance(time.Second)
	job := welcomeJob(t, c, "conversation.list")
	b, _ := json.Marshal(importCatalog())
	job.Apply(m, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	return v, c, ui
}
func TestConversationImportSelectionSurvivesFilterAndPreviewDoesNotImport(t *testing.T) {
	v, c, ui := showImport(t)
	if err := ui.Click("选择会话：旋转测试"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Click("已导入")
	ui.Advance(configurationTabDuration)
	if !v.conversationImport.selected["one"] {
		t.Fatal("filter cleared selection")
	}
	ui.Click("全部")
	ui.Advance(configurationTabDuration)
	if err := ui.Click("预览会话：边界测试"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job := welcomeJob(t, c, "conversation.preview")
	in := job.Input.(protocol.ConversationQuery)
	if in.SourceRef != "two" || in.Directory != "/Users/local/project" || job.ConnectionID != "local" {
		t.Fatal("preview escaped source", job)
	}
	ui.Click("返回列表")
	ui.Advance(configurationTabDuration)
	b, _ := json.Marshal(domain.ConversationPreview{Candidate: importCatalog().Items[1], Items: []domain.Item{{ID: "a", Kind: "assistant", Text: "迟到预览", Final: true}}})
	job.Apply(v.Model, b, nil)
	ui.Frame()
	if ui.HasText("迟到预览") || v.conversationImport.previewSource != "" {
		t.Fatal("late preview reopened closed detail")
	}
	for len(c.Jobs) > 0 {
		if j := <-c.Jobs; j.Method == "conversation.import" {
			t.Fatal("preview imported source")
		}
	}
}
func TestConversationImportRetryOnlySendsFailuresAndKeepsDraft(t *testing.T) {
	v, c, ui := showImport(t)
	draft := v.Model.Drafts["home"]
	ui.Click("选择会话：旋转测试")
	ui.Click("选择会话：边界测试")
	ui.Click("导入所选会话")
	ui.Frame()
	first := welcomeJob(t, c, "conversation.import")
	in := first.Input.(protocol.ConversationImportInput)
	if in.SourceRef != "one" {
		t.Fatal("unstable import order", in)
	}
	s := domain.Session{ID: "imported-one", WorkspaceID: in.WorkspaceID, HarnessID: "memory", Title: "旋转测试", Status: "completed"}
	b, _ := json.Marshal(s)
	first.Apply(v.Model, b, nil)
	ui.Frame()
	second := welcomeJob(t, c, "conversation.import")
	second.Apply(v.Model, nil, errors.New("读取失败"))
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("已导入 1 个，1 个未能完成") {
		t.Fatal("partial import lost success", ui.Texts())
	}
	if err := ui.Click("导入所选会话"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	retry := welcomeJob(t, c, "conversation.import")
	if retry.Input.(protocol.ConversationImportInput).SourceRef != "two" {
		t.Fatal("retry repeated successful source", retry)
	}
	if v.Model.Drafts["home"] != draft {
		t.Fatal("import consumed composer draft")
	}
}
func TestWelcomeImportSkipInvalidatesInFlightDiscovery(t *testing.T) {
	m := importModel()
	m.FirstRun = true
	c := app.NewController("")
	v := New(m, c, "")
	f := v.setupFlow()
	f.step = welcomeImport
	w := m.DeviceStates["local"].Snapshot.Workspaces[0]
	v.openConversationImport("local", w, true)
	ui := n.NewTester(v.View, 1120, 840)
	ui.Advance(time.Second)
	job := welcomeJob(t, c, "conversation.list")
	if err := ui.Click("跳过导入"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(time.Second)
	b, _ := json.Marshal(importCatalog())
	job.Apply(m, b, nil)
	ui.Frame()
	if m.FirstRun || m.SessionID != "" || v.conversationImport != nil || m.Drafts["home"] != "保留首页草稿" {
		t.Fatal("skip lost state or late discovery reopened", m.SessionID)
	}
}

func TestWelcomeImportBackReusesAdmittedWorkspace(t *testing.T) {
	m := importModel()
	m.FirstRun = true
	c := app.NewController("")
	v := New(m, c, "")
	w := m.DeviceStates["local"].Snapshot.Workspaces[0]
	w.Folders = []string{"/Users/local/project"}
	m.DeviceStates["local"].Snapshot.Workspaces[0] = w
	m.RefreshDevices()
	f := v.setupFlow()
	f.step = welcomeImport
	f.workspace = w
	f.name = w.Name
	f.directory = w.Folders[0]
	v.openConversationImport("local", w, true)
	ui := n.NewTester(v.View, 1120, 840)
	ui.Advance(time.Second)
	ui.Click("取消导入")
	ui.Advance(configurationTabDuration)
	if err := ui.Click("下一步"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	ui.Advance(configurationTabDuration)
	if f.step != welcomeImport || m.SessionID != "" {
		t.Fatal("back/next created a runtime session")
	}
	for len(c.Jobs) > 0 {
		if j := <-c.Jobs; j.Method == "workspace.create" {
			t.Fatal("back/next duplicated workspace")
		}
	}
}

func TestDisconnectDuringImportStillAllowsCancelAndDropsLateReply(t *testing.T) {
	v, c, ui := showImport(t)
	ui.Click("选择会话：旋转测试")
	ui.Click("导入所选会话")
	ui.Frame()
	j := welcomeJob(t, c, "conversation.import")
	v.Model.DeviceStates["local"].Connected = false
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("取消导入"); err != nil {
		t.Fatal("disconnect trapped import dialog", err)
	}
	ui.Advance(time.Second)
	b, _ := json.Marshal(domain.Session{ID: "late", WorkspaceID: v.Model.WorkspaceID, HarnessID: "memory"})
	j.Apply(v.Model, b, nil)
	ui.Frame()
	if v.conversationImport != nil || v.Model.SessionID != "" {
		t.Fatal("late import reopened dialog or stole session")
	}
}

func TestWelcomeImportBackMovesItsContentTopContinuously(t *testing.T) {
	v, _, ui := showImport(t)
	u := v.conversationImport
	u.welcome = true
	v.Model.FirstRun = true
	f := v.setupFlow()
	f.step = welcomeImport
	f.workspace = u.workspace
	f.name = u.workspace.Name
	f.directory = u.workspace.Folders[0]
	ui.Frame()
	ui.Advance(time.Second)
	before, _ := ui.Find("欢迎内容")
	ui.Click("取消导入")
	ui.Advance(50 * time.Millisecond)
	mid, _ := ui.Find("欢迎内容")
	ui.Advance(time.Second)
	end, _ := ui.Find("欢迎内容")
	if mid.Y <= before.Y+1 || mid.Y >= end.Y-1 {
		t.Fatal("welcome import changes its top in one frame", before, mid, end)
	}
}
