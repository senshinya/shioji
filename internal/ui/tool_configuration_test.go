package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestDeviceAgentMCPIsIndependentConfigurationTab(t *testing.T) {
	m := deviceUIModel()
	m.DeviceStates["local"].WorkerMinor = 24
	v := New(m, app.NewController(""), "")
	info := setupCatalog()[0]
	info.Capabilities.ToolServices = true
	e := agentEditor([]domain.HarnessInfo{info})
	e.edit(info)
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(700).Height(600).Children(func() { v.agentConfiguration(c, c.Theme(), "local", true, e, info, false, func() {}) })
	}, 850, 800)
	ui.Advance(time.Second)
	if err := ui.Click("MCP"); err != nil {
		t.Fatal("missing independent MCP tab", err, ui.Texts())
	}
	ui.Advance(time.Second)
	if ui.HasText("可执行文件") || ui.HasText("标题生成") {
		t.Fatal("general/model settings leaked into MCP tab", ui.Texts())
	}
	if _, ok := ui.Find("MCP 服务列表"); !ok {
		t.Fatal("MCP list absent from selected tab", ui.Texts())
	}
}

func TestToolSaveReturnsBeforeRefreshAndKeepsRecoveryVisible(t *testing.T) {
	v, _, ui := serviceUI(t)
	v.Model.DeviceStates["local"].WorkerMinor = 24
	service := domain.ToolService{ID: "files", Name: "Files", Editable: true, State: domain.ToolServiceReady, Configuration: &domain.ToolServiceConfiguration{Name: "files", Transport: "command", Enabled: true, Command: "files"}}
	body, _ := json.Marshal(domain.ToolServiceCatalog{CanAdd: true, CanRefresh: true, Revision: "one", Services: []domain.ToolService{service}})
	welcomeJob(t, v.Controller, "runtime.tools.list").Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("编辑 MCP：Files"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	feedbackClick(t, ui, "刷新 MCP 配置")
	oldCheck := welcomeJob(t, v.Controller, "runtime.tools.refresh")
	if err := ui.Click("保存 MCP 服务"); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(domain.ToolServiceChangeResult{Saved: true})
	welcomeJob(t, v.Controller, "runtime.tools.change").Apply(v.Model, body, nil)
	check := welcomeJob(t, v.Controller, "runtime.tools.refresh")
	oldCheck.Apply(v.Model, nil, errors.New("pre-save check failed"))
	ui.Frame()
	ui.Advance(time.Second)
	if ui.HasText("MCP 执行命令") || !ui.HasText("已保存，正在检查连接") {
		t.Fatal("confirmed save still waits in the editor", ui.Texts())
	}
	check.Apply(v.Model, nil, errors.New("offline"))
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("配置已保存，连接检查未完成，请刷新核对") {
		t.Fatal("saved configuration lost its recovery guidance", ui.Texts())
	}
	ui.Click("编辑 MCP：Files")
	ui.Advance(time.Second)
	if ui.HasText("MCP 执行命令") {
		t.Fatal("unverified saved configuration still opens editor")
	}
	if err := ui.Click("刷新 MCP"); err != nil {
		t.Fatal("recovery refresh is disabled", err)
	}
	refresh := welcomeJob(t, v.Controller, "runtime.tools.refresh")
	body, _ = json.Marshal(domain.ToolServiceCatalog{CanAdd: true, CanRefresh: true, Revision: "two", Services: []domain.ToolService{service}})
	refresh.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("编辑 MCP：Files"); err != nil {
		t.Fatal("confirmed refresh did not unlock editor", err)
	}
}

func TestToolEditorSavesTypedArgumentsAndRetainsPrivateValues(t *testing.T) {
	v, e, ui := serviceUI(t)
	v.Model.DeviceStates["local"].WorkerMinor = 24
	job := welcomeJob(t, v.Controller, "runtime.tools.list")
	service := domain.ToolService{ID: "opaque/service", Name: "Files", Editable: true, Transport: "command", State: domain.ToolServiceReady, Configuration: &domain.ToolServiceConfiguration{Name: "files", Transport: "command", Enabled: true, Command: "mcp-files", Arguments: []string{"a value with spaces"}, Environment: []domain.ToolServiceValue{{Name: "TOKEN", Retained: true}}}}
	body, _ := json.Marshal(domain.ToolServiceCatalog{CanAdd: true, CanRefresh: true, Revision: "revision-one", Services: []domain.ToolService{service}})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("编辑 MCP：Files"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	label, _ := ui.Find("参数")
	first, _ := ui.Find("MCP 参数 1")
	if math.Abs(float64(label.Y-first.Y)) > 6 {
		t.Fatal("argument label is centered against the entire list", label, first)
	}
	if err := ui.Click("HTTP"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	ui.Click("本地命令")
	ui.Advance(time.Second)
	if err := ui.Click("保存 MCP 服务"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job = welcomeJob(t, v.Controller, "runtime.tools.change")
	in := job.Input.(protocol.ToolServiceInput)
	if job.ConnectionID != "local" || in.HarnessID != e.openID || in.Change == nil || in.Change.Revision != "revision-one" || in.Change.ServiceID != "opaque/service" {
		t.Fatal("configuration changed owning runtime", in)
	}
	config := in.Change.Configuration
	if config == nil || len(config.Arguments) != 1 || config.Arguments[0] != "a value with spaces" || len(config.Environment) != 1 || !config.Environment[0].Retained || config.Environment[0].Value != "" {
		t.Fatal("editor lost typed inputs or retained secret identity", config)
	}
	body, _ = json.Marshal(domain.ToolServiceChangeResult{Saved: true})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	if !ui.HasText("正在检查连接") {
		t.Fatal("save does not hand off to connection check", ui.Texts())
	}
	refresh := welcomeJob(t, v.Controller, "runtime.tools.refresh")
	body, _ = json.Marshal(domain.ToolServiceCatalog{CanAdd: true, Revision: "revision-two", Services: []domain.ToolService{service}})
	refresh.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("已保存，下轮生效") || ui.HasText("MCP 执行命令") {
		t.Fatal("confirmed save did not return to list", ui.Texts())
	}
}
