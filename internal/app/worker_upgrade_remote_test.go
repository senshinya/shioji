package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"shioji/internal/transport"
)

func TestRemoteRepairRunsWithBusinessWorkerOffline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	connection := DeviceConnection{ID: "remote", DeviceID: "device-B", SSH: transport.SSH{
		Target: "peer", Worker: "/remote/worker", StateDirectory: "/remote/state",
	}}
	// A missing helper gives an immediate, attributable error if the action
	// reaches the SSH recovery path. No real device is needed for this test.
	missing := t.TempDir() + "/missing-worker"
	controller := NewDeviceController(t.TempDir(), "device-A", []DeviceConnection{connection}, missing, nil)
	child := controller.fleet.sources["remote"].controller
	child.paths.open = func(context.Context, transport.SSH) (net.Conn, error) {
		return nil, errors.New("business writer unavailable")
	}
	in := domain.WorkerReplacement{ID: "interrupted", BuildID: "staged", Explicit: true, Policy: "when-idle"}
	op := supervisor.Operation{DeviceID: "device-B", Input: in, Phase: "blocked"}
	if err := child.upgradeObserver("device-B", nil).save(&workerUpgradeRecord{DeviceID: "device-B", Input: in, Operation: &op}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { controller.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	result := make(chan error, 1)
	controller.Do(Job{ConnectionID: "remote", Method: "$worker.repair", Input: in.ID, Apply: func(_ *Model, _ json.RawMessage, err error) {
		result <- err
	}})
	model := NewModel()
	for {
		select {
		case update := <-controller.Updates:
			update(model)
		case err := <-result:
			if err == nil || !strings.Contains(err.Error(), "执行服务签名校验失败") {
				t.Fatal("repair did not try the independent service control path", err)
			}
			return
		case <-ctx.Done():
			t.Fatal("offline controller did not handle the repair action")
		}
	}
}

func TestDetachedRepairUsesStagedBuildAndQueriesLostReceipt(t *testing.T) {
	in := domain.WorkerReplacement{ID: "interrupted", BuildID: "staged", ExpectedBuildID: "old", Explicit: true, Policy: "when-idle"}
	blocked := supervisor.Operation{DeviceID: "device-B", Input: in, Phase: "blocked"}
	var sent domain.WorkerReplacement
	calls := 0
	lost := errors.New("SSH reply lost")
	u := workerUpgradeClient{directory: t.TempDir(), device: "device-B"}
	u.control = func(_ context.Context, _, id string) (supervisor.Operation, error) {
		if id == "" {
			return blocked, nil
		}
		if id == sent.ID {
			return supervisor.Operation{}, lost
		}
		t.Fatalf("unexpected receipt query %q", id)
		return supervisor.Operation{}, nil
	}
	u.repair = func(_ context.Context, input domain.WorkerReplacement) (supervisor.Operation, error) {
		calls++
		sent = input
		if saved, err := u.load(); err != nil || saved.Input != input {
			t.Fatalf("repair dispatched before saving its operation ID: %v %v", saved, err)
		}
		return supervisor.Operation{}, lost
	}
	if err := u.save(&workerUpgradeRecord{DeviceID: "device-B", Input: in, Operation: &blocked}); err != nil {
		t.Fatal(err)
	}
	candidate := &domain.WorkerBuild{BuildID: "controller-bundle"}
	state, err := u.retryDetached(context.Background(), in.ID, candidate)
	if !errors.Is(err, lost) || state == nil || state.Phase != WorkerUpdating || sent.ID == "" || sent.ID == in.ID || sent.BuildID != "staged" {
		t.Fatalf("expected one new repair for the signed staged target, got %+v, %+v, %v", state, sent, err)
	}
	if _, err = u.retryDetached(context.Background(), in.ID, candidate); !errors.Is(err, lost) {
		t.Fatal("retry did not query the unconfirmed repair", err)
	}
	if saved, err := u.load(); err != nil || saved.Input != sent || calls != 1 {
		t.Fatalf("lost reply triggered another repair: %v %v calls=%d", saved, err, calls)
	}
}

func TestRemoteUpgradeLostReplyQueriesOriginalOperationAfterRouteAndClientRestart(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	accepted := make(chan domain.WorkerReplacement, 1)
	peer := func(build string, receipt *domain.WorkerReplacement) *protocol.Client {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			codec, epoch, err := protocol.Welcome(b, "device-B", domain.WorkerBuild{BuildID: build, Lifecycle: 1, SafeUpdate: true})
			if err != nil {
				return
			}
			for {
				var frame protocol.Frame
				if codec.Read(&frame) != nil {
					return
				}
				if receipt == nil {
					if frame.Method != "worker.update" {
						t.Error("initial update was not dispatched", frame.Method)
						return
					}
					var in domain.WorkerReplacement
					if err := json.Unmarshal(frame.Body, &in); err != nil {
						t.Error(err)
						return
					}
					accepted <- in
					return // Device accepted the operation, but its reply was lost.
				}
				if frame.Method != "worker.update.status" {
					t.Error("restart replayed an update instead of querying", frame.Method)
					return
				}
				var query struct{ ID string }
				if err := json.Unmarshal(frame.Body, &query); err != nil || query.ID != "" && query.ID != receipt.ID {
					t.Error("query lost the original operation identity", query.ID, err)
					return
				}
				raw, _ := json.Marshal(supervisor.Operation{DeviceID: "device-B", Input: *receipt, Phase: "applied"})
				if codec.Write(protocol.Frame{Type: "result", ID: frame.ID, Epoch: epoch, DeviceID: "device-B", Body: raw}) != nil {
					return
				}
			}
		}()
		client, err := protocol.Connect(ctx, a, "device-B")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close() })
		return client
	}
	connection := DeviceConnection{ID: "remote", DeviceID: "device-B", SSH: transport.SSH{Target: "original-peer", Worker: "/remote/worker", StateDirectory: "/remote/state"}}
	controller := NewDeviceController(directory, "device-A", []DeviceConnection{connection}, "", nil)
	observer := controller.fleet.sources["remote"].controller.upgradeObserver("device-B", nil)
	candidate := &domain.WorkerBuild{BuildID: "next", Lifecycle: 1, SafeUpdate: true}
	if _, err := observer.update(ctx, peer("old", nil), candidate, false, "when-idle", false); err == nil {
		t.Fatal("lost update reply was reported as confirmed")
	}
	var in domain.WorkerReplacement
	select {
	case in = <-accepted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The same device is reached through another address and connection alias.
	// Its receipt must still come from the controlling Mac and its stable ID.
	connection.ID, connection.SSH.Target = "new-route", "replacement-peer"
	for restart := 0; restart < 2; restart++ {
		controller = NewDeviceController(directory, "device-A", []DeviceConnection{connection}, "", nil)
		child := controller.fleet.sources["new-route"].controller
		observer = child.upgradeObserver("device-B", nil)
		observer.control = func(context.Context, string, string) (supervisor.Operation, error) {
			t.Error("healthy remote status query incorrectly used the fallback control path")
			return supervisor.Operation{}, errors.New("unexpected fallback")
		}
		state, err := observer.update(ctx, peer("next", &in), candidate, false, "when-idle", false)
		if err != nil || state == nil || state.Phase != WorkerCurrent || state.OperationID != in.ID {
			t.Fatal("remote restart did not recover the applied original operation", state, err)
		}
		if child.Directory != "" || child.currentSSH().StateDirectory != "/remote/state" {
			t.Fatal("receipt recovery changed the remote execution directory")
		}
		other, err := child.upgradeObserver("another-device", nil).load()
		if err != nil || other != nil {
			t.Fatal("one device inherited another device's update receipt", other, err)
		}
	}
}
