package ui

import (
	"encoding/json"
	n "github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

// These render the production native view with explicit catalog fixtures. The
// owning worker/native Codex path is tested separately in worker_test.
func TestSkillManagementNativeAppearance(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, project := range []bool{false, true} {
			name := "device-light"
			if project {
				name = "workspace-light"
			}
			if dark {
				name = name[:len(name)-5] + "dark"
			}
			t.Run(name, func(t *testing.T) {
				m := deviceUIModel()
				state := m.DeviceStates["local"]
				state.WorkerMinor = 25
				info := setupCatalog()[0]
				info.ID, info.Name, info.Icon = "codex", "Codex", "openai"
				info.Capabilities.SkillManagement = true
				info.Capabilities.ToolServices = true
				state.Readiness.Harnesses = []domain.HarnessInfo{info}
				for i := range m.Snapshot.Workspaces {
					if m.Snapshot.Workspaces[i].ID == "w" {
						m.Snapshot.Workspaces[i].Name = "红黑树"
						m.Snapshot.Workspaces[i].Folders = []string{"/Users/shinya/Projects/red-black-tree"}
					}
				}
				v := New(m, app.NewController(""), "")
				if project {
					for _, w := range m.Snapshot.Workspaces {
						if w.ID == "w" {
							v.editWorkspace(w)
							v.workspaceEditor.Tab = "Skills"
							v.workspaceEditor.Harness = "codex"
						}
					}
				} else {
					v.devices = true
					v.deviceSelected = "local"
				}
				ui := n.NewTester(v.View, 1240, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if !project {
					catalog, _ := json.Marshal(domain.Readiness{Harnesses: []domain.HarnessInfo{info}})
					welcomeJob(t, v.Controller, "runtime.catalog").Apply(m, catalog, nil)
					ui.Frame()
					ui.Advance(time.Second)
					if err := ui.Click("Agent"); err != nil {
						t.Fatal(err)
					}
					ui.Advance(time.Second)
					if err := ui.Click("打开 Codex 配置"); err != nil {
						t.Fatal(err, ui.Texts())
					}
					ui.Advance(time.Second)
					if err := ui.Click("Skills"); err != nil {
						t.Fatal(err, ui.Texts())
					}
					ui.Advance(time.Second)
				}
				inventory := domain.SkillInventory{Revision: "fixture"}
				for _, r := range []struct{ name, description string }{{"check", "审查改动并完成提交前检查"}, {"design", "设计界面并检查视觉层级与交互"}, {"debugging", "从错误现场追踪问题的根本原因"}, {"read", "阅读页面与文档并保留来源"}, {"write", "调整技术文案的结构与表达"}} {
					inventory.Entries = append(inventory.Entries, domain.ManagedSkill{SkillReference: domain.SkillReference{ID: r.name, Name: r.name}, Description: r.description, Scope: domain.SkillUser, Directory: "/Users/shinya/.agents/skills", Path: "/Users/shinya/.agents/skills/" + r.name + "/SKILL.md", Enabled: true, CanEnable: !project})
				}
				inventory.Entries = append(inventory.Entries, domain.ManagedSkill{SkillReference: domain.SkillReference{ID: "system", Name: "skill-creator"}, Description: "创建或更新 Skill", Scope: domain.SkillBuiltIn, Directory: "/Users/shinya/.codex/skills/.system", Enabled: true})
				if project {
					inventory.Entries = append(inventory.Entries, domain.ManagedSkill{SkillReference: domain.SkillReference{ID: "tree", Name: "go-test"}, Description: "运行 Go 测试并分析失败原因", Scope: domain.SkillProject, Directory: "/Users/shinya/Projects/red-black-tree/.agents/skills", Path: "/Users/shinya/Projects/red-black-tree/.agents/skills/go-test/SKILL.md", Enabled: true, CanEnable: true})
				}
				body, _ := json.Marshal(inventory)
				welcomeJob(t, v.Controller, "runtime.skills.list").Apply(m, body, nil)
				ui.Frame()
				ui.Advance(time.Second)
				if !ui.HasText("check") {
					t.Fatal("native skill list did not render", ui.Texts())
				}
				search, _ := ui.Find("搜索 Skills")
				rows, _ := ui.Find("Skills 列表")
				if search.X < 230 || rows.Y <= search.Y || rows.H < 300 {
					t.Fatal("native content lost full-height alignment", search, rows)
				}
				saveNativeSkillImage(t, ui, name+".png")
				target := "check"
				id := "check"
				if project {
					target, id = "go-test", "tree"
				}
				if err := ui.Click("查看 Skill：" + target); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				doc, _ := json.Marshal(domain.SkillDocument{ID: id, Text: "# 使用场景\n\n检查当前改动与测试结果。\n\n## 工作步骤\n\n1. 读取项目约定。\n2. 运行相关测试。\n3. 报告失败位置。\n\n```go\nfunc rotateLeft(root *Node) *Node {\n    return root\n}\n```"})
				welcomeJob(t, v.Controller, "runtime.skills.read").Apply(m, doc, nil)
				ui.Frame()
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, name+"-detail.png")
			})
		}
	}
}
func saveNativeSkillImage(t *testing.T, ui *n.Tester, name string) {
	t.Helper()
	dir := os.Getenv("SHIOJI_SKILL_SNAPSHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, ui.Image()); err != nil {
		t.Fatal(err)
	}
}
