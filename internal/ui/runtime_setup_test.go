package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSetupActionsStayOnSelectedDevice(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerMinor = 16
	state.Readiness = domain.Readiness{State: domain.RuntimeUnavailable, Harnesses: []domain.HarnessInfo{{Configured: true, ID: "custom", Name: "Custom Agent", State: domain.RuntimeUnavailable, Installation: &domain.RuntimeInstallation{Configurable: true, Issue: domain.RuntimeMissing, Guide: "在设备终端安装"}}}}
	controller := app.NewController("")
	v := New(m, controller, "")
	v.devices = true
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "runtime.catalog" {
			data, _ := json.Marshal(state.Readiness)
			job.Apply(m, data, nil)
		}
	}
	ui.Frame()
	if !ui.HasText("未安装") {
		t.Fatal("setup cause missing", ui.Texts())
	}
	if _, ok := ui.Find("Custom Agent 可执行文件"); !ok {
		if err := ui.Click("配置 Custom Agent 路径"); err != nil {
			t.Fatal(err)
		}
	}
	ui.Advance(250 * time.Millisecond)
	if err := ui.Click("Custom Agent 可执行文件"); err != nil {
		t.Fatal(err)
	}
	ui.Type("/Users/remote/bin/custom")
	ui.Advance(250 * time.Millisecond)
	if err := ui.Click("保存 Custom Agent 路径"); err != nil {
		t.Fatal(err)
	}
	var found *app.Job
	for len(controller.Jobs) > 0 {
		j := <-controller.Jobs
		if j.Method == "runtime.agents" {
			found = &j
		}
	}
	if found == nil || found.ConnectionID != "air" || found.ConnectionEpoch != "b" {
		t.Fatal("setup went to wrong device", found)
	}
	if in := found.Input.(protocol.DeviceAgents); len(in.Agents) != 1 || in.Agents[0].HarnessID != "custom" || in.Agents[0].Executable != "/Users/remote/bin/custom" {
		t.Fatal("remote path changed", in)
	}
}

func TestDeviceAgentStatusUpdatesWithinSameConnectionAndKeepsPathDraft(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerMinor = 16
	state.Readiness = domain.Readiness{State: domain.RuntimeReady, Harnesses: []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}}}
	controller := app.NewController("")
	v := New(m, controller, "")
	v.devices = true
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "runtime.catalog" {
			b, _ := json.Marshal(state.Readiness)
			job.Apply(m, b, nil)
		}
	}
	ui.Frame()
	if _, ok := ui.Find("Custom Agent 可执行文件"); !ok {
		if err := ui.Click("配置 Custom Agent 路径"); err != nil {
			t.Fatal(err)
		}
	}
	ui.Advance(250 * time.Millisecond)
	ui.Click("Custom Agent 可执行文件")
	ui.Type("/Users/remote/draft")
	ui.Advance(250 * time.Millisecond)
	state.Readiness.Harnesses[0].Installation = &domain.RuntimeInstallation{Configurable: true, Issue: domain.RuntimeAuthenticationExpired}
	state.Readiness.Harnesses[0].State = domain.RuntimeUnavailable
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	if !ui.HasText("登录已失效") {
		t.Fatal("same-epoch status stale", ui.Texts())
	}
	ui.Click("保存 Custom Agent 路径")
	found := false
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "runtime.agents" {
			found = true
			in := job.Input.(protocol.DeviceAgents)
			if len(in.Agents) != 1 || in.Agents[0].Executable != "/Users/remote/draft" {
				t.Fatal("path draft lost", in)
			}
		}
	}
	if !found {
		t.Fatal("missing save")
	}
}

func TestPendingSSHLoginCommandUsesRemoteDeviceBeforeAdding(t *testing.T) {
	v := New(app.NewModel(), nil, "")
	v.sshTarget = "remote@air"
	v.sshPort = "2222"
	got := v.agentLoginCommand("", []string{"/remote/bin/agent", "login"})
	if !strings.Contains(got, "ssh -t -p 2222 -- 'remote@air'") || !strings.Contains(got, "/remote/bin/agent") {
		t.Fatal("onboarding login would run locally", got)
	}
}

func TestSavingDisabledAgentDoesNotEnableIt(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerMinor = 18
	controller := app.NewController("")
	v := New(m, controller, "")
	e := agentEditor([]domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: false, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}})
	e.epoch = state.ConnectionEpoch
	e.edit(e.catalog[0])
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).FillWidth().FillHeight().Children(func() { v.deviceAgentPage(c, c.Theme(), "air", "MacBook Air", state, e, e.catalog[0]) })
	}, 1100, 820)
	ui.Advance(time.Second)
	if err := ui.Click("Custom Agent 可执行文件"); err != nil {
		t.Fatal(err)
	}
	ui.Type("/usr/bin/true")
	ui.Advance(250 * time.Millisecond)
	if err := ui.Click("保存 Custom Agent 路径"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	configured := false
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "runtime.agents" {
			t.Fatal("saving disabled Agent enabled it")
		}
		if job.Method == "runtime.configure" {
			configured = true
		}
	}
	if !configured {
		t.Fatal("disabled Agent settings not saved", ui.Texts(), m.Error)
	}
}

func TestAgentInstallDocumentationSharesGuidanceRow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{850, 1240} {
			t.Run(fmt.Sprintf("dark-%v-width-%d", dark, width), func(t *testing.T) {
				const guide = "在该设备的终端安装或登录，然后重新检测"
				m := deviceUIModel()
				m.Preferences.Appearance = "light"
				if dark {
					m.Preferences.Appearance = "dark"
				}
				m.DeviceStates["air"].Readiness = domain.Readiness{State: domain.RuntimeUnavailable, Harnesses: []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeUnavailable, Installation: &domain.RuntimeInstallation{Configurable: true, Issue: domain.RuntimeMissing, Guide: guide, Documentation: "https://example.test/install"}}}}
				v := New(m, nil, "")
				v.devices, v.deviceSelected = true, "air"
				ui := n.NewTester(v.View, width, 820)
				ui.SetDark(dark)
				ui.SetScale(2)
				ui.Advance(time.Second)
				if e := ui.Click("配置 Custom Agent 路径"); e != nil {
					t.Fatal(e)
				}
				ui.Advance(time.Second)
				hint, ok := ui.Find(guide)
				if !ok {
					t.Fatal("missing guidance", ui.Texts())
				}
				button, ok := ui.Find("安装说明")
				if !ok {
					t.Fatal("missing documentation action", ui.Texts())
				}
				field, ok := ui.Find("Custom Agent 可执行文件")
				if !ok {
					t.Fatal("missing executable field")
				}
				if dir := os.Getenv("SHIOJI_INSTALL_GUIDANCE_EVIDENCE"); dir != "" {
					if e := os.MkdirAll(dir, 0700); e != nil {
						t.Fatal(e)
					}
					// Real native pixels, with the account/input rows retained for context.
					im := ui.Image()
					r := image.Rect(int((hint.X-12)*2), int((field.Y-48)*2), int((field.X+field.W+12)*2), int((max(hint.Y+hint.H, button.Y+button.H)+16)*2)).Intersect(im.Bounds())
					f, e := os.Create(filepath.Join(dir, fmt.Sprintf("dark-%v-width-%d.png", dark, width)))
					if e != nil {
						t.Fatal(e)
					}
					e = png.Encode(f, im.SubImage(r))
					f.Close()
					if e != nil {
						t.Fatal(e)
					}
				}
				if math.Abs(float64(button.Y+button.H/2-hint.Y-9)) > 1.5 {
					t.Fatal("documentation is not beside the guidance's first line", hint, button)
				}
				if math.Abs(float64(button.X+button.W+8-field.X-field.W)) > 1 {
					t.Fatal("documentation does not share the form's right edge", button, field)
				}
				if hint.X+hint.W > button.X-4 {
					t.Fatal("guidance overlaps documentation", hint, button)
				}
			})
		}
	}
}
