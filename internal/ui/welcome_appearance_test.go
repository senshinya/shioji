package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

// Native rendering with synthetic peer/catalog snapshots. This is visual and
// navigation evidence, not a live Tailnet enrollment or remote deployment.
func TestWelcomeRefactorNativeAppearance(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h float32
	}{{"desktop", 1280, 800}, {"narrow", 850, 650}} {
		for _, dark := range []bool{false, true} {
			theme := "light"
			if dark {
				theme = "dark"
			}
			t.Run(size.name+"-"+theme, func(t *testing.T) {
				m := welcomeNetworkModel()
				info := setupCatalog()[0]
				info.ID, info.Name, info.Icon = "codex", "Codex", "openai"
				info.Account = &domain.RuntimeAccount{Mode: "account", State: "authenticated", Identity: "senshinya@example.com", Subscription: "Pro"}
				info.Models = []domain.Model{{ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol"}, {ID: "gpt-6-luna", Name: "GPT-6 Luna"}, {ID: "gpt-6.1-terra", Name: "GPT-6.1 Terra"}, {ID: "gpt-6-astra", Name: "GPT-6 Astra"}}
				m.DeviceStates["local"].Readiness.Harnesses = []domain.HarnessInfo{info}
				v := New(m, app.NewController(""), "")
				ui := n.NewTester(v.View, int(size.w), int(size.h))
				ui.SetDark(dark)
				ui.Advance(time.Second)
				save := func(phase string) {
					if phase != "slide" {
						ui.Move(24, 72)
						ui.Advance(200 * time.Millisecond)
					}
					saveNativeSkillImage(t, ui, "welcome-"+size.name+"-"+phase+"-"+theme+".png")
				}
				save("entry")
				ui.Click("通过 SSH 连接")
				ui.Advance(80 * time.Millisecond)
				save("slide")
				ui.Advance(time.Second)
				v.sshTarget = "shinya@192.168.7.119"
				ui.Frame()
				ui.Advance(time.Second)
				save("ssh")
				v.sshError = "无法连接 SSH，请检查设备地址与远程登录设置。"
				ui.Frame()
				ui.Advance(time.Second)
				save("failure")
				ui.Click("返回")
				ui.Advance(time.Second)
				ui.Click("从 Tailnet 添加")
				ui.Advance(time.Second)
				ui.Click("选择 Tailnet 设备：MacBook Air")
				ui.Advance(time.Second)
				save("peers")
				ui.Click("继续")
				ui.Advance(time.Second)
				save("tailnet-ssh")
				v.welcome.key, v.welcome.agents = "air", agentEditor([]domain.HarnessInfo{info})
				v.welcomeStep(welcomeAgents, 1)
				ui.Frame()
				ui.Advance(time.Second)
				save("agent")
				ui.Click("配置 Codex")
				ui.Advance(time.Second)
				save("config")
				ui.Click("模型")
				ui.Advance(time.Second)
				save("models")
				if r, ok := ui.Find("保存"); !ok || r.Y+r.H > size.h-20 {
					t.Fatal("model list displaced shared actions", r)
				}
				ui.Click("取消")
				ui.Advance(time.Second)
				f := v.setupFlow()
				f.name, f.directory = "红黑树", "/Users/shinya/Projects/red-black-tree"
				v.welcomeStep(welcomeWorkspace, 1)
				ui.Frame()
				ui.Advance(time.Second)
				save("workspace")
			})
		}
	}
}
