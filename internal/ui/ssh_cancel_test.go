package ui

import (
	"context"
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
