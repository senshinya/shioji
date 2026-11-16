package app

import (
	"context"
	"encoding/json"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"strings"
	"testing"
)

func TestWaitingUpgradeNoticeUsesCurrentDeviceActions(t *testing.T) {
	m := NewModel()
	m.Preferences = m.Preferences.Normalized()
	m.Preferences.Notifications.Enabled, m.Preferences.Notifications.Attention = true, true
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.Snapshot = domain.Snapshot{DeviceID: "device"}
		p.WorkerService = &WorkerService{Phase: WorkerWaiting, OperationID: "upgrade"}
	})
	if len(m.Notifications) != 1 || !strings.Contains(m.Notifications[0].Body, "取消更新") || strings.Contains(m.Notifications[0].Body, "强制") {
		t.Fatal("waiting notice still directs users to the removed force workflow", m.Notifications)
	}
}

func TestWorkerUpgradeLostReceiptRestartQueriesSameOperation(t *testing.T) {
	dir := t.TempDir()
	var accepted domain.WorkerReplacement
	peer := func(query bool) *protocol.Client {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			codec, epoch, err := protocol.Welcome(b, "device", domain.WorkerBuild{BuildID: "old", Lifecycle: 1, SafeUpdate: true})
			if err != nil {
				return
			}
			var f protocol.Frame
			if codec.Read(&f) != nil {
				return
			}
			if !query {
				if f.Method != "worker.update" {
					t.Error("first dispatch", f.Method)
				}
				_ = json.Unmarshal(f.Body, &accepted)
				return
			}
			if f.Method != "worker.update.status" {
				t.Error("unknown update was resent after restart", f.Method)
			}
			var in struct{ ID string }
			_ = json.Unmarshal(f.Body, &in)
			if in.ID != accepted.ID {
				t.Error("query changed operation ID", in.ID, accepted.ID)
			}
			raw, _ := json.Marshal(supervisor.Operation{Input: accepted, DeviceID: "device", Phase: "waiting"})
			_ = codec.Write(protocol.Frame{Type: "result", Epoch: epoch, DeviceID: "device", ID: f.ID, Body: raw})
		}()
		c, err := protocol.Connect(context.Background(), a, "device")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	candidate := &domain.WorkerBuild{BuildID: "next", Lifecycle: 1, SafeUpdate: true, BuiltAt: 200}
	first := workerUpgradeClient{directory: dir, device: "device"}
	if _, err := first.update(context.Background(), peer(false), candidate, true, "when-idle"); err == nil {
		t.Fatal("lost acceptance was reported as success")
	}
	if accepted.ID == "" {
		t.Fatal("operation ID was not saved before dispatch")
	}
	restarted := workerUpgradeClient{directory: dir, device: "device"}
	service, err := restarted.update(context.Background(), peer(true), candidate, false, "when-idle")
	if err != nil || service.Phase != WorkerWaiting || service.OperationID != accepted.ID {
		t.Fatal("restart did not observe original waiting operation", service, err)
	}
}

func TestWorkerUpgradeDefiniteRejectionIsNotKeptAsUnknown(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		defer b.Close()
		codec, epoch, e := protocol.Welcome(b, "device", domain.WorkerBuild{BuildID: "old", Lifecycle: 1, SafeUpdate: true})
		if e != nil {
			return
		}
		var f protocol.Frame
		if codec.Read(&f) != nil {
			return
		}
		_ = codec.Write(protocol.Frame{Type: "result", ID: f.ID, Epoch: epoch, DeviceID: "device", ErrorCode: "notSent", Error: "已有不同的更新"})
	}()
	c, e := protocol.Connect(context.Background(), a, "device")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	client := workerUpgradeClient{directory: t.TempDir(), device: "device"}
	state, e := client.update(context.Background(), c, &domain.WorkerBuild{BuildID: "next"}, true, "when-idle")
	if e == nil || state == nil || state.Phase != WorkerUpdateFailed {
		t.Fatal("definite refusal became permanent unknown intent", state, e)
	}
}
