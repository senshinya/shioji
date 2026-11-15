package ui

import (
	"encoding/json"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/portforward"
	"testing"
	"time"
)

func TestManualForwardFormPreservesExplicitLocalPortAndUsesBoundDevice(t *testing.T) {
	m := deviceUIModel()
	m.WorkspaceID = "wb"
	m.ActiveConnection = "air"
	m.SessionID = ""
	m.RefreshDevices()
	controller := app.NewController("")
	v := New(m, controller, "")
	v.beginPortForward()
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	d := v.portForward
	d.host = "localhost"
	d.remote = "8080"
	d.local = "9080"
	ui.Frame()
	if err := ui.Click("创建"); err != nil {
		t.Fatal(err)
	}
	job := welcomeJob(t, controller, "$forward.create")
	in := job.Input.(app.ForwardInput)
	if job.ConnectionID != "air" || in.Request.DeviceID != "B" || in.Request.Target.Port != 8080 || in.Request.LocalPort != 9080 || in.Request.AutoPort || in.OpenURL != nil {
		t.Fatal(job.ConnectionID, in)
	}
	job.Apply(m, nil, fmt.Errorf("port busy"))
	ui.Frame()
	if v.portForward != d || d.local != "9080" || !ui.HasText("port busy") {
		t.Fatal("failure discarded draft")
	}
}
func TestManualForwardLateReceiptDoesNotReopenDismissedDraft(t *testing.T) {
	m := deviceUIModel()
	controller := app.NewController("")
	v := New(m, controller, "")
	v.beginPortForward()
	d := v.portForward
	d.connection = "air"
	d.host = "localhost"
	d.remote = "3000"
	d.local = "3000"
	v.submitPortForward(d)
	job := welcomeJob(t, controller, "$forward.create")
	v.cancelPortForward(d)
	encoded, _ := json.Marshal(portforward.Mapping{ID: "new", DeviceID: "B", LocalPort: 3000, State: "available"})
	job.Apply(m, encoded, nil)
	if v.portForward != nil || v.quickUI().selected != "" {
		t.Fatal("late receipt changed navigation")
	}
}

func TestFaultedForwardRetainsRetryAndCloseActions(t *testing.T) {
	m := deviceUIModel()
	m.PortForwards = []portforward.Mapping{{ID: "mapping", DeviceID: "B", Name: "MacBook Air", Target: portforward.Target{Host: "localhost", Port: 8080}, LocalPort: 8080, State: "unavailable", Error: "connection refused"}}
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	ui.Click("端口转发")
	ui.Advance(time.Second)
	if err := ui.RightClick("打开转发：127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	if err := ui.ChooseMenuItem("重试"); err != nil {
		t.Fatal(err, ui.Menu())
	}
	job := welcomeJob(t, c, "$forward.retry")
	if job.Input.(app.ForwardInput).ID != "mapping" {
		t.Fatal(job.Input)
	}
	if err := ui.Click("关闭转发：MacBook Air · 8080"); err != nil {
		t.Fatal(err)
	}
	job = welcomeJob(t, c, "$forward.close")
	if job.Input.(app.ForwardInput).ID != "mapping" {
		t.Fatal(job.Input)
	}
	if m.SessionID != "s" && m.SessionID != "" {
		t.Fatal("closing mapping changed session", m.SessionID)
	}
}

func TestManualForwardCancelAddressesAcceptedMapping(t *testing.T) {
	v := New(deviceUIModel(), app.NewController(""), "")
	v.beginPortForward()
	d := v.portForward
	d.connection = "air"
	v.submitPortForward(d)
	create := welcomeJob(t, v.Controller, "$forward.create")
	id := create.Input.(app.ForwardInput).Request.ID
	if id == "" {
		t.Fatal("pending manual forward has no stable cancellation identity")
	}
	v.cancelPortForward(d)
	closeJob := welcomeJob(t, v.Controller, "$forward.close")
	if closeJob.Input.(app.ForwardInput).ID != id {
		t.Fatal("cancel addressed another mapping")
	}
}

func TestManualForwardInputsKeepNativeEditingAndPortFollowing(t *testing.T) {
	v := New(deviceUIModel(), app.NewController(""), "")
	v.beginPortForward()
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	edit := func(label, value string) {
		t.Helper()
		if err := ui.Click(label); err != nil {
			t.Fatal(err)
		}
		ui.Key(n.Cmd, n.KeyA)
		ui.Type(value)
	}
	edit("转发目标端口", "5173")
	if v.portForward.remote != "5173" || v.portForward.local != "5173" {
		t.Fatal("local port did not follow", v.portForward)
	}
	edit("转发本机端口", "55173")
	edit("转发目标端口", "8080")
	if v.portForward.local != "55173" {
		t.Fatal("explicit local port overwritten", v.portForward)
	}
	edit("转发目标地址", "192.168.1.20")
	if v.portForward.host != "192.168.1.20" {
		t.Fatal("native target field lost editing", v.portForward.host)
	}
}
