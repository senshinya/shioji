package worker_test

import (
	"context"
	"shioji/internal/domain"
	"testing"
)

func TestDevicePingDoesNotMutateJournal(t *testing.T) {
	_, client, _ := startServer(t)
	var before, after domain.Snapshot
	call(t, client, "snapshot", nil, &before)
	for i := 0; i < 3; i++ {
		var reply struct {
			DeviceID string `json:"deviceId"`
		}
		if err := client.Call(context.Background(), "device.ping", nil, &reply); err != nil {
			t.Fatal(err)
		}
		if reply.DeviceID != before.DeviceID {
			t.Fatal("probe did not verify owning worker")
		}
	}
	call(t, client, "snapshot", nil, &after)
	if after.Sequence != before.Sequence {
		t.Fatal("read-only ping changed the event journal")
	}
}
