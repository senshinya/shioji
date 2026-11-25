package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestAgentDetailPagesAlignmentForCodexAndAntigravity(t *testing.T) {
	reset := time.Date(2026, 11, 30, 17, 27, 0, 0, time.Local)
	codex := domain.HarnessInfo{
		ID: "codex", Name: "Codex", Icon: "openai", Configured: true, State: domain.RuntimeReady,
		Capabilities: domain.HarnessCapabilities{SkillManagement: true, ToolServices: true, HookManagement: true},
		Models: []domain.Model{
			{ID: "gpt-5.4", Name: "GPT-5.4", Default: true},
			{ID: "gpt-5.4-mini", Name: "GPT-5.4 mini"},
		},
		Account: &domain.RuntimeAccount{Mode: "account", State: "authenticated", Label: "ChatGPT", Identity: "developer@example.com", Subscription: "Pro"},
		Installation: &domain.RuntimeInstallation{
			Executable:    "/Applications/Codex.app/Contents/Resources/codex",
			Configurable:  true,
			SignInMethods: []domain.SignInMethod{domain.SignInBrowser, domain.SignInDeviceCode},
			OptionsSupported: domain.RuntimeOptionSupport{
				Arguments: true, Directory: true, Environment: true, Instructions: true, TitleModel: true,
			},
			Options: domain.RuntimeOptions{
				Environment: []domain.EnvironmentVariable{{Name: "OPENAI_ORG", Value: "team"}, {Name: "RUST_LOG", Value: "info"}},
			},
		},
	}
	antigravity := domain.HarnessInfo{
		ID: "antigravity", Name: "Antigravity", Icon: "antigravity", Configured: true, State: domain.RuntimeReady,
		Capabilities: domain.HarnessCapabilities{SkillManagement: true, ToolServices: true, HookManagement: true},
		Models: []domain.Model{
			{ID: "gemini-3-pro", Name: "Gemini 3 Pro", Default: true},
			{ID: "claude-sonnet", Name: "Claude Sonnet"},
		},
		Account: &domain.RuntimeAccount{
			Mode: "account", State: "authenticated", Label: "Antigravity", Subscription: "Google AI Pro",
			QuotaWindows: []domain.RuntimeQuotaWindow{
				{Group: "Gemini Models", DurationSeconds: 604800, UsedPercent: 1, ResetsAt: &reset},
			},
		},
		Installation: &domain.RuntimeInstallation{
			Executable:    "/opt/homebrew/bin/agy",
			Configurable:  true,
			SignInMethods: []domain.SignInMethod{domain.SignInBrowser},
		},
	}

	for _, tc := range []struct {
		name string
		info domain.HarnessInfo
		dark bool
	}{
		{"codex-light", codex, false},
		{"codex-dark", codex, true},
		{"antigravity-light", antigravity, false},
		{"antigravity-dark", antigravity, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := deviceUIModel()
			state := m.DeviceStates["local"]
			state.WorkerMinor = protocol.SignInManualCodeMinor
			state.Readiness.Harnesses = []domain.HarnessInfo{codex, antigravity}
			v := New(m, app.NewController(""), "")
			e := agentEditor(state.Readiness.Harnesses)
			e.edit(tc.info)
			e.draft.login = true
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).FillWidth().FillHeight().Background(c.Theme().Surface).Children(func() {
					v.deviceAgentPage(c, c.Theme(), "local", "本机", state, e, tc.info)
				})
			}, 950, 670)
			ui.SetDark(tc.dark)
			ui.Advance(time.Second)

			// 1. Header optical alignment between 20px title and 12px status.
			title, okTitle := ui.Find(tc.info.Name)
			status, okStatus := ui.Find("已就绪")
			if !okTitle || !okStatus {
				t.Fatalf("missing header title or status: title=%v status=%v", okTitle, okStatus)
			}
			// Status sits near the vertical center of the 20px title rather than bottom baseline.
			if math.Abs(float64((status.Y+status.H/2)-(title.Y+title.H/2))) > 4 {
				t.Fatalf("header status not optically centered with title: title=%+v status=%+v", title, status)
			}

			// 2. General tab reading edges.
			accountLabel, okAcc := ui.Find("账号")
			execLabel, okExec := ui.Find("可执行文件")
			execField, okField := ui.Find(tc.info.Name + " 可执行文件")
			if !okAcc || !okExec || !okField {
				t.Fatalf("missing general rows: %v", ui.Texts())
			}
			if math.Abs(float64(accountLabel.X-execLabel.X)) > 1 {
				t.Fatalf("general row left edges differ: account=%+v exec=%+v", accountLabel, execLabel)
			}
			if math.Abs(float64(950-configurationInset-execField.X-execField.W)) > 1 {
				t.Fatalf("executable field right edge drifted: field=%+v", execField)
			}

			if tc.info.ID == "antigravity" {
				// Single-method label aligns with the 8 DIP text inset of adjacent controls.
				methodText, ok := ui.Find("浏览器授权")
				if !ok {
					t.Fatal("missing single-method label")
				}
				if math.Abs(float64(methodText.X-(execField.X+configurationFieldInset))) > 1 {
					t.Fatalf("single-method text edge %v differs from control text edge %v", methodText.X, execField.X+configurationFieldInset)
				}
				if !ui.HasText("Google AI Pro") {
					t.Fatalf("missing Antigravity subscription tier: %v", ui.Texts())
				}
				if ui.HasText("额度") {
					t.Fatalf("Antigravity agent page should not show quota row: %v", ui.Texts())
				}
			}
			auditCapture(t, ui, fmt.Sprintf("agent-%s-general", tc.name))

			// 3. Models tab alignment.
			if err := ui.Click("模型"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			auditCapture(t, ui, fmt.Sprintf("agent-%s-models", tc.name))

			// 4. Advanced tab alignment (Codex).
			if tc.info.ID == "codex" {
				if err := ui.Click("高级"); err != nil {
					t.Fatal(err)
				}
				ui.Advance(time.Second)
				argsLabel, ok := ui.Find("启动参数")
				if !ok {
					t.Fatal("missing advanced arguments row")
				}
				if math.Abs(float64(argsLabel.Y-accountLabel.Y)) > 1 {
					t.Fatalf("advanced first row Y %v differs from general first row Y %v", argsLabel.Y, accountLabel.Y)
				}
				envLabel, okEnv := ui.Find("环境变量")
				addEnv, okAdd := ui.Find("添加变量")
				if !okEnv || !okAdd {
					t.Fatal("missing environment row")
				}
				if math.Abs(float64(envLabel.Y-addEnv.Y)) > 8 {
					t.Fatalf("environment label drifted from top row: env=%+v add=%+v", envLabel, addEnv)
				}
				auditCapture(t, ui, fmt.Sprintf("agent-%s-advanced", tc.name))
			}
		})
	}
}
