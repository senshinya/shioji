package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func mcpRefreshUI(t *testing.T, dark bool) (*View, *n.Tester, domain.ToolServiceCatalog) {
	t.Helper()
	m := deviceUIModel()
	m.DeviceStates["local"].WorkerMinor = 24
	v := New(m, app.NewController(""), "")
	info := setupCatalog()[0]
	info.Capabilities.ToolServices = true
	e := agentEditor([]domain.HarnessInfo{info})
	e.edit(info)
	e.setup, e.tab, e.workerMinor = false, "MCP", 24
	v.devices, v.deviceSelected = true, "local"
	v.toolServicesFor("local", info.ID, e).open = true
	ui := n.NewTester(func(c *n.Context) {
		theme := applyAppearance(c, v.Model.Preferences)
		v.frameTime = c.Now()
		v.syncToolServiceAuthorizations(c)
		n.Column(c).Fill().Padding(24).Background(theme.Background).Children(func() {
			v.agentConfiguration(c, theme, "local", true, e, info, false, func() {})
		})
	}, 860, 500)
	ui.SetDark(dark)
	catalog := domain.ToolServiceCatalog{CanAdd: true, CanRefresh: true, Revision: "one", Services: []domain.ToolService{{ID: "docs", Name: "Documentation", State: domain.ToolServiceReady, Tools: 2}}}
	body, _ := json.Marshal(catalog)
	welcomeJob(t, v.Controller, "runtime.tools.list").Apply(v.Model, body, nil)
	ui.Frame()
	ui.Move(820, 460)
	ui.Advance(time.Second)
	return v, ui, catalog
}

func TestMCPRefreshProgressReplacesTheButtonWithoutMovingActions(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui, catalog := mcpRefreshUI(t, dark)
			idle, _ := ui.Find("刷新 MCP")
			add, _ := ui.Find("添加 MCP 服务")
			if idle.W != 28 || idle.H != 28 || add.W != 28 || add.H != 28 {
				t.Fatal("MCP actions lost their standard hit areas", idle, add)
			}
			if err := ui.Click("刷新 MCP"); err != nil {
				t.Fatal(err)
			}
			job := welcomeJob(t, v.Controller, "runtime.tools.refresh")
			for frame := 0; frame < 20; frame++ {
				ui.Advance(time.Second / 60)
				refresh, _ := ui.Find("刷新 MCP")
				adding, _ := ui.Find("添加 MCP 服务")
				progress, ok := ui.Find("MCP 刷新进度")
				if !ok || refresh != idle || adding != add || progress.X < refresh.X || progress.Y < refresh.Y || progress.X+progress.W > refresh.X+refresh.W || progress.Y+progress.H > refresh.Y+refresh.H {
					t.Fatal("loading progress must occupy the existing refresh button", idle, refresh, add, adding, progress)
				}
				if !ui.HasText("Documentation") {
					t.Fatal("refresh removed existing service rows")
				}
			}
			ui.Click("刷新 MCP")
			if len(v.Controller.Jobs) != 0 {
				t.Fatal("busy refresh dispatched another request")
			}
			body, _ := json.Marshal(catalog)
			job.Apply(v.Model, body, nil)
			ui.Frame()
			ui.Advance(time.Second)
			if _, active := ui.Find("MCP 刷新进度"); active {
				t.Fatal("completed refresh kept the loading glyph")
			}
			if refresh, _ := ui.Find("刷新 MCP"); refresh != idle {
				t.Fatal("completion moved refresh", idle, refresh)
			}
		})
	}
}

func TestMCPRefreshHeaderAlignsVisibleInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui, _ := mcpRefreshUI(t, dark)
		ui.SetScale(2)
		ui.SetPreferences(n.Preferences{ReduceMotion: true})
		for _, loading := range []bool{false, true} {
			if loading {
				ui.Click("刷新 MCP")
				welcomeJob(t, v.Controller, "runtime.tools.refresh")
			}
			ui.Move(820, 460)
			ui.Frame()
			center := func(label string) float64 {
				r, ok := ui.Find(label)
				if !ok {
					t.Fatal("missing header element", label)
				}
				if label == "MCP" {
					// The tab has the same text. Read the actual section title
					// immediately above its native service-list viewport.
					list, _ := ui.Find("MCP 服务列表")
					r = n.Rect{X: list.X, Y: list.Y - configurationSectionHeader, W: 60, H: configurationSectionHeader}
				}
				im := ui.Image()
				bg := im.RGBAAt(0, 0)
				low, high := im.Bounds().Max.Y, -1
				for y := int(r.Y * 2); y < int((r.Y+r.H)*2); y++ {
					for x := int(r.X * 2); x < int((r.X+r.W)*2); x++ {
						p := im.RGBAAt(x, y)
						if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
							low = min(low, y)
							high = max(high, y)
						}
					}
				}
				if high < 0 {
					t.Fatal("missing header ink", label, r)
				}
				return float64(low+high+1) / 4
			}
			title := center("MCP")
			for _, label := range []string{"刷新 MCP", "添加 MCP 服务"} {
				if delta := math.Abs(center(label) - title); delta > 1 {
					t.Errorf("%s is not aligned with the MCP cap-height center: %.2f DIP (loading=%v)", label, delta, loading)
				}
			}
		}
	}
}

func TestMCPRefreshNativeRecording(t *testing.T) {
	root := os.Getenv("SHIOJI_MCP_REFRESH_CAPTURE")
	if root == "" {
		t.Skip("set SHIOJI_MCP_REFRESH_CAPTURE for native refresh videos")
	}
	for _, dark := range []bool{true, false} {
		v, ui, catalog := mcpRefreshUI(t, dark)
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(root, fmt.Sprint(dark)))
		body, _ := json.Marshal(catalog)
		var finish func(bool)
		for frame := 0; frame < 540; frame++ {
			switch frame {
			case 60, 240, 420:
				if frame == 420 {
					ui.SetPreferences(n.Preferences{ReduceMotion: true})
				}
				if err := ui.Click("刷新 MCP"); err != nil {
					t.Fatal(err)
				}
				job := welcomeJob(t, v.Controller, "runtime.tools.refresh")
				finish = func(failed bool) {
					if failed {
						job.Apply(v.Model, nil, errors.New("连接检查失败，请重试"))
					} else {
						job.Apply(v.Model, body, nil)
					}
					ui.Frame()
				}
			case 180, 480:
				finish(false)
			case 360:
				finish(true)
			}
			ui.Move(820, 460)
			saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			ui.Advance(time.Second / 60)
		}
	}
}
