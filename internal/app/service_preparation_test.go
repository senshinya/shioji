package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"strings"
	"testing"
	"time"
)

func TestServicePreparationPlansOnlySafeForwardUpdates(t *testing.T) {
	candidate := domain.WorkerBuild{BuildID: "candidate", BuiltAt: 200, SafeUpdate: true, Schema: 12}
	for _, tc := range []struct {
		name         string
		minor        int
		old          domain.WorkerBuild
		update, fail bool
	}{
		{"old capabilities", 15, domain.WorkerBuild{BuildID: "old", SafeUpdate: true, Schema: 10}, true, false},
		{"compatible older", 20, domain.WorkerBuild{BuildID: "old", BuiltAt: 100, SafeUpdate: true}, false, false},
		{"same", 20, candidate, false, false},
		{"newer", 20, domain.WorkerBuild{BuildID: "newer", BuiltAt: 300, SafeUpdate: true}, false, false},
		{"unknown age compatible", 20, domain.WorkerBuild{BuildID: "unknown", SafeUpdate: true}, false, false},
		{"unsupported", 15, domain.WorkerBuild{BuildID: "old"}, false, true},
		{"newer incompatible", 15, domain.WorkerBuild{BuildID: "newer", BuiltAt: 300, SafeUpdate: true}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, e := planServicePreparation(transport.WorkerInfo{DeviceID: "device", Minor: tc.minor, Build: &tc.old}, candidate)
			if (e != nil) != tc.fail || (p != nil) != tc.update {
				t.Fatal(p, e)
			}
			if p != nil && (p.Info.DeviceID != "device" || p.Candidate.BuildID != candidate.BuildID || p.Phase != ServiceStaging) {
				t.Fatal(p)
			}
		})
	}
}

func preparationClient(t *testing.T, ready bool, lose bool) (*protocol.Client, *int) {
	t.Helper()
	a, b := net.Pipe()
	count := new(int)
	go func() {
		defer b.Close()
		codec, epoch, e := protocol.Welcome(b, "device", domain.WorkerBuild{BuildID: "old", SafeUpdate: true})
		if e != nil {
			return
		}
		var f protocol.Frame
		if codec.Read(&f) != nil {
			return
		}
		*count++
		if f.Method != "worker.update" {
			t.Error("unexpected command", f.Method)
		}
		if lose {
			return
		}
		body, _ := json.Marshal(domain.WorkerUpdateResult{Ready: ready, Reason: "busy"})
		codec.Write(protocol.Frame{Type: "result", ID: f.ID, DeviceID: "device", Epoch: epoch, Body: body})
	}()
	c, e := protocol.Connect(context.Background(), a, "device")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c, count
}
func TestServicePreparationBusyWaitsAndLostReplyOnlyObserves(t *testing.T) {
	for _, tc := range []struct {
		ready, lose bool
		phase       ServicePreparationPhase
	}{{false, false, ServiceWaiting}, {true, false, ServiceReconnecting}, {true, true, ServiceObserving}} {
		c, _ := preparationClient(t, tc.ready, tc.lose)
		p := ServicePreparation{ID: "same-operation", Info: transport.WorkerInfo{DeviceID: "device", Build: &domain.WorkerBuild{BuildID: "old"}}, Candidate: domain.WorkerBuild{BuildID: "new"}, Phase: ServiceDispatching}
		got, e := updatePreparedService(context.Background(), c, p)
		if e != nil || got.Phase != tc.phase || got.ID != p.ID {
			t.Fatal(got, e)
		}
		if tc.lose && got.NextAction() != "observe" {
			t.Fatal("uncertain update would be repeated", got)
		}
	}
	if (ServicePreparation{Phase: ServiceDispatching}).NextAction() != "observe" {
		t.Fatal("restored dispatch must observe")
	}
}

func TestServicePreparationRecordsPersistIndependently(t *testing.T) {
	m := NewModel()
	m.ServicePreparations = map[string]*ServicePreparation{"A": {ID: "first", Info: transport.WorkerInfo{DeviceID: "A"}, Phase: ServiceDispatching}, "B": {ID: "second", Info: transport.WorkerInfo{DeviceID: "B"}, Phase: ServiceWaiting}}
	m.ServicePreparationDeviceID = "B"
	body, e := m.EncodeState()
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if e = WriteState(path, body); e != nil {
		t.Fatal(e)
	}
	loaded := NewModel()
	if e = loaded.Load(path); e != nil {
		t.Fatal(e)
	}
	if loaded.ServicePreparations["A"].NextAction() != "observe" || loaded.ServicePreparations["B"].NextAction() != "update" || loaded.ServicePreparationDeviceID != "B" {
		t.Fatal(loaded.ServicePreparations)
	}
}

func TestServicePreparationSaveFailurePreventsDispatch(t *testing.T) {
	c := NewDeviceController("/missing", "local", nil, "/missing", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	done := false
	c.Do(Job{Method: "$device.prepare-service", Input: PrepareService{Action: "update", Preparation: ServicePreparation{ID: "persist-first", Phase: ServiceDispatching}}, Prepare: func() error { return errors.New("saved-intent-marker") }, Apply: func(_ *Model, _ json.RawMessage, e error) {
		var remote *protocol.RemoteError
		if !errors.As(e, &remote) || remote.Code != "notSent" || !strings.Contains(e.Error(), "saved-intent-marker") {
			t.Error("dispatch passed failed persistence", e)
		}
		done = true
	}})
	m := NewModel()
	timeout := time.After(3 * time.Second)
	for !done {
		select {
		case update := <-c.Updates:
			update(m)
		case <-timeout:
			t.Fatal("no result")
		}
	}
}

func TestServicePreparationObservedBuildMustBeSameDirectoryAndForward(t *testing.T) {
	p := ServicePreparation{Info: transport.WorkerInfo{DeviceID: "device", Directory: "/worker", Build: &domain.WorkerBuild{BuildID: "old"}}, Candidate: domain.WorkerBuild{BuildID: "target", BuiltAt: 200}}
	for _, tc := range []struct {
		device, dir, id string
		at              int64
		minor           int
		want            bool
	}{{"device", "/worker", "target", 200, 20, true}, {"device", "/worker", "newer", 300, 20, true}, {"device", "/worker", "old", 300, 20, false}, {"device", "/other", "target", 200, 20, false}, {"other", "/worker", "target", 200, 20, false}, {"device", "/worker", "target", 200, 15, false}} {
		if got := p.ReadyAt(transport.WorkerInfo{DeviceID: tc.device, Directory: tc.dir, Minor: tc.minor, Build: &domain.WorkerBuild{BuildID: tc.id, BuiltAt: tc.at}}); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
