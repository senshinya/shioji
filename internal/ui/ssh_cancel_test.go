package ui

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
)

func TestSSHCancelAllowsFreshAttemptBeforeOldReceipt(t *testing.T) {
	for _, action := range []string{"取消", "取消添加设备", "escape"} {
		t.Run(action, func(t *testing.T) {
			v := New(layoutModel(), app.NewController(""), "")
			v.sshCheck, v.sshTarget = sshEditing, "user@example.test"
			ui := n.NewTester(v.View, 1100, 800)
			ui.Advance(time.Second)
			v.checkSSHDevice()
			old := welcomeJob(t, v.Controller, "$device.check")
			ui.Frame()
			ui.Advance(time.Second)
			if action == "escape" {
				ui.Key(0, n.KeyEscape)
			} else if err := ui.Click(action); err != nil {
				t.Fatal(err)
			}
			welcomeJob(t, v.Controller, "$device.cancel")
			v.sshCheck = v.sshCheck.show()
			ui.Frame()
			ui.Advance(time.Second)
			if v.sshCheck.busy() || ui.HasText("正在检查设备服务") {
				t.Fatal("reopening retained the canceled connection attempt", v.sshCheck)
			}
			if v.sshTarget != "" {
				t.Fatal("reopening restored the canceled address", v.sshTarget)
			}
			v.sshTarget = "fresh@example.test"
			ui.Frame()
			if err := ui.Click("连接并准备服务"); err != nil {
				t.Fatal(err)
			}
			fresh := welcomeJob(t, v.Controller, "$device.check")
			old.Apply(v.Model, nil, context.Canceled)
			if v.sshCheck != sshChecking || v.sshError != "" {
				t.Fatal("old receipt changed the new attempt", v.sshCheck, v.sshError)
			}
			fresh.Apply(v.Model, nil, context.DeadlineExceeded)
			ui.Frame()
			ui.Advance(time.Second)
			if v.sshCheck.busy() || !ui.HasText("连接超时（10 秒），请检查地址后重试") {
				t.Fatal("timeout did not restore an editable retry", ui.Texts())
			}
		})
	}
}

func TestCanceledDeviceAdmissionReceiptCannotFinishFreshCheck(t *testing.T) {
	v, _ := sshSetupUI(t)
	v.sshAgents = agentEditor(setupCatalog())
	v.sshCheck = sshSelecting
	v.completeSSHDevice()
	old := welcomeJob(t, v.Controller, "$device.complete")
	v.cancelSSHCheck()
	if v.sshAgents.busy {
		t.Fatal("cancel retained the old admission's busy state")
	}
	v.sshCheck = v.sshCheck.show()
	v.checkSSHDevice()
	old.Apply(v.Model, nil, context.Canceled)
	if v.sshCheck != sshChecking {
		t.Fatal("canceled admission receipt finished a new connection check", v.sshCheck)
	}
}

func TestCancelAddingDeviceDiscardsSSHAndTailnetDrafts(t *testing.T) {
	for _, route := range []string{"ssh", "tailnet"} {
		for _, action := range []string{"取消", "取消添加设备", "escape", "backdrop", "quick-cancel"} {
			t.Run(route+"/"+action, func(t *testing.T) {
				m := welcomeNetworkModel()
				m.FirstRun = false
				m.Drafts["home"] = "保留会话草稿"
				c := app.NewController("")
				v := New(m, c, "")
				v.devices = true
				ui := n.NewTester(v.View, 1100, 800)
				ui.SetDark(false)
				recording, frame := false, 0
				evidence := os.Getenv("SHIOJI_CANCEL_DRAFTS_EVIDENCE")
				advance := func(d time.Duration) {
					if evidence == "" || action != "取消" || !recording {
						ui.Advance(d)
						return
					}
					dir := filepath.Join(evidence, route)
					if e := os.MkdirAll(dir, 0755); e != nil {
						t.Fatal(e)
					}
					for elapsed := time.Duration(0); elapsed < d; elapsed += time.Second / 60 {
						f, e := os.Create(filepath.Join(dir, fmt.Sprintf("frame-%04d.png", frame)))
						if e != nil {
							t.Fatal(e)
						}
						e = png.Encode(f, ui.Image())
						closeErr := f.Close()
						if e != nil {
							t.Fatal(e)
						}
						if closeErr != nil {
							t.Fatal(closeErr)
						}
						frame++
						ui.Advance(time.Second / 60)
					}
				}
				open := func() {
					t.Helper()
					if route == "ssh" {
						v.welcome = nil
						v.sshCheck = v.sshCheck.show()
						ui.Frame()
						advance(time.Second)
					} else {
						v.beginTailnet()
						ui.Frame()
						advance(time.Second)
						if e := ui.Click("选择 Tailnet 设备：MacBook Air"); e != nil {
							t.Fatal(e)
						}
						if e := ui.Click("连接设备"); e != nil {
							t.Fatal(e)
						}
						advance(time.Second)
					}
				}
				open()
				if e := ui.Click("连接选项"); e != nil {
					t.Fatal(e)
				}
				advance(time.Second)
				input, ok := ui.Find("远程设备名称")
				if !ok {
					t.Fatal("missing connection input")
				}
				for _, field := range []struct{ name, text string }{{"SSH 地址", "discard-user@100.64.0.99"}, {"远程设备名称", "取消后不应保留的名称"}, {"SSH 端口", "8022"}} {
					row, ok := ui.Find(field.name)
					if !ok {
						t.Fatal("missing field", field.name)
					}
					ui.ClickAt(input.X+input.W/2, row.Y+row.H/2)
					ui.Key(n.Cmd, n.KeyA)
					ui.Type(field.text)
				}
				if v.sshTarget != "discard-user@100.64.0.99" || v.sshName != "取消后不应保留的名称" || v.sshPort != "8022" {
					t.Fatal("fixture did not edit connection fields", v.sshTarget, v.sshName, v.sshPort)
				}
				recording = true
				advance(time.Second)
				v.saveWelcomeSSHInput()
				v.sshExpectedDevice = "abandoned-device"
				if action == "backdrop" {
					ui.ClickAt(10, 10)
				} else if action == "quick-cancel" {
					if e := ui.Click("取消"); e != nil {
						t.Fatal(e)
					}
				} else if action == "escape" {
					ui.Key(0, n.KeyEscape)
				} else if e := ui.Click(action); e != nil {
					t.Fatal(e)
				}
				ui.Frame()
				if action != "quick-cancel" {
					advance(time.Second)
				}
				if v.sshCheck.open() {
					t.Fatal("cancel kept the add-device flow open", v.sshCheck)
				}
				if v.sshTarget != "" || v.sshName != "" || v.sshPort != "" || v.sshTailnetPeer != "" || v.sshExpectedDevice != "" {
					t.Error("cancel retained connection fields", v.sshTarget, v.sshName, v.sshPort, v.sshTailnetPeer)
				}
				open()
				wantTarget, wantName := "", ""
				if route == "tailnet" {
					wantTarget, wantName = "100.64.0.1", "MacBook Air"
				}
				if v.sshTarget != wantTarget || v.sshName != wantName || v.sshPort != "" || v.setupFlow().sshOptions {
					t.Fatal("new add-device flow restored abandoned draft", v.sshTarget, v.sshName, v.sshPort)
				}
				if e := ui.Click("连接选项"); e != nil {
					t.Fatal(e)
				}
				advance(2 * time.Second)
				if ui.HasText("discard-user@100.64.0.99") || ui.HasText("取消后不应保留的名称") || ui.HasText("8022") {
					t.Fatal("native fields restored discarded text", ui.Texts())
				}
				if m.Tailnet.State != "Running" || m.Drafts["home"] != "保留会话草稿" {
					t.Fatal("discarding setup changed the network or ordinary draft")
				}
				for len(c.Jobs) > 0 {
					if j := <-c.Jobs; j.Method == "$tailnet" {
						t.Fatal("cancel sent an unrelated network action", j.Input)
					}
				}
			})
		}
	}
}
