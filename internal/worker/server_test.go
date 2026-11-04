package worker_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/testutil"
	"shioji/internal/worker"
	"syscall"
	"testing"
	"time"
)

func startServer(t *testing.T) (*worker.Server, *protocol.Client, *testutil.Provider) {
	t.Helper()
	p := testutil.NewProvider(t)
	dir, err := os.MkdirTemp("", "sw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	server, err := worker.New(context.Background(), worker.Config{Directory: dir, CodexHome: p.Home})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	go server.Serve()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return server, client, p
}
func call(t *testing.T, c *protocol.Client, m string, in, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Call(ctx, m, in, out); err != nil {
		t.Fatal(err)
	}
}
func wait(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestWorkerDisconnectDoesNotStopParallelSessions(t *testing.T) {
	server, c, p := startServer(t)
	_ = server
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	if ready.State != "ready" {
		t.Fatalf("readiness: %+v", ready)
	}
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Test", Folders: []string{t.TempDir()}}, &w)
	var slow, fast domain.Operation
	cfg := domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}
	call(t, c, "session.submit", domain.Submission{OperationID: "slow", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: cfg}, &slow)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("slow not started")
	}
	call(t, c, "session.submit", domain.Submission{OperationID: "fast", WorkspaceID: w.ID, Text: "SHIOJI_FAST", Config: cfg}, &fast)
	c.Close()
	c2 := connect(t, server.Directory())
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c2, "session", worker.SessionQuery{ID: fast.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	call(t, c2, "session", worker.SessionQuery{ID: slow.SessionID}, &d)
	if d.Session.Status != "running" {
		t.Fatalf("disconnect stopped active session: %s", d.Session.Status)
	}
	close(p.Release)
	wait(t, func() bool {
		call(t, c2, "session", worker.SessionQuery{ID: slow.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	var snap domain.Snapshot
	call(t, c2, "snapshot", nil, &snap)
	if len(snap.Sessions) != 2 {
		t.Fatalf("sessions=%d", len(snap.Sessions))
	}
}
func connect(t *testing.T, dir string) *protocol.Client {
	t.Helper()
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := protocol.Connect(context.Background(), conn, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func TestWorkerPendingQuestionSurvivesReconnectAndOnlyOneAnswerIsSent(t *testing.T) {
	server, c, _ := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Test", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "question", WorkspaceID: w.ID, Text: "SHIOJI_QUESTION", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only", Plan: true}}, &op)
	var d domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return len(d.Requests) > 0
	})
	c.Close()
	c2 := connect(t, server.Directory())
	call(t, c2, "session", worker.SessionQuery{ID: op.SessionID}, &d)
	r := d.Requests[0]
	answer := domain.Answer{RequestID: r.ID, Revision: r.Revision, Answers: map[string][]string{"q": {"Yes"}}}
	call(t, c2, "request.respond", worker.AnswerInput{SessionID: op.SessionID, Answer: answer}, nil)
	if err := c2.Call(context.Background(), "request.respond", worker.AnswerInput{SessionID: op.SessionID, Answer: answer}, nil); err == nil {
		t.Fatal("second answer accepted")
	}
	wait(t, func() bool {
		call(t, c2, "session", worker.SessionQuery{ID: op.SessionID}, &d)
		return d.Session.Status == "completed"
	})
	if d.Requests[0].State != "answered" {
		t.Fatalf("request=%+v", d.Requests[0])
	}
}
func TestWrongDeviceAndOldEpochCannotMutateWorker(t *testing.T) {
	server, c, _ := startServer(t)
	conn, err := net.Dial("unix", filepath.Join(server.Directory(), "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	codec := protocol.NewCodec(conn, conn)
	nonce := "nonce"
	codec.Write(protocol.Frame{Type: "hello", Major: 1, Nonce: nonce})
	var welcome protocol.Frame
	if err = codec.Read(&welcome); err != nil {
		t.Fatal(err)
	}
	codec.Write(protocol.Frame{Type: "command", ID: "bad", Method: "workspace.create", DeviceID: c.DeviceID, Epoch: "old", Body: []byte(`{"name":"Bad","folders":["/tmp"]}`)})
	var response protocol.Frame
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if err = codec.Read(&response); err == nil && response.Error == "" {
		t.Fatal("old epoch accepted")
	}
	var snap domain.Snapshot
	call(t, c, "snapshot", nil, &snap)
	if len(snap.Workspaces) != 0 {
		t.Fatal("old epoch mutated state")
	}
}

func TestAppServerDeathMarksExecutionUnknownWithoutReplayingInput(t *testing.T) {
	server, c, p := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Test", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "crash", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
	select {
	case <-p.Started:
	case <-time.After(10 * time.Second):
		t.Fatal("turn not dispatched")
	}
	b, err := os.ReadFile(filepath.Join(server.Directory(), "runtime-process.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		PID int `json:"pid"`
	}
	if err = json.Unmarshal(b, &record); err != nil {
		t.Fatal(err)
	}
	if record.PID <= 0 {
		t.Fatal("no owned harness process")
	}
	if err = syscall.Kill(record.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return detail.Session.Status == "unknown"
	})
	for _, turn := range detail.Turns {
		if turn.Status == "running" {
			t.Fatal("dead harness still has a running turn")
		}
	}
	var again domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "crash", WorkspaceID: w.ID, Text: "SHIOJI_SLOW", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &again)
	if again.SessionID != op.SessionID {
		t.Fatal("replayed into a new session")
	}
	select {
	case <-p.Started:
		t.Fatal("input was replayed")
	default:
	}
	close(p.Release)
}

func TestWorkerExecutionApprovalCanBeDenied(t *testing.T) {
	_, c, _ := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	var w domain.Workspace
	call(t, c, "workspace.create", worker.WorkspaceInput{Name: "Test", Folders: []string{t.TempDir()}}, &w)
	var op domain.Operation
	call(t, c, "session.submit", domain.Submission{OperationID: "approval", WorkspaceID: w.ID, Text: "SHIOJI_APPROVAL", Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "default"}}, &op)
	var detail domain.SessionDetail
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return len(detail.Requests) > 0
	})
	r := detail.Requests[0]
	if r.Kind != "approval" {
		t.Fatalf("request kind=%s", r.Kind)
	}
	call(t, c, "request.respond", worker.AnswerInput{SessionID: op.SessionID, Answer: domain.Answer{RequestID: r.ID, Revision: r.Revision, Decision: "decline"}}, nil)
	wait(t, func() bool {
		call(t, c, "session", worker.SessionQuery{ID: op.SessionID}, &detail)
		return detail.Session.Status == "completed"
	})
	if detail.Requests[0].State != "answered" {
		t.Fatalf("request=%+v", detail.Requests[0])
	}
}
