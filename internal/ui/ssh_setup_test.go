package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/plugins/terminal"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/transport"
	"strings"
	"testing"
	"time"
)

func sshSetupUI(t *testing.T) (*View, *n.Tester) {
	t.Helper()
	m := welcomeModel()
	m.FirstRun = true
	v := New(m, app.NewController(""), "")
	v.sshCheck = sshEditing
	v.sshTarget = "user@example.test"
	f := v.setupFlow()
	f.key = ""
	f.step = welcomeSSH
	ui := n.NewTester(v.View, 1100, 800)
	ui.Advance(time.Second)
	return v, ui
}
func TestSSHHostTrustRequiresExplicitActionBeforePreparingService(t *testing.T) {
	v, ui := sshSetupUI(t)
	v.checkSSHDevice()
	job := welcomeJob(t, v.Controller, "$device.check")
	key := &transport.SSHHostKey{Target: v.sshTarget, Name: "example.test", Fingerprints: "256 SHA256:reviewed example.test (ED25519)", File: "/tmp/known_hosts"}
	body, _ := json.Marshal(app.DeviceSetupCheck{HostKey: key})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(350 * time.Millisecond)
	if !ui.HasText("信任并继续") || !ui.HasText(key.Fingerprints) {
		t.Fatal("missing native fingerprint confirmation", ui.Texts())
	}
	if len(v.Controller.Jobs) != 0 || v.sshTerminal != nil {
		t.Fatal("trusting or authentication started without user action")
	}
	if err := ui.Click("信任并继续"); err != nil {
		t.Fatal(err)
	}
	job = welcomeJob(t, v.Controller, "$device.check")
	in := job.Input.(app.CheckSSHDevice)
	if in.Trust == nil || in.Trust.Fingerprints != key.Fingerprints || in.SSH.Target != key.Target {
		t.Fatal("trust lost the reviewed target/key", in)
	}
}
func TestSSHAuthenticationFailureOffersTerminalWithoutStartingIt(t *testing.T) {
	v, ui := sshSetupUI(t)
	v.checkSSHDevice()
	job := welcomeJob(t, v.Controller, "$device.check")
	body, _ := json.Marshal(app.DeviceSetupCheck{NeedsKey: true})
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(350 * time.Millisecond)
	if !ui.HasText("设置 SSH 密钥") || v.sshTerminal != nil {
		t.Fatal("key setup missing or started without consent", ui.Texts())
	}
	if strings.Contains(strings.Join(ui.Texts(), " "), "免交互登录") {
		t.Fatal("still sends the user out to a terminal")
	}
	v.cancelSSHCheck()
	if v.sshHostKey != nil || v.sshCheck.open() {
		t.Fatal("cancel left a pending host trust")
	}
}
func TestSSHSetupTerminalUsesRealPTYWithoutEchoingSecret(t *testing.T) {
	term := newSSHSetupTerminal([]string{"/bin/sh", "-c", "stty -echo; printf 'Enter secret: '; IFS= read -r value; stty echo; printf '\\r\\nAccepted %s bytes\\r\\n' \"${#value}\""}, "monospace", func() {})
	defer term.close()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		active, _, _, err := term.snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if active != nil && strings.Contains(active.Text(), "Enter secret:") {
			active.Send([]byte("fixture-secret\r"))
			for time.Now().Before(deadline) {
				active, phase, code, err := term.snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if phase == sshTerminalFinished {
					if code != 0 || strings.Contains(active.Text(), "fixture-secret") || !strings.Contains(active.Text(), "Accepted 14 bytes") {
						t.Fatal("PTY input/echo contract failed", code, active.Text())
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("interactive terminal did not complete")
}

func TestSSHTrustDisclosureMovesFooterContinuously(t *testing.T) {
	v, ui := sshSetupUI(t)
	before, _ := ui.Find("连接并准备服务")
	v.sshHostKey = &transport.SSHHostKey{Target: v.sshTarget, Fingerprints: "256 SHA256:reviewed example.test (ED25519)"}
	v.sshCheck = sshAwaitingTrust
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("信任并继续")
	ui.Advance(350 * time.Millisecond)
	settled, _ := ui.Find("信任并继续")
	if middle.Y <= before.Y || middle.Y >= settled.Y {
		t.Fatal("host-key review jumps following actions", before, middle, settled)
	}
}
func TestClosingSSHSetupTerminalCancelsOnlyItsOwnPTY(t *testing.T) {
	s := newSSHSetupTerminal([]string{"/bin/sh", "-c", "printf ready; IFS= read -r value"}, "monospace", func() {})
	defer s.close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		active, _, _, err := s.snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if active != nil && strings.Contains(active.Text(), "ready") {
			s.close()
			select {
			case <-active.Done():
				return
			case <-time.After(4 * time.Second):
				t.Fatal("setup PTY survived cancellation")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("PTY did not start")
}

func TestSSHSetupTerminalKeepsMonospacedCellsWithAnUnavailablePreferredFont(t *testing.T) {
	s := newSSHSetupTerminal([]string{"/bin/sh", "-c", "printf ready; IFS= read -r value"}, "Shioji Missing Font Fixture", func() {})
	defer s.close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		active, _, _, err := s.snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if active != nil {
			ui := n.NewTester(func(c *n.Context) { terminal.View(c, active).Size(480, 180) }, 500, 200)
			ui.Advance(100 * time.Millisecond)
			cols, _ := active.Size()
			if cols < 50 {
				t.Fatal("unavailable preferred font fell back to proportional, oversized cells", cols)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("terminal did not initialize")
}

func TestSSHSetupTerminalKeepsActionsVisibleAtMinimumWindowSize(t *testing.T) {
	v, _ := sshSetupUI(t)
	v.sshTerminal = newSSHSetupTerminal([]string{"/bin/sh", "-c", "IFS= read -r value"}, "monospace", func() {})
	defer v.CloseTransient()
	v.sshCheck = sshConfiguring
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(350 * time.Millisecond)
	button, ok := ui.Find("正在设置密钥")
	if !ok || button.H < 31 || button.Y+button.H > 600 {
		t.Fatal("terminal pushes actions outside the minimum window", ok, button)
	}
}

func TestSSHSetupTerminalCaptionChangesProgressively(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, _ := sshSetupUI(t)
			v.sshTerminal = &sshSetupTerminal{phase: sshTerminalLoading}
			ui := n.NewTester(func(c *n.Context) { v.sshSetupContent(c, c.Theme()) }, 600, 400)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			before := append([]byte(nil), ui.Image().Pix...)
			v.sshTerminal.phase = sshTerminalRunning
			ui.Frame()
			ui.Advance(50 * time.Millisecond)
			middle := append([]byte(nil), ui.Image().Pix...)
			ui.Advance(200 * time.Millisecond)
			after := ui.Image().Pix
			if bytes.Equal(before, after) || bytes.Equal(middle, before) || bytes.Equal(middle, after) {
				t.Fatal("terminal preparation caption switches without an intermediate frame")
			}
		})
	}
}

func TestSSHTrustConfirmationOpensKeySetupWhenAuthenticationIsRequired(t *testing.T) {
	v, ui := sshSetupUI(t)
	defer v.CloseTransient()
	v.sshHostKey = &transport.SSHHostKey{Target: v.sshTarget, Fingerprints: "reviewed"}
	v.sshCheck = sshAwaitingTrust
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("信任并继续"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, v.Controller, "$device.check")
	body, _ := json.Marshal(app.DeviceSetupCheck{NeedsKey: true})
	job.Apply(v.Model, body, nil)
	if v.sshTerminal == nil || v.sshCheck != sshConfiguring {
		t.Fatal("confirmed trust still requires a second key-setup click")
	}
}

func TestSSHKeySetupRecheckDoesNotAutomaticallyRelaunchTerminal(t *testing.T) {
	v, _ := sshSetupUI(t)
	defer v.CloseTransient()
	finished := &sshSetupTerminal{phase: sshTerminalFinished}
	v.sshTerminal, v.sshCheck = finished, sshConfiguring
	v.syncSSHSetupTerminal()
	job := welcomeJob(t, v.Controller, "$device.check")
	body, _ := json.Marshal(app.DeviceSetupCheck{NeedsKey: true})
	job.Apply(v.Model, body, nil)
	if v.sshTerminal != finished || v.sshCheck != sshNeedsKey {
		t.Fatal("authentication recheck restarted key installation without a new action")
	}
}

func TestSSHConnectionOptionsCollapseAfterAcceptedConnectWithHeightAnimation(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := sshSetupUI(t)
			ui.SetDark(dark)
			v.sshName = "MacBook Air"
			v.sshPort = "2222"
			ui.Click("连接选项")
			ui.Advance(time.Second)
			before, _ := ui.Find("连接并准备服务")
			ui.Click("连接并准备服务")
			job := welcomeJob(t, v.Controller, "$device.check")
			in := job.Input.(app.CheckSSHDevice)
			if in.Name != v.sshName || in.SSH.Port != 2222 {
				t.Fatal("collapse changed submitted options", in)
			}
			ui.Advance(60 * time.Millisecond)
			middle, _ := ui.Find("正在连接")
			ui.Advance(350 * time.Millisecond)
			after, _ := ui.Find("正在连接")
			if !(after.Y < middle.Y && middle.Y < before.Y) {
				t.Fatal("connection options did not collapse continuously", before, middle, after)
			}
			if ui.HasText("远程设备名称") {
				t.Fatal("connection options remained expanded")
			}
			job.Apply(v.Model, nil, fmt.Errorf("offline"))
			ui.Frame()
			ui.Advance(350 * time.Millisecond)
			ui.Click("连接选项")
			ui.Advance(350 * time.Millisecond)
			if !ui.HasText("远程设备名称") || v.sshName != "MacBook Air" || v.sshPort != "2222" {
				t.Fatal("cannot reopen options after a connection failure")
			}
		})
	}
}

func TestSSHConnectionOptionsStayOpenWhenPortValidationFails(t *testing.T) {
	v, ui := sshSetupUI(t)
	v.sshPort = "not-a-port"
	ui.Click("连接选项")
	ui.Advance(time.Second)
	ui.Click("连接并准备服务")
	ui.Advance(350 * time.Millisecond)
	if !ui.HasText("远程设备名称") || !ui.HasText("请输入有效的 SSH 端口") {
		t.Fatal("validation hid the fields needed for correction", ui.Texts())
	}
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; job.Method == "$device.check" {
			t.Fatal("invalid port submitted a connection job")
		}
	}
}
