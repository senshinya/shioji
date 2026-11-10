package ui

import (
	"encoding/json"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestWorkspaceToolDetailShowsSourceAndActualConnectionWithoutFormDefaults(t *testing.T) {
	v, ui := workspaceToolFixture(t)
	catalog := workspaceToolCatalog()
	catalog.Entries[0].EnabledConfigured = false
	b, _ := json.Marshal(catalog)
	welcomeJob(t, v.Controller, "runtime.tools.inspect").Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("查看 MCP：filesystem"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText("未设置") || ui.HasText("启用状态") || ui.HasText("命令 / 地址") {
		t.Fatal("readonly detail still looks like an incomplete form", ui.Texts())
	}
	if !ui.HasText("命令") || !ui.HasText("mcp-files") || !ui.HasText("/owning/project/.codex/config.toml") {
		t.Fatal("actual connection or source lost", ui.Texts())
	}
	source, ok := ui.Find("MCP 配置来源路径")
	if !ok {
		t.Fatal("source not grouped under title")
	}
	command, ok := ui.Find("命令")
	if !ok || command.Y < source.Y || command.Y-source.Y > 100 {
		t.Fatal("detail metadata is excessively spaced", source, command)
	}
	if err := ui.Click("复制 MCP 配置路径"); err != nil {
		t.Fatal(err)
	}
	if ui.Clipboard() != "/owning/project/.codex/config.toml" {
		t.Fatal("copied the wrong source path", ui.Clipboard())
	}
}

func TestWorkspaceToolDetailOmitsFieldsAbsentFromPartialSource(t *testing.T) {
	v, ui := workspaceToolFixture(t)
	catalog := workspaceToolCatalog()
	catalog.Entries[0].Configuration = &domain.ToolServiceConfiguration{Enabled: false}
	catalog.Entries[0].EnabledConfigured = true
	b, _ := json.Marshal(catalog)
	welcomeJob(t, v.Controller, "runtime.tools.inspect").Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("查看 MCP：filesystem"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if ui.HasText("未设置") || ui.HasText("连接") || ui.HasText("连接方式") || ui.HasText("命令 / 地址") {
		t.Fatal("partial source fabricated empty connection fields", ui.Texts())
	}
	if !ui.HasText("已停用") || !ui.HasText("/owning/project/.codex/config.toml") {
		t.Fatal("partial source state missing", ui.Texts())
	}
}

func TestWorkspaceToolDetailRetainsSourceWarnings(t *testing.T) {
	v, ui := workspaceToolFixture(t)
	catalog := workspaceToolCatalog()
	catalog.Entries = catalog.Entries[1:]
	warning := "项目配置读取失败，显示可读取的继承配置"
	catalog.Warnings = []string{warning}
	b, _ := json.Marshal(catalog)
	welcomeJob(t, v.Controller, "runtime.tools.inspect").Apply(v.Model, b, nil)
	ui.Advance(time.Second)
	if err := ui.Click("查看 MCP：Context7"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText(warning) {
		t.Fatal("opening inherited detail hid source warning", ui.Texts())
	}
}
