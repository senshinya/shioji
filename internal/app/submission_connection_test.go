package app

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestLostSubmitKeepsConnectionAndQueriesUntilOriginalReceiptArrives(t *testing.T) {
	for _, receiptAtFirstCheck := range []bool{false, true} {
		name := "missing first"
		if receiptAtFirstCheck {
			name = "available first"
		}
		t.Run(name, func(t *testing.T) { checkLostSubmitRecovery(t, receiptAtFirstCheck) })
	}
}

func checkLostSubmitRecovery(t *testing.T, receiptAtFirstCheck bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var submitted, lookedUp, watches atomic.Int32
	watchStarted := make(chan struct{})
	var receiptReady atomic.Bool
	receiptReady.Store(receiptAtFirstCheck)
	c := NewController("")
	c.Dial = func(context.Context) (net.Conn, error) {
		client, worker := net.Pipe()
		go func() {
			defer worker.Close()
			stop := context.AfterFunc(ctx, func() { worker.Close() })
			defer stop()
			codec, _, err := protocol.Welcome(worker, "device")
			if err != nil {
				return
			}
			for {
				var f protocol.Frame
				if codec.Read(&f) != nil {
					return
				}
				switch f.Method {
				case "snapshot":
					result(codec, f, domain.Snapshot{DeviceID: "device", Generation: "same-worker"})
				case "readiness":
					result(codec, f, domain.Readiness{State: "ready"})
				case "events.watch":
					if watches.Add(1) == 1 {
						close(watchStarted)
					}
					<-ctx.Done()
					return
				case "session.submit":
					submitted.Add(1)
					return // The command stream lost the reply, not the device.
				case "operation":
					lookedUp.Add(1)
					if !receiptReady.Load() {
						_ = codec.Write(protocol.Frame{Type: "result", ID: f.ID, DeviceID: f.DeviceID, Epoch: f.Epoch, ErrorCode: "notFound", Error: "receipt not written yet"})
					} else {
						result(codec, f, domain.Operation{ID: "original", SessionID: "s", State: domain.OperationConfirmed})
					}
				case "probe":
					result(codec, f, true)
				}
			}
		}()
		return client, nil
	}
	go c.Run(ctx)
	m := NewModel()
	observing, dropped := false, false
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.After(6 * time.Second)
		for !ready() {
			select {
			case u := <-c.Updates:
				u(m)
				dropped = dropped || observing && !m.Connected
			case <-deadline:
				t.Fatal("controller did not reach the expected result")
			}
		}
	}
	wait(func() bool { return m.Connected })
	select {
	case <-watchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial event watch did not start")
	}
	observing = true
	epoch := m.ConnectionEpoch
	in := domain.Submission{OperationID: "original", SessionID: "s", WorkspaceID: "w", Text: "keep until the original settles"}
	m.Pending[in.OperationID] = in
	m.Drafts["session:s"] = in.Text
	warned, received := false, false
	if !c.Do(Job{Method: "session.submit", ConnectionEpoch: epoch, Input: in, Apply: func(m *Model, _ json.RawMessage, err error) {
		if err != nil {
			m.SubmissionError(in, err)
		}
		warned = m.UncertainSubmissions[in.OperationID]
		received = true
	}}) {
		t.Fatal("could not queue submission")
	}
	wait(func() bool { return received })
	if dropped || !m.Connected || m.ConnectionEpoch != epoch {
		t.Fatal("a lost reply disconnected a worker still responding on a fresh socket")
	}
	commandFinished := false
	if !c.Do(Job{Method: "probe", ConnectionEpoch: epoch, Apply: func(_ *Model, data json.RawMessage, err error) {
		if err != nil || string(data) != "true" {
			t.Errorf("healthy worker command after reply loss: %s, %v", data, err)
		}
		commandFinished = true
	}}) {
		t.Fatal("could not queue next command")
	}
	wait(func() bool { return commandFinished && lookedUp.Load() > 0 })
	if receiptAtFirstCheck {
		if warned || !m.Resolved[in.OperationID] {
			t.Fatal("reported uncertainty before observing the available receipt")
		}
	} else {
		if !warned || m.Resolved[in.OperationID] || m.Drafts["session:s"] != in.Text {
			t.Fatal("a missing receipt was treated as a definitive result")
		}
		receiptReady.Store(true)
		wait(func() bool { return m.Resolved[in.OperationID] })
	}
	if dropped || m.ConnectionEpoch != epoch || submitted.Load() != 1 || watches.Load() != 1 {
		t.Fatalf("receipt recovery reset the connection, event watch or replayed input: dropped=%t submits=%d watches=%d", dropped, submitted.Load(), watches.Load())
	}
}

func TestDeviceEventsHistoryAndStopContinueWhileSubmissionWaitsForReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	submitted := make(chan struct{})
	reply := make(chan struct{})
	interrupted := make(chan struct{})
	c := NewController("")
	c.Dial = func(context.Context) (net.Conn, error) {
		client, worker := net.Pipe()
		go func() {
			defer worker.Close()
			stop := context.AfterFunc(ctx, func() { worker.Close() })
			defer stop()
			codec, _, err := protocol.WelcomeWorker(worker, "device", "same-worker", domain.WorkerBuild{})
			if err != nil {
				return
			}
			for {
				var f protocol.Frame
				if codec.Read(&f) != nil {
					return
				}
				switch f.Method {
				case "snapshot":
					result(codec, f, domain.Snapshot{DeviceID: "device", Generation: "same-worker"})
				case "readiness":
					result(codec, f, domain.Readiness{State: "ready"})
				case "events.watch":
					var q protocol.EventsQuery
					if json.Unmarshal(f.Body, &q) != nil || q.After != 0 {
						<-ctx.Done()
						return
					}
					select {
					case <-ctx.Done():
						return
					case <-submitted:
					}
					event := domain.Event{Sequence: 1, Type: "workspace", Workspace: &domain.Workspace{ID: "live", DeviceID: "device", Revision: 1}}
					result(codec, f, protocol.EventPage{Generation: "same-worker", Events: []domain.Event{event}})
				case "turn.interrupt":
					close(interrupted)
					result(codec, f, nil)
				case "session.outline":
					result(codec, f, domain.HistoryOutline{SessionID: "other"})
				case "session.submit":
					close(submitted)
					select {
					case <-ctx.Done():
						return
					case <-reply:
					}
					result(codec, f, domain.Operation{ID: "awaiting", SessionID: "s", State: domain.OperationAccepted})
				}
			}
		}()
		return client, nil
	}
	go c.Run(ctx)
	m := NewModel()
	wait := func(ready func() bool, message string) {
		t.Helper()
		timeout := time.After(2 * time.Second)
		for !ready() {
			select {
			case u := <-c.Updates:
				u(m)
			case <-timeout:
				t.Fatal(message)
			}
		}
	}
	wait(func() bool { return m.Connected && m.ConnectionEpoch != "" }, "worker catalog did not load")
	settled := false
	c.Do(Job{Method: "session.submit", Input: domain.Submission{OperationID: "awaiting"}, Apply: func(_ *Model, _ json.RawMessage, err error) {
		if err != nil {
			t.Error(err)
		}
		settled = true
	}})
	wait(func() bool { return m.Sequence == 1 }, "live events were blocked behind the pending submission")
	if settled || len(m.Snapshot.Workspaces) != 1 || m.Snapshot.Workspaces[0].ID != "live" {
		t.Fatal("worker event did not reach the model while submission was pending")
	}
	if !c.Do(Job{Method: "turn.interrupt", Input: map[string]string{"sessionId": "other", "turnId": "running"}}) {
		t.Fatal("Stop was not queued")
	}
	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Fatal("Stop for another session waited behind the submission")
	}
	historyRead := false
	c.Do(Job{Method: "session.outline", Input: domain.HistoryQuery{SessionID: "other"}, Apply: func(_ *Model, body json.RawMessage, err error) {
		var outline domain.HistoryOutline
		if err != nil || json.Unmarshal(body, &outline) != nil || outline.SessionID != "other" {
			t.Errorf("history read failed while submission waited: %s, %v", body, err)
		}
		historyRead = true
	}})
	wait(func() bool { return historyRead }, "history read waited behind the submission")
	close(reply)
	wait(func() bool { return settled }, "submission did not settle after its reply")
}

func TestEventWatchRestartsAtItsCursorWithoutReloadingSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var snapshots atomic.Int32
	var interrupted atomic.Bool
	c := NewController("")
	c.Dial = func(context.Context) (net.Conn, error) {
		client, worker := net.Pipe()
		go func() {
			defer worker.Close()
			stop := context.AfterFunc(ctx, func() { worker.Close() })
			defer stop()
			codec, _, err := protocol.WelcomeWorker(worker, "device", "same-worker", domain.WorkerBuild{})
			if err != nil {
				return
			}
			for {
				var f protocol.Frame
				if codec.Read(&f) != nil {
					return
				}
				switch f.Method {
				case "snapshot":
					snapshots.Add(1)
					result(codec, f, domain.Snapshot{DeviceID: "device", Generation: "same-worker"})
				case "readiness":
					result(codec, f, domain.Readiness{State: "ready"})
				case "events.watch":
					var query protocol.EventsQuery
					if json.Unmarshal(f.Body, &query) != nil || query.After > 1 {
						<-ctx.Done()
						return
					}
					if query.After == 1 && interrupted.CompareAndSwap(false, true) {
						return
					}
					revision := query.After + 1
					workspace := domain.Workspace{ID: "w", DeviceID: "device", Revision: revision}
					result(codec, f, protocol.EventPage{Generation: "same-worker", Events: []domain.Event{{Sequence: revision, Type: "workspace", Workspace: &workspace}}})
				}
			}
		}()
		return client, nil
	}
	go c.Run(ctx)
	m := NewModel()
	var firstEpoch string
	deadline := time.After(4 * time.Second)
	for m.Sequence < 2 {
		select {
		case update := <-c.Updates:
			update(m)
			if firstEpoch == "" && m.Connected {
				firstEpoch = m.ConnectionEpoch
			}
			if firstEpoch != "" && (!m.Connected || m.ConnectionEpoch != firstEpoch) {
				t.Fatal("losing a watcher disconnected a reachable worker")
			}
		case <-deadline:
			t.Fatal("event stream did not resume at the previous cursor")
		}
	}
	if snapshots.Load() != 1 || !interrupted.Load() || m.Snapshot.Workspaces[0].Revision != 2 {
		t.Fatal("watch recovery rebuilt the snapshot or skipped a live update")
	}
}

func TestCatalogRetryKeepsAnAuthenticatedWorkerOnline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	c := NewController("")
	c.Dial = func(context.Context) (net.Conn, error) {
		client, worker := net.Pipe()
		go func() {
			defer worker.Close()
			defer context.AfterFunc(ctx, func() { worker.Close() })()
			codec, _, err := protocol.WelcomeWorker(worker, "device", "generation", domain.WorkerBuild{})
			if err != nil {
				return
			}
			for {
				var f protocol.Frame
				if codec.Read(&f) != nil {
					return
				}
				switch f.Method {
				case "snapshot":
					if attempts.Add(1) == 1 {
						_ = codec.Write(protocol.Frame{Type: "result", ID: f.ID, DeviceID: f.DeviceID, Epoch: f.Epoch, Error: "temporary catalog failure"})
					} else {
						result(codec, f, domain.Snapshot{DeviceID: "device", Generation: "generation"})
					}
				case "readiness":
					result(codec, f, domain.Readiness{State: "ready"})
				case "events.watch":
					<-ctx.Done()
					return
				}
			}
		}()
		return client, nil
	}
	go c.Run(ctx)
	m := NewModel()
	seenOnline := false
	timeout := time.After(3 * time.Second)
	for m.ConnectionEpoch == "" {
		select {
		case u := <-c.Updates:
			u(m)
			if seenOnline && !m.Connected {
				t.Fatal("a catalog failure made the authenticated worker appear offline")
			}
			seenOnline = seenOnline || m.Connected
		case <-timeout:
			t.Fatal("catalog did not recover")
		}
	}
	if !seenOnline || attempts.Load() != 2 || m.Error != "" || m.Snapshot.Generation != "generation" {
		t.Fatalf("catalog did not recover cleanly: attempts=%d, error=%q, generation=%q", attempts.Load(), m.Error, m.Snapshot.Generation)
	}
}
