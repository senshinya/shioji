package worker_test

import (
	"shioji/internal/domain"
	"testing"
)

func TestDeviceHealthAndKeepAwakeAreOwnedByWorkerAcrossClients(t *testing.T) {
	server, client, _ := startServer(t)
	var before, after domain.Snapshot
	call(t, client, "snapshot", nil, &before)
	var health domain.DeviceHealth
	call(t, client, "device.health", nil, &health)
	if health.DeviceID != before.DeviceID || health.ObservedAt.IsZero() {
		t.Fatal("health lacks worker identity and observation time", health)
	}
	other := connect(t, server.Directory())
	call(t, client, "device.keepAwake", domain.KeepAwakeInput{Enabled: true, Revision: health.KeepAwake.Revision}, &health)
	call(t, other, "device.health", nil, &health)
	if !health.KeepAwake.Enabled {
		t.Fatal("another client cannot observe the device assertion")
	}
	call(t, other, "device.keepAwake", domain.KeepAwakeInput{Enabled: false, Revision: health.KeepAwake.Revision}, &health)
	if health.KeepAwake.Enabled {
		t.Fatal("device assertion was not released")
	}
	call(t, client, "snapshot", nil, &after)
	if before.Sequence != after.Sequence {
		t.Fatal("telemetry changed the conversation journal")
	}
}
