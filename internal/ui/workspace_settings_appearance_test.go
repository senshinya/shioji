package ui

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestWorkspaceDirectorySelectionSharesNativeTextInkCenter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, folder := range []string{"HHH", "国国国", "国 H 国"} {
				t.Run(fmt.Sprintf("dark-%v-scale-%g-%s", dark, scale, folder), func(t *testing.T) {
					m := deviceUIModel()
					v := New(m, nil, "")
					v.editWorkspace(m.Snapshot.Workspaces[0])
					v.workspaceEditor.Workspace.Folders = []string{folder}
					v.workspaceEditor.Main = folder
					ui := n.NewTester(v.View, 1240, 820)
					ui.SetDark(dark)
					ui.SetScale(scale)
					ui.Advance(time.Second)
					radio, _ := ui.Find("设为主目录：" + folder)
					text, _ := ui.Find(folder)
					a, b := nativeQuickInkY(t, ui, radio, scale)
					c, d := nativeQuickInkY(t, ui, text, scale)
					if delta := math.Abs(float64(a+b-c-d) / float64(2*scale)); delta > 1 {
						t.Fatalf("directory selection drifts from native text ink by %.2f DIP", delta)
					}
				})
			}
		}
	}
}

func TestWorkspaceHeadingDeviceSharesNativeTextInkCenter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			for _, title := range []string{"HHH", "国国国", "国 H 国"} {
				for _, device := range []string{"Home Mac", "这台 Mac"} {
					t.Run(fmt.Sprintf("dark-%v-scale-%g-%s-%s", dark, scale, title, device), func(t *testing.T) {
						m := deviceUIModel()
						m.LocalDeviceName = device
						v := New(m, nil, "")
						v.editWorkspace(m.Snapshot.Workspaces[0])
						v.workspaceEditor.Workspace.Name = title
						ui := n.NewTester(func(c *n.Context) { v.workspaceSettingsHeader(c, c.Theme()) }, 1240, 140)
						ui.SetDark(dark)
						ui.SetScale(scale)
						ui.Advance(time.Second)
						heading, _ := ui.Find(title)
						heading.X += 32 // Existing 24-DIP icon slot and 8-DIP gap.
						heading.W = 120
						caption, _ := ui.Find(device)
						a, b := nativeQuickInkY(t, ui, heading, scale)
						c, d := nativeQuickInkY(t, ui, caption, scale)
						if delta := math.Abs(float64(a+b-c-d) / float64(2*scale)); delta > 1 {
							t.Fatalf("device caption drifts from workspace title ink by %.2f DIP", delta)
						}
					})
				}
			}
		}
	}
}

// The production View uses MyGo's native text, raster and input implementation.
// Device state and the failed save response are fixtures; no live worker is used.
func TestWorkspaceSettingsNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_WORKSPACE_SETTINGS_RECORDING")
	if dir == "" {
		t.Skip("opt-in native workspace settings recording")
	}
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, name))
			m := deviceUIModel()
			m.LocalDeviceName = "MacMini at Home"
			m.DeviceStates["local"].WorkerMinor = 5
			v := New(m, app.NewController(""), "")
			v.editWorkspace(m.Snapshot.Workspaces[0])
			v.workspaceEditor.Workspace.Name = "shioji"
			root := "/Users/shinya/Downloads/tahuna"
			v.workspaceEditor.Workspace.Folders = []string{root, root + "/design", root + "/docs"}
			v.workspaceEditor.Main = root
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			click := func(label string) {
				t.Helper()
				if err := ui.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			for frame := range 270 {
				switch frame {
				case 30:
					click("工作区名称")
					ui.Key(n.Cmd, n.KeyA)
					ui.Type("潮路 shioji")
				case 60:
					click("设为主目录：" + root + "/design")
				case 90:
					click("移除目录：" + root)
				case 120:
					click("Skills")
				case 132:
					click("MCP")
				case 144:
					click("常规")
				case 180:
					click("移除目录：" + root + "/docs")
				case 210:
					click("保存工作区")
				case 225:
					welcomeJob(t, v.Controller, "workspace.update").Apply(m, nil, errors.New("设备连接已断开"))
					ui.Frame()
				}
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			}
			if v.workspaceEditor == nil || v.workspaceEditor.Workspace.Name != "潮路 shioji" || v.workspaceEditor.Main != root+"/design" || !ui.HasText("设备连接已断开") {
				t.Fatal("workspace draft or visible save failure was lost")
			}
		})
	}
}
