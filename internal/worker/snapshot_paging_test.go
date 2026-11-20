package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func TestSnapshotReadSpansFrameLimit(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "audit"})
	goal := strings.Repeat("🧪", domain.MaxGoalRunes)
	for i := 0; i < 68; i++ {
		input := domain.Submission{OperationID: fmt.Sprintf("goal-%d", i), WorkspaceID: w.ID, Text: "/goal " + goal, GoalObjective: goal, Config: domain.RunConfig{Model: "audit-model", Permission: "limited"}}
		op, e := s.store.Accept(input)
		if e != nil {
			t.Fatal(e)
		}
		d, e := s.store.Session(op.SessionID, 0, 1)
		if e != nil {
			t.Fatal(e)
		}
		session := d.Session
		session.ThreadID = fmt.Sprintf("fixture-thread-%d", i)
		if _, e = s.store.Apply(op.SessionID, domain.Event{Type: "session", Session: &session}); e != nil {
			t.Fatal(e)
		}
		state := &domain.Goal{Objective: goal, State: domain.GoalComplete, CreatedAt: 1}
		if _, e = s.store.Apply(op.SessionID, domain.Event{Type: "goal", Goal: state, GoalSource: session.ThreadID}); e != nil {
			t.Fatal(e)
		}
	}
	snapshot, e := s.store.Snapshot()
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(snapshot)
	if e != nil || len(b) <= protocol.MaxFrame {
		t.Fatalf("fixture did not cross limit: %d, %v", len(b), e)
	}
	go s.Serve()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, e := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(s.config.Directory, "worker.sock"))
	}, w.DeviceID)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	if e = client.Call(ctx, "device.ping", nil, nil); e != nil {
		t.Fatal(e)
	}
	controller := app.NewController(s.config.Directory)
	controller.ExpectedDeviceID = w.DeviceID
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		controller.Run(ctx)
	}()
	defer func() { cancel(); <-stopped }()
	model := app.NewModel()
	for model.ConnectionEpoch == "" {
		select {
		case update := <-controller.Updates:
			update(model)
		case <-ctx.Done():
			t.Fatalf("catalog never became usable: %v", model.Error)
		}
	}
	if !model.Connected || len(model.Snapshot.Sessions) != 68 || model.Snapshot.DeviceID != w.DeviceID ||
		model.Sequence < snapshot.Sequence {
		t.Fatalf("controller did not apply the whole catalog: online=%t, sessions=%d, sequence=%d", model.Connected, len(model.Snapshot.Sessions), model.Sequence)
	}
	t.Logf("controller loaded %d sessions in a %d-byte catalog through the %d-byte frame limit", len(model.Snapshot.Sessions), len(b), protocol.MaxFrame)
}
