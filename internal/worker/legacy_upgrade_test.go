package worker

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/harness"
	"shioji/internal/transport"
	"testing"
)

func TestSupervisedStartupArchivesHistoricalPartialFenceWithoutLosingReceipt(t *testing.T) {
	s, _ := memoryWorker(t, idleHarness{})
	dir := s.config.Directory
	device := s.store.DeviceID()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "worker-recovery")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	receipt := []byte(`{"Operation":{"id":"historic","deviceId":"` + device + `","phase":"partial"},"Accepted":true,"Explicit":true,"ExitConfirmed":false,"futureField":{"keep":true}}`)
	if err := os.WriteFile(filepath.Join(root, "historic.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fence.json"), []byte(`{"ID":"historic","DeviceID":"`+device+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), Config{Directory: dir, ServiceInstance: "new-fixed-service", Harnesses: []harness.Adapter{idleHarness{}}})
	if err != nil {
		t.Fatal("historical partial still blocks ordinary fixed-service startup", err)
	}
	defer restarted.Close()
	if fence, err := transport.PendingDeviceRecovery(dir); err != nil || fence != nil {
		t.Fatal("old global fence retained", fence, err)
	}
	after, err := os.ReadFile(filepath.Join(root, "historic.json"))
	if err != nil || string(after) != string(receipt) {
		t.Fatal("legacy receipt fields were rewritten or deleted", string(after), err)
	}
}
