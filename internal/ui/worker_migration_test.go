package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/transport"
	"testing"
	"time"
)

func legacySetupReply() app.DeviceSetupCheck {
	return app.DeviceSetupCheck{Info: transport.WorkerInfo{DeviceID: "legacy-device", Worker: "/remote/signed/shioji-worker", Directory: "/remote/worker", Legacy: &domain.LegacyWorkerInfo{DeviceID: "legacy-device", OwnerRef: "opaque-owner", Major: 1, Minor: 3, Supported: true}}}
}
func TestSSHLegacyMigrationRequiresConsentAndQueriesUncertainStart(t *testing.T) {
	v, ui := sshSetupUI(t)
	v.sshName = "MBA"
	v.Model.Drafts["home"] = "preserve ordinary draft"
	v.checkSSHDevice()
	job := welcomeJob(t, v.Controller, "$device.check")
	body, _ := json.Marshal(legacySetupReply())
	job.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(350 * time.Millisecond)
	if !ui.HasText("需要更新设备服务") || !ui.HasText("停止旧版并升级") {
		t.Fatal("missing explicit migration action", ui.Texts())
	}
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "$worker.migration" {
			t.Fatal("migration started before consent")
		}
	}
	ui.Click("停止旧版并升级")
	job = welcomeJob(t, v.Controller, "$worker.migration")
	in := job.Input.(app.WorkerMigrationAction)
	if in.Action != "start" || in.Target.Input.ID == "" || in.Target.Input.DeviceID != "legacy-device" || in.Target.SSH == nil || in.Target.SSH.Target != v.sshTarget {
		t.Fatal("migration lost owning-device context", in)
	}
	saved := v.Model.WorkerMigrations["legacy-device"]
	if saved.Input.ID != in.Target.Input.ID {
		t.Fatal("operation was not recorded before submission")
	}
	job.Apply(v.Model, nil, errors.New("connection lost"))
	ui.Frame()
	ui.Advance(1200 * time.Millisecond)
	query := welcomeJob(t, v.Controller, "$worker.migration").Input.(app.WorkerMigrationAction)
	if query.Action != "status" || query.Target.Input.ID != in.Target.Input.ID {
		t.Fatal("uncertain start was resubmitted or replaced", query)
	}
	if v.Model.Drafts["home"] != "preserve ordinary draft" {
		t.Fatal("migration consumed ordinary draft")
	}
}
func TestSSHLegacyBusyStateDoesNotOfferStop(t *testing.T) {
	v, ui := sshSetupUI(t)
	v.checkSSHDevice()
	job := welcomeJob(t, v.Controller, "$device.check")
	reply := legacySetupReply()
	reply.Info.Legacy.Busy = true
	reply.Info.Legacy.Message = "设备仍有任务或待处理请求，请空闲后更新"
	b, _ := json.Marshal(reply)
	job.Apply(v.Model, b, nil)
	ui.Frame()
	ui.Advance(350 * time.Millisecond)
	if !ui.HasText("等待任务结束") {
		t.Fatal("busy legacy state not shown", ui.Texts())
	}
	ui.Click("等待任务结束")
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "$worker.migration" {
			t.Fatal("busy worker received a stop action")
		}
	}
}

func TestLegacyDeviceMigrationIsReadOnlyUntilExplicitStop(t *testing.T) {
	m := deviceUIModel()
	state := m.DeviceStates["air"]
	state.Connected = false
	state.WorkerService = &app.WorkerService{Phase: app.WorkerLegacy, Legacy: &domain.LegacyWorkerInfo{DeviceID: m.ConnectionDeviceID("air"), Major: 1, Minor: 3}}
	v := New(m, app.NewController(""), "")
	v.devices, v.deviceSelected, v.deviceTab = true, "air", "settings"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	if err := ui.Click("准备更新执行服务"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job := welcomeJob(t, v.Controller, "$worker.migration.inspect")
	target := job.Input.(app.WorkerMigrationAction).Target
	if target.ConnectionID != "air" || target.SSH == nil {
		t.Fatal("wrong target", target)
	}
	reply := legacySetupReply().Info
	reply.DeviceID = m.ConnectionDeviceID("air")
	reply.Legacy.DeviceID = reply.DeviceID
	b, _ := json.Marshal(reply)
	job.Apply(m, b, nil)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if err := ui.Click("停止旧版并升级"); err != nil {
		t.Fatal(err, ui.Texts())
	}
	job = welcomeJob(t, v.Controller, "$worker.migration")
	if in := job.Input.(app.WorkerMigrationAction); in.Action != "start" || in.Target.ConnectionID != "air" || in.Target.Input.OwnerRef != "opaque-owner" {
		t.Fatal(in)
	}
}

func TestLegacyMigrationRestoresQueryWithoutStealingNavigation(t *testing.T) {
	m := deviceUIModel()
	m.FirstRun = false
	m.Drafts["home"] = "my draft"
	m.WorkerMigrations = map[string]app.WorkerMigrationTarget{"legacy-device": {Setup: true, SSH: &transport.SSH{Target: "user@example.test"}, Info: legacySetupReply().Info, Input: domain.WorkerMigrationInput{ID: "accepted-id", DeviceID: "legacy-device", OwnerRef: "opaque-owner"}, Request: app.MigrationSubmitting}}
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	in := welcomeJob(t, v.Controller, "$worker.migration").Input.(app.WorkerMigrationAction)
	if in.Action != "status" || in.Target.Input.ID != "accepted-id" {
		t.Fatal(in)
	}
	if v.welcome != nil && v.welcome.step == welcomeSSH || m.Drafts["home"] != "my draft" {
		t.Fatal("recovery stole view or draft")
	}
}

func TestLegacyDeviceRecheckReconnectsIfAnotherClientAlreadyUpgraded(t *testing.T) {
	m := deviceUIModel()
	v := New(m, app.NewController(""), "")
	target := app.WorkerMigrationTarget{ConnectionID: "air", Info: transport.WorkerInfo{DeviceID: m.ConnectionDeviceID("air")}}
	flow := &workerMigrationUI{target: target, phase: migrationViewReady}
	v.migrationFlows()[target.Info.DeviceID] = flow
	v.inspectMigration(flow)
	job := welcomeJob(t, v.Controller, "$worker.migration.inspect")
	b, _ := json.Marshal(transport.WorkerInfo{DeviceID: target.Info.DeviceID, Build: &domain.WorkerBuild{BuildID: "current"}})
	job.Apply(m, b, nil)
	job = welcomeJob(t, v.Controller, "$reconnect")
	if job.ConnectionID != "air" {
		t.Fatal(job)
	}
	if v.migrationForConnection("air") != nil {
		t.Fatal("healthy worker still masked by legacy migration flow")
	}
}

func TestDisconnectedDeviceCanDiscoverMigrationFromAnotherClient(t *testing.T) {
	m := deviceUIModel()
	m.DeviceStates["air"].Connected = false
	m.DeviceStates["air"].WorkerService = nil
	v := New(m, app.NewController(""), "")
	v.devices = true
	v.deviceSelected = "air"
	v.deviceTab = "settings"
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	if e := ui.Click("检查设备服务"); e != nil {
		t.Fatal(e, ui.Texts())
	}
	job := welcomeJob(t, v.Controller, "$worker.migration.inspect")
	info := legacySetupReply().Info
	info.Legacy = nil
	info.DeviceID = m.ConnectionDeviceID("air")
	info.Migration = &domain.WorkerMigrationStatus{ID: "other-client-operation", DeviceID: info.DeviceID, OwnerRef: "owner", Phase: domain.MigrationFailed, CanResume: true}
	b, _ := json.Marshal(info)
	job.Apply(m, b, nil)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if !ui.HasText("继续准备") || m.WorkerMigrations[info.DeviceID].Input.ID != info.Migration.ID {
		t.Fatal("foreign client's operation not adopted", ui.Texts())
	}
}

func TestLostResumeReplyQueriesSameOperation(t *testing.T) {
	v, ui := sshSetupUI(t)
	reply := legacySetupReply().Info
	reply.Legacy = nil
	reply.Migration = &domain.WorkerMigrationStatus{ID: "resume-id", DeviceID: reply.DeviceID, OwnerRef: "owner", Phase: domain.MigrationFailed, CanResume: true}
	v.sshSetup.Info = reply
	v.acceptSSHMigration(reply)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if e := ui.Click("继续准备"); e != nil {
		t.Fatal(e, ui.Texts())
	}
	job := welcomeJob(t, v.Controller, "$worker.migration")
	in := job.Input.(app.WorkerMigrationAction)
	if in.Action != "resume" {
		t.Fatal(in)
	}
	job.Apply(v.Model, nil, errors.New("reply lost"))
	ui.Frame()
	ui.Advance(1500 * time.Millisecond)
	in = welcomeJob(t, v.Controller, "$worker.migration").Input.(app.WorkerMigrationAction)
	if in.Action != "status" || in.Target.Input.ID != "resume-id" {
		t.Fatal("resume was repeated instead of observed", in)
	}
}

func TestMigrationPollingKeepsCurrentProgressCaption(t *testing.T) {
	flow := &workerMigrationUI{phase: migrationViewFollowing, pending: true, target: app.WorkerMigrationTarget{Status: &domain.WorkerMigrationStatus{Phase: domain.MigrationBackingUp}}}
	label, disabled := migrationAction(flow)
	if label != "正在备份会话" || !disabled {
		t.Fatal("polling replaced real phase with transient check", label, disabled)
	}
	flow.target.Status = nil
	if migrationCaption(flow) != "正在准备设备服务" {
		t.Fatal("accepted start still looks idle")
	}
}
