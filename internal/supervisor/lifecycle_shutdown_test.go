package supervisor

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/store"
	"testing"
	"time"
)

// This real child has no business socket. The private pipe is its sole drain
// path, and rejection must leave the same process alive for the next request.
func privateControlFixture(name string) int {
	var dir string
	for i, arg := range os.Args {
		if arg == "--state-dir" && i+1 < len(os.Args) {
			dir = os.Args[i+1]
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "private-ready"), []byte("ready"), 0600)
	f := os.NewFile(3, "private control")
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		for {
			time.Sleep(time.Hour)
		}
	}
	defer conn.Close()
	decode := json.NewDecoder(conn)
	for {
		var in Control
		if decode.Decode(&in) != nil {
			return 1
		}
		if name == "fixture-private-control-slow" {
			time.Sleep(250 * time.Millisecond)
			if !in.AdmitBefore.IsZero() && !time.Now().Before(in.AdmitBefore) {
				_ = os.WriteFile(filepath.Join(dir, "admission-rejected"), []byte("expired"), 0600)
				for {
					time.Sleep(time.Hour)
				}
			}
			if SaveDrainIntent(dir, DrainIntent{ID: in.OperationID, Instance: in.Instance, DeviceID: "fixture-device", BuildID: "old"}) != nil {
				return 1
			}
			if WriteDrained(dir, Drained{PID: os.Getpid(), Instance: in.Instance, OperationID: in.OperationID, DeviceID: "fixture-device", BuildID: "old"}) != nil {
				return 1
			}
			return 43
		}
		out := domain.WorkerUpdateResult{Ready: true}
		if in.Instance != os.Getenv("SHIOJI_WORKER_INSTANCE") {
			return 2
		}
		if in.Replacement.Policy == "when-idle" {
			out.Ready = false
			out.Reason = "busy"
		}
		reply := struct {
			Result domain.WorkerUpdateResult `json:"result"`
			Error  string                    `json:"error,omitempty"`
		}{Result: out}
		if !in.ExpiresAt.IsZero() && !time.Now().Before(in.ExpiresAt) {
			reply.Result.Ready = false
			reply.Error = "prepared update expired"
		}
		if json.NewEncoder(conn).Encode(reply) != nil {
			return 1
		}
		if !reply.Result.Ready {
			continue
		}
		time.Sleep(20 * time.Millisecond)
		if WriteDrained(dir, Drained{PID: os.Getpid(), Instance: in.Instance, OperationID: in.OperationID, DeviceID: "fixture-device", BuildID: "old"}) != nil {
			return 1
		}
		return 43
	}
}
func TestPrivateLifecycleDoesNotNeedBusinessSocket(t *testing.T) {
	dir, options := regressionOptions(t)
	path := filepath.Join(dir, "fixture-private-control")
	if err := os.Symlink(os.Args[0], path); err != nil {
		t.Fatal(err)
	}
	h := &host{options: options, ctx: context.Background(), device: "fixture-device"}
	build := domain.WorkerBuild{BuildID: "old", Schema: store.SchemaVersion, Lifecycle: 1}
	raw, _ := json.Marshal(map[string]any{"buildId": "old", "schema": store.SchemaVersion, "lifecycle": 1, "privateControl": 1})
	json.Unmarshal(raw, &build)
	child, err := h.launch(path, build, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer h.stopExact(child, 100*time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		if _, err = os.Stat(filepath.Join(dir, "private-ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("private fixture never started")
		}
		time.Sleep(time.Millisecond)
	}
	err = h.shutdown(child, "private-stop", 400*time.Millisecond)
	if err != nil {
		t.Fatal("private drain failed without business socket", err)
	}
	if managedexec.ProcessState(child.identity) == "running" {
		t.Fatal("drained child is still alive")
	}
}
func TestPrivateLifecycleBusyRejectionDoesNotSignalChild(t *testing.T) {
	dir, options := regressionOptions(t)
	path := filepath.Join(dir, "fixture-private-control")
	if err := os.Symlink(os.Args[0], path); err != nil {
		t.Fatal(err)
	}
	h := &host{options: options, ctx: context.Background(), device: "fixture-device"}
	var build domain.WorkerBuild
	json.Unmarshal([]byte(`{"buildId":"old","schema":15,"lifecycle":1,"privateControl":1}`), &build)
	child, err := h.launch(path, build, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer h.stopExact(child, 100*time.Millisecond)
	err = h.shutdown(child, "idle-stop", 400*time.Millisecond, Control{Replacement: domain.WorkerReplacement{Policy: "when-idle"}})
	if err != errDrainAdmission {
		t.Fatal("busy request was not rejected", err)
	}
	if managedexec.ProcessState(child.identity) != "running" {
		t.Fatal("busy child was signalled")
	}
	if err = h.shutdown(child, "explicit-stop", 400*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateLifecycleExpiredRequestDoesNotSignalChild(t *testing.T) {
	dir, options := regressionOptions(t)
	path := filepath.Join(dir, "fixture-private-control")
	if err := os.Symlink(os.Args[0], path); err != nil {
		t.Fatal(err)
	}
	h := &host{options: options, ctx: context.Background(), device: "fixture-device"}
	var build domain.WorkerBuild
	json.Unmarshal([]byte(`{"buildId":"old","schema":15,"lifecycle":1,"privateControl":1}`), &build)
	child, err := h.launch(path, build, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer h.stopExact(child, 100*time.Millisecond)
	err = h.shutdown(child, "expired-stop", 400*time.Millisecond, Control{ExpiresAt: time.Now().Add(-time.Second), Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}})
	if err == nil {
		t.Fatal("expired request succeeded")
	}
	if managedexec.ProcessState(child.identity) != "running" {
		t.Fatal("expired control signalled captured child", err)
	}
}

func TestPrivateLifecycleExpiredAttemptReturnsToWaitingWithoutLateExit(t *testing.T) {
	dir, options := regressionOptions(t)
	path := filepath.Join(dir, "fixture-private-control-slow")
	if err := os.Symlink(os.Args[0], path); err != nil {
		t.Fatal(err)
	}
	h := &host{options: options, ctx: context.Background(), device: "fixture-device"}
	child, err := h.launch(path, domain.WorkerBuild{BuildID: "old", Schema: store.SchemaVersion, Lifecycle: 1, PrivateControl: 1}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer h.stopExact(child, 100*time.Millisecond)
	err = h.shutdown(child, "late-idle", 600*time.Millisecond, Control{Replacement: domain.WorkerReplacement{Policy: "when-idle"}})
	if err != errDrainAdmission {
		t.Fatal("unaccepted timed-out attempt did not return to waiting", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err = os.Stat(filepath.Join(dir, "admission-rejected")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late handler was not rejected")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-child.done:
		t.Fatal("old writer exited after rejected attempt", child.err)
	default:
	}
	if _, err = ReadDrainIntent(dir, "late-idle"); err == nil {
		t.Fatal("rejected attempt left accepted intent")
	}
}
