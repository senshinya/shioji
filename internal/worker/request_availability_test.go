package worker

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestIdleLegacyMaintenanceDoesNotWaitForDispatch(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "availability"})
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	done := make(chan error, 1)
	go func() { done <- s.advanceWorkerMaintenance(time.Now()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("maintenance without a pending operation waits for dispatch")
	}
}

func TestWorkerReadsRemainAvailableDuringMaintenance(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "availability"})
	in := domain.Submission{OperationID: "availability-op", WorkspaceID: w.ID, Text: "fixture", Config: domain.RunConfig{Model: "availability-model", Permission: "limited"}}
	if _, err := s.submit(in); err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, in.OperationID)
	go s.Serve()

	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	s.updating.Store(true)
	defer s.updating.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(s.config.Directory, "worker.sock"))
	}, w.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var snapshot domain.Snapshot
	if err = client.Call(ctx, "snapshot", struct{}{}, &snapshot); err != nil || snapshot.DeviceID != w.DeviceID {
		t.Fatalf("snapshot unavailable while maintenance is waiting: %v", err)
	}
	var ready domain.Readiness
	if err = client.Call(ctx, "readiness", nil, &ready); err != nil || ready.State != "ready" {
		t.Fatalf("readiness unavailable during maintenance: %v", err)
	}
	var op domain.Operation
	if err = client.Call(ctx, "operation", map[string]string{"id": in.OperationID}, &op); err != nil || op.State != domain.OperationConfirmed {
		t.Fatalf("durable receipt unavailable during maintenance: %v, %v", op.State, err)
	}
}
