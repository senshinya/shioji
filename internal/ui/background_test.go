package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestBackgroundRegistrationIsExplicitAndDeviceScoped(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.WorkerService = &app.WorkerService{Phase: app.WorkerCurrent, Background: &domain.BackgroundService{Format: 1, State: "notRegistered"}}
	controller := app.NewController("")
	v := New(m, controller, "")
	v.devices = true
	v.deviceTab = "settings"
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	for len(controller.Jobs) > 0 {
		if j := <-controller.Jobs; j.Method == "$background.enable" {
			t.Fatal("registered without user action")
		}
	}
	if err := ui.Click("登录后启动服务开关"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(controller.Jobs) > 0 {
		j := <-controller.Jobs
		if j.Method == "$background.enable" {
			found = true
			if j.ConnectionID != "air" || j.ConnectionEpoch != "b" {
				t.Fatal("wrong service target", j)
			}
		}
	}
	if !found {
		t.Fatal("missing registration request")
	}
}
func TestBackgroundDetailsResizeContinuouslyWhenManagementStateChanges(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["local"]
	state.WorkerService = &app.WorkerService{Phase: app.WorkerCurrent, Background: &domain.BackgroundService{Format: 1, State: "notRegistered"}}
	v := New(m, app.NewController(""), "")
	v.devices = true
	v.deviceTab = "settings"
	v.deviceSelected = "local"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("服务详情")
	state.WorkerService.Background = &domain.BackgroundService{Format: 1, State: "enabled", Message: "保留原 worker，空闲后由系统服务接管"}
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	moving, _ := ui.Find("服务详情")
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("服务详情")
	if moving.Y <= before.Y+.5 || moving.Y >= end.Y-.5 {
		t.Fatal("background service detail height jumped", before, moving, end)
	}
}

func TestBackgroundSwitchCanDisableOnlyItsSelectedDevice(t *testing.T) {
	m := deviceUIModel()
	m.DeviceStates["air"].WorkerService = &app.WorkerService{Background: &domain.BackgroundService{Format: 1, State: "enabled"}}
	controller := app.NewController("")
	v := New(m, controller, "")
	v.devices = true
	v.deviceTab = "settings"
	v.deviceSelected = "air"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	if err := ui.Click("登录后启动服务开关"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(controller.Jobs) > 0 {
		job := <-controller.Jobs
		if job.Method == "$background.disable" {
			found = true
			if job.ConnectionID != "air" || job.ConnectionEpoch != "b" {
				t.Fatal("wrong service disabled", job)
			}
		}
	}
	if !found {
		t.Fatal("missing unregister request")
	}
}
