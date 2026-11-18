package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestConfigurationRowsShareRhythmAndSwitchAlignment(t *testing.T) {
	for _, dark := range []bool{false, true} {
		ui := n.NewTester(func(c *n.Context) {
			value := true
			n.Column(c).Width(560).Children(func() {
				settingsRow(c, c.Theme(), "系统通知", func() { configurationSwitch(c, &value).Label("设置开关") })
				deviceConfigRow(c, "登录后启动服务", func() {
					n.Row(c).FillWidth().Justify(n.End).Children(func() { configurationSwitch(c, &value).Label("设备开关") })
				})
				setupRow(c, "前台也通知", func() {
					n.Row(c).FillWidth().Justify(n.End).Children(func() { configurationSwitch(c, &value).Label("配置开关") })
				})
			})
		}, 560, 160)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		var prev n.Rect
		for i, label := range []string{"设置开关", "设备开关", "配置开关"} {
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("missing switch", label)
			}
			if i > 0 && (math.Abs(float64(r.Y-prev.Y-40)) > 1 || math.Abs(float64(r.X+r.W-prev.X-prev.W)) > 1) {
				t.Fatal("forms differ in rhythm or right edge", dark, label, prev, r)
			}
			prev = r
		}
	}
}

func TestNarrowDeviceHeaderAndBodyShareLeftEdge(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	v.devices = true
	v.deviceSelected = "local"
	v.deviceTab = "settings"
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)
	title, _ := ui.Find(v.Model.LocalName())
	icon, iconOK := ui.Find("设备图标")
	body, ok := ui.Find("执行服务")
	if !ok || !iconOK || math.Abs(float64(icon.X+8-body.X)) > 1 || title.X < icon.X+icon.W {
		t.Fatal("narrow header group and body are not aligned", icon, title, body)
	}
}

func TestNarrowDeviceBackActionClearsNativeWindowControls(t *testing.T) {
	v := New(deviceUIModel(), nil, "")
	v.devices = true
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)
	back, ok := ui.Find("返回设备列表")
	if !ok || back.X < 80 && back.Y < 32 {
		t.Fatal("compact toolbar overlaps native traffic lights", back)
	}
}

func TestWideDevicePagesShareHeaderAndBodyEdges(t *testing.T) {
	for _, width := range []float32{1240, 1800} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%.0f-dark-%v", width, dark), func(t *testing.T) {
				m := deviceUIModel()
				m.DeviceStates["air"].ConnectionEpoch = ""
				m.DeviceStates["air"].WorkerMinor = 22
				m.DeviceStates["air"].Readiness.Harnesses = []domain.HarnessInfo{{ID: "custom", Name: "Custom Agent", Configured: true, State: domain.RuntimeReady, Installation: &domain.RuntimeInstallation{Configurable: true}}}
				v := New(m, nil, "")
				v.devices = true
				v.deviceSelected = "air"
				v.deviceTab = "agents"
				ui := n.NewTester(v.View, int(width), 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				arrow, ok := ui.Find("打开 Custom Agent 配置")
				if !ok || math.Abs(float64(width-configurationInset-8-arrow.X-arrow.W)) > 1 {
					t.Fatal("agent list does not reach header right edge", width, arrow)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("device-width-agents-%.0f-dark-%v.png", width, dark))
				ui.Click("设备设置")
				ui.Advance(time.Second)
				heading, _ := ui.Find("执行服务")
				if math.Abs(float64(width-configurationInset-heading.X-heading.W)) > 1 {
					t.Fatal("device settings section does not share page edges", width, heading)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("device-width-settings-%.0f-dark-%v.png", width, dark))
				ui.Click("Agent")
				ui.Advance(time.Second)
				ui.Click("打开 Custom Agent 配置")
				ui.Advance(time.Second)
				field, ok := ui.Find("Custom Agent 可执行文件")
				if !ok || math.Abs(float64(width-configurationInset-field.X-field.W)) > 1 {
					t.Fatal("agent configuration does not share header right edge", width, field)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("device-width-config-%.0f-dark-%v.png", width, dark))
			})
		}
	}
}

func TestWideWorkspaceSettingsUsesAvailableWidth(t *testing.T) {
	for _, width := range []int{850, 1240, 1800} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("width-%d-dark-%v", width, dark), func(t *testing.T) {
				m := deviceUIModel()
				m.LocalDeviceName = "MacMini at Home"
				m.DeviceStates["local"].WorkerMinor = 5
				v := New(m, app.NewController(""), "")
				v.editWorkspace(m.Snapshot.Workspaces[0])
				v.workspaceEditor.Workspace.Name = "shioji"
				v.workspaceEditor.Workspace.Folders = []string{"/Users/shinya/Downloads/tahuna", "/Users/shinya/Projects/红黑树与边界条件/very-long-workspace-directory/another-folder"}
				v.workspaceEditor.Main = v.workspaceEditor.Workspace.Folders[0]
				ui := n.NewTester(v.View, width, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				field, ok := ui.Find("工作区名称")
				title, _ := ui.Find("shioji")
				back, backOK := ui.Find("返回会话")
				cancel, _ := ui.Find("取消工作区编辑")
				add, _ := ui.Find("添加目录")
				frame := ui.Image()
				background := frame.RGBAAt(int(field.X-6), int(title.Y+title.H/2))
				headerRight := sidebarInkBounds(t, frame, n.Rect{X: field.X + field.W - 180, Y: title.Y, W: 180, H: title.H}, 1, background).Max.X
				if !ok || math.Abs(float64(float32(width)-configurationInset-field.X-field.W)) > 1 || math.Abs(float64(title.X-field.X)) > 1 || math.Abs(float64(cancel.X-field.X)) > 1 {
					t.Fatal("workspace settings lost its full width or original shared left edge", title, field, cancel)
				}
				if !backOK || math.Abs(float64(back.X-field.X)) > 1 {
					t.Errorf("workspace back label does not share the form's left edge: back=%+v form=%+v", back, field)
				}
				for _, path := range v.workspaceEditor.Workspace.Folders {
					remove, _ := ui.Find("移除目录：" + path)
					if math.Abs(float64(remove.X+remove.W/2-add.X-add.W/2)) > 1 {
						t.Error("directory icon buttons do not share one visual center column", remove, add)
					}
					if x := sidebarInkBounds(t, frame, remove, 1, background).Max.X; math.Abs(float64(x-headerRight)) > 1 {
						t.Errorf("remove directory ink ends at %d, want header's trailing edge %d", x, headerRight)
					}
				}
				if x := sidebarInkBounds(t, frame, add, 1, background).Max.X; math.Abs(float64(x-headerRight)) > 1 {
					t.Errorf("add directory ink ends at %d, want header's trailing edge %d", x, headerRight)
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("workspace-fullwidth-%d-dark-%v.png", width, dark))
				last := "移除目录：" + v.workspaceEditor.Workspace.Folders[1]
				remove, _ := ui.Find(last)
				ui.ClickAt(remove.X+remove.W-2, remove.Y+remove.H/2)
				if len(v.workspaceEditor.Workspace.Folders) != 1 {
					t.Error("the workspace tab clips the directory button's trailing hit area", remove)
				}
			})
		}
	}
}
