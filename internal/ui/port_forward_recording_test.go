package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/portforward"
	"shioji/internal/tailnet"
	"shioji/internal/transport"
	"testing"
	"time"
)

// Production View + MyGo native layout/Core Text/raster/input. Device/network
// snapshots are explicit fixtures; this recording makes no real device changes.
func TestPortForwardNativeRecording(t *testing.T) {
	dir := os.Getenv("SHIOJI_PORT_FORWARD_RECORDING")
	if dir == "" {
		t.Skip("opt-in native sidebar recording")
	}
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(dir, name)
			t.Setenv("SHIOJI_SKILL_SNAPSHOTS", out)
			m := deviceUIModel()
			m.SessionID = ""
			m.WorkspaceID = "wb"
			m.ActiveConnection = "air"
			m.Drafts["home"] = "检查设备连接与开发服务"
			m.RefreshDevices()
			m.Tailnet = tailnet.Snapshot{State: "Running", Hostname: "shioji-mac", Address: "100.80.0.2"}
			names := []string{"Mac mini", "MacBook Pro", "构建机", "开发 MacBook Air", "测试 Mac mini", "渲染 Mac Studio", "发布机", "办公室 Mac mini", "实验室 MacBook Pro", "备份 Mac mini", "随身 MacBook Air"}
			for i, label := range names {
				key := fmt.Sprint("peer-", i)
				m.Connections = append(m.Connections, app.DeviceConnection{ID: key, DeviceID: key, Name: label, SSH: transport.SSH{Target: "dev@mac", Worker: "/worker", TailnetPeer: key}})
				m.DeviceStates[key] = &app.DeviceState{Connected: i%3 != 2, Timing: &app.DeviceTiming{Milliseconds: float64(12 + i*7), ObservedAt: time.Now()}}
				m.Tailnet.Peers = append(m.Tailnet.Peers, tailnet.Peer{ID: key, Name: label, Address: fmt.Sprint("100.86.12.", 4+i), Online: i%3 != 2, Measurement: &tailnet.Measurement{Milliseconds: float64(12 + i*7), ObservedAt: time.Now()}})
			}
			m.PortForwards = []portforward.Mapping{{ID: "a", DeviceID: "B", Name: "MacBook Air", Target: portforward.Target{Host: "localhost", Port: 3000}, LocalPort: 3000, State: "available"}, {ID: "b", DeviceID: "peer-0", Name: "Mac mini", Target: portforward.Target{Host: "192.168.1.20", Port: 8080}, LocalPort: 8081, State: "available"}}
			v := New(m, app.NewController(""), "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			click := func(label string) {
				t.Helper()
				if err := ui.Click(label); err != nil {
					t.Fatal(err, ui.Texts())
				}
			}
			for frame := range 360 {
				switch frame {
				case 0:
					r, _ := ui.Find("设备")
					ui.Move(r.X+r.W/2, r.Y+r.H/2)
				case 30:
					click("设备")
				case 60:
					click("Tailnet")
				case 90:
					r, _ := ui.Find("Tailnet 对端列表")
					ui.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, 400)
				case 120:
					click("端口转发")
				case 150:
					click("创建端口转发")
				case 175:
					click("转发目标地址")
					ui.Key(n.Cmd, n.KeyA)
					ui.Type("192.168.1.20")
				case 195:
					click("取消")
				case 220:
					click("端口转发")
				case 230:
					click("Tailnet")
				case 250:
					m.Tailnet.State = "Stopped"
					ui.Frame()
				case 270:
					m.Tailnet.State = "Running"
					ui.Frame()
				case 290:
					m.Tailnet.Peers = m.Tailnet.Peers[:2]
					ui.Frame()
				case 310:
					click("设备")
				case 330:
					click("设备")
				case 340:
					click("Tailnet")
				}
				ui.Advance(time.Second / 30)
				saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
				for at, file := range map[int]string{49: "devices.png", 79: "tailnet.png", 109: "tailnet-scroll.png", 139: "ports.png", 189: "form.png", 265: "paused.png", 305: "compact.png"} {
					if frame == at {
						saveNativeSkillImage(t, ui, file)
					}
				}
			}
			narrow := n.NewTester(v.View, 920, 640)
			narrow.SetDark(dark)
			narrow.Advance(time.Second)
			v.quickUI().selected = ""
			narrow.Frame()
			narrow.Click("Tailnet")
			narrow.Advance(time.Second)
			saveNativeSkillImage(t, narrow, "narrow.png")
		})
	}
}
