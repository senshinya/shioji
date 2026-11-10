package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestWorkspaceToolsNativeAppearance(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1240, 850} {
			name := "workspace-mcp-light"
			if dark {
				name = "workspace-mcp-dark"
			}
			if width < 900 {
				name += "-narrow"
			}
			t.Run(name, func(t *testing.T) {
				v, _ := workspaceToolFixture(t)
				job := welcomeJob(t, v.Controller, "runtime.tools.inspect")
				catalog := workspaceToolCatalog()
				for i := 0; i < 9; i++ {
					e := catalog.Entries[1]
					e.EntryID += "-" + string(rune('a'+i))
					e.Name += " " + string(rune('A'+i))
					catalog.Entries = append(catalog.Entries, e)
				}
				body, _ := json.Marshal(catalog)
				job.Apply(v.Model, body, nil)
				height := 820
				if width == 850 {
					height = 600
				}
				ui := n.NewTester(v.View, width, height)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if !ui.HasText("filesystem") {
					t.Fatal("native page absent", ui.Texts())
				}
				list, ok := ui.Find("工作区 MCP 列表")
				if !ok || list.H < 250 || list.W > float32(width) {
					t.Fatal("unbounded list", list)
				}
				status, ok := ui.Find("已配置")
				if !ok || status.X+status.W > list.X+list.W+.5 {
					t.Fatal("status passes right edge", status, list)
				}
				saveNativeSkillImage(t, ui, name+".png")
				if err := ui.Click("查看 MCP：filesystem"); err != nil {
					t.Fatal(err)
				}
				ui.Frame()
				ui.Advance(100 * time.Millisecond)
				saveNativeSkillImage(t, ui, name+"-sliding.png")
				ui.Advance(time.Second)
				if !ui.HasText("配置文件") {
					t.Fatal("detail missing", ui.Texts())
				}
				saveNativeSkillImage(t, ui, name+"-detail.png")
			})
		}
	}
}
