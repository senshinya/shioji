package app

import (
	"shioji/internal/domain"
	"testing"
)

func TestWorkerServiceDoesNotAutomaticallyDowngradeOrGuessLegacySafety(t *testing.T) {
	old := &domain.WorkerBuild{BuildID: "old", BuiltAt: 10, SafeUpdate: true}
	next := &domain.WorkerBuild{BuildID: "new", BuiltAt: 20, SafeUpdate: true}
	if s := newWorkerService(old, next); s.Phase != WorkerAvailable || !s.Automatic() {
		t.Fatal("newer package not available", s)
	}
	if s := newWorkerService(next, old); s.Phase != WorkerNewer || s.Automatic() {
		t.Fatal("old client would downgrade worker", s)
	}
	if s := newWorkerService(nil, next); s.Phase != WorkerLegacy || s.Automatic() {
		t.Fatal("legacy worker assumed safe", s)
	}
	old.BuiltAt = 0
	if s := newWorkerService(old, next); s.Phase != WorkerAvailable || s.Automatic() {
		t.Fatal("unknown build order auto updated", s)
	}
}
func TestWorkerServiceProjectionStaysOnOwningDevice(t *testing.T) {
	m := NewModel()
	m.ActiveConnection = "remote"
	m.Connections = []DeviceConnection{{ID: "remote", DeviceID: "B"}}
	m.ApplyDeviceUpdate("local", func(p *Model) {
		p.Snapshot = domain.Snapshot{DeviceID: "A"}
		p.WorkerService = &WorkerService{Phase: WorkerWaiting, Message: "local busy"}
	})
	m.ApplyDeviceUpdate("remote", func(p *Model) {
		p.Snapshot = domain.Snapshot{DeviceID: "B"}
		p.WorkerService = &WorkerService{Phase: WorkerCurrent}
	})
	if m.WorkerService == nil || m.WorkerService.Phase != WorkerCurrent || m.DeviceStates["local"].WorkerService.Message != "local busy" {
		t.Fatal("local update polluted remote device", m.WorkerService)
	}
}
