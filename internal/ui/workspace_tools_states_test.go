package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestWorkspaceToolsNativeStates(t *testing.T) {
	for _, scenario := range []string{"session", "offline", "source-error", "overlays"} {
		for _, dark := range []bool{false, true} {
			t.Run(scenario+map[bool]string{false: "-light", true: "-dark"}[dark], func(t *testing.T) {
				v, _ := workspaceToolFixture(t)
				first := welcomeJob(t, v.Controller, "runtime.tools.inspect")
				u := v.workspaceToolsFor(v.workspaceEditor, "custom")
				catalog := workspaceToolCatalog()
				b, _ := json.Marshal(catalog)
				first.Apply(v.Model, b, nil)
				switch scenario {
				case "session":
					v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: "live-fixture", Title: "红黑树的旋转", HarnessID: "custom", WorkspaceID: "w", ThreadID: "fixture-source", Context: domain.LaunchContext{CWD: "/owning/project"}})
					v.Model.DeviceStates["local"].Snapshot.Sessions = append(v.Model.DeviceStates["local"].Snapshot.Sessions, v.Model.Snapshot.Sessions[len(v.Model.Snapshot.Sessions)-1])
					v.selectWorkspaceToolScope(u, "live-fixture", "")
					v.queryWorkspaceTools(u)
					job := welcomeJob(t, v.Controller, "runtime.tools.inspect")
					catalog.RuntimeKnown = true
					catalog.Entries[0].State = domain.ToolServiceReady
					catalog.Entries[0].Tools = 1
					catalog.Entries[0].Definitions = []domain.ToolServiceDefinition{{Name: "read_file", Description: "Read allowed files"}}
					catalog.Entries[1].State = domain.ToolServiceAuthorizationRequired
					b, _ = json.Marshal(catalog)
					job.Apply(v.Model, b, nil)
				case "offline":
					v.deviceState("local").Connected = false
				case "source-error":
					u.catalog.Entries = u.catalog.Entries[1:]
					u.catalog.Warnings = []string{"项目配置读取失败，显示可读取的继承配置；会话加载状态尚未核对"}
				case "overlays":
					source := u.catalog.Entries[0]
					source.Configuration = &domain.ToolServiceConfiguration{Name: "filesystem", Enabled: false}
					source.EnabledConfigured = true
					u.catalog.Entries[0] = source
					inherited := u.catalog.Entries[1]
					inherited.ID = source.ID
					inherited.Name = source.Name
					inherited.PrimarySource = false
					inherited.EnabledConfigured = false
					inherited.ArgumentCount = 3
					inherited.Configuration = &domain.ToolServiceConfiguration{Name: "filesystem", Enabled: true, Transport: "command", Command: "npx"}
					u.catalog.Entries[1] = inherited
				}
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if scenario == "session" && (!ui.HasText("已连接") || !ui.HasText("需要授权")) {
					t.Fatal(ui.Texts())
				}
				if scenario == "offline" && ui.HasText("已连接") {
					t.Fatal(ui.Texts())
				}
				if scenario == "overlays" && !ui.HasText("stdio · npx · 3 个参数") {
					t.Fatal(ui.Texts())
				}
				saveNativeSkillImage(t, ui, "workspace-mcp-"+scenario+map[bool]string{false: "-light", true: "-dark"}[dark]+".png")
			})
		}
	}
}
