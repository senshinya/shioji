package worker

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"testing"
	"time"
)

type maintenanceCrashState struct {
	DeviceID, SessionID, Log, Operation string
	PID                                 int
}

// A separate OS worker owns the DB and tool. SIGKILL bypasses all Go cleanup;
// the next worker must observe the durable authorization without replaying it.
func TestWorkerMaintenanceExternalCrashRecovery(t *testing.T) {
	if root := os.Getenv("SHIOJI_MAINTENANCE_CHILD"); root != "" {
		s, err := New(context.Background(), Config{Directory: root, Harnesses: []harness.Adapter{maintenanceHarness{}}})
		if err != nil {
			t.Fatal(err)
		}
		enableMaintenance(s)
		if os.Getenv("SHIOJI_MAINTENANCE_SEED") == "1" {
			folder := filepath.Join(root, "project")
			os.MkdirAll(folder, 0700)
			folder, err = filepath.EvalSymlinks(folder)
			if err != nil {
				t.Fatal(err)
			}
			w, err := s.store.CreateWorkspace("Recovery fixture", []string{folder})
			if err != nil {
				t.Fatal(err)
			}
			d := submitIdleFixture(t, s, w, "seed", "")
			_, log := maintenanceTool(t, s, d.Session.ID)
			op, err := s.applyWorkerMaintenance(maintenanceInput(t, s, "crash-operation"))
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if b, _ := os.ReadFile(log); string(b) == "T" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			scope, _ := s.executions.Existing(d.Session.ID)
			obs, err := scope.InspectMaintenance()
			if err != nil {
				t.Fatal(err)
			}
			pid := 0
			for _, p := range obs.Processes {
				if !p.Infrastructure {
					pid = p.Identity.PID
				}
			}
			if pid == 0 {
				t.Fatal("fixture process disappeared")
			}
			data, _ := json.Marshal(maintenanceCrashState{s.store.DeviceID(), d.Session.ID, log, op.ID, pid})
			if err = os.WriteFile(filepath.Join(root, "ready.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			os.WriteFile(filepath.Join(root, "recovered"), []byte("ready"), 0600)
		}
		if err = s.Serve(); err != nil {
			t.Fatal(err)
		}
		return
	}
	root, err := os.MkdirTemp("", "sh-maint-crash-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	var child *exec.Cmd
	launch := func(seed bool) {
		t.Helper()
		child = exec.Command(os.Args[0], "-test.run=^TestWorkerMaintenanceExternalCrashRecovery$")
		child.Env = append(os.Environ(), "SHIOJI_MAINTENANCE_CHILD="+root, "TMPDIR="+root, "SHIOJI_MAINTENANCE_SEED=0")
		if seed {
			child.Env = append(child.Env, "SHIOJI_MAINTENANCE_SEED=1")
		}
		child.Stdout = os.Stderr
		child.Stderr = os.Stderr
		if err = child.Start(); err != nil {
			t.Fatal(err)
		}
	}
	stop := func() {
		if child != nil {
			child.Process.Kill()
			child.Wait()
			child = nil
		}
	}
	defer stop()
	launch(true)
	awaitFile := func(name string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if _, err = os.Stat(filepath.Join(root, name)); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("helper not ready", name)
	}
	awaitFile("ready.json")
	var info maintenanceCrashState
	b, _ := os.ReadFile(filepath.Join(root, "ready.json"))
	if json.Unmarshal(b, &info) != nil {
		t.Fatal("invalid fixture metadata")
	}
	defer func() {
		stop()
		manager, e := managedexec.New(filepath.Join(root, "executions"), os.Args[0])
		if e == nil {
			if scope, e := manager.Existing(info.SessionID); e == nil && scope != nil {
				scope.ForceManualStop(info.Operation, "test-cleanup")
				scope.Stop(context.Background())
			}
		}
	}()
	stop()
	if err = unix.Kill(info.PID, 0); err != nil {
		t.Fatal("worker crash killed the unforced tool", err)
	}
	launch(false)
	awaitFile("recovered")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dial := func(context.Context) (net.Conn, error) { return net.Dial("unix", filepath.Join(root, "worker.sock")) }
	a, err := protocol.Dial(ctx, dial, info.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	bclient, err := protocol.Dial(ctx, dial, info.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer bclient.Close()
	query := protocol.WorkerMaintenanceQuery{DeviceID: info.DeviceID, ID: info.Operation}
	var first, second domain.WorkerMaintenance
	if err = a.Call(ctx, "worker.maintenance.get", query, &first); err != nil {
		t.Fatal(err)
	}
	if err = bclient.Call(ctx, "worker.maintenance.get", query, &second); err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Revision != second.Revision || first.Phase != domain.WorkerMaintenanceNeedsForce {
		t.Fatal("clients disagree after crash", first, second)
	}
	signals, _ := os.ReadFile(info.Log)
	if string(signals) != "T" {
		t.Fatal("recovery replayed the stop", string(signals))
	}
	if err = unix.Kill(info.PID, 0); err != nil {
		t.Fatal("recovery force-killed the tool", err)
	}
	cancelInput := domain.WorkerMaintenanceInput{ID: first.ID, ActionID: "cancel-from-second-client", Action: "cancel", DeviceID: info.DeviceID, Revision: first.Revision}
	if err = bclient.Call(ctx, "worker.maintenance.cancel", cancelInput, &second); err != nil {
		t.Fatal(err)
	}
	if err = a.Call(ctx, "worker.maintenance.get", query, &first); err != nil || first.Phase != domain.WorkerMaintenanceCancelled || first.Revision != second.Revision {
		t.Fatal("cancel did not converge", first, second, err)
	}
	stop()
	os.Remove(filepath.Join(root, "recovered"))
	launch(false)
	awaitFile("recovered")
	// Await the new socket by doing an authenticated handshake, rather than
	// trusting a leftover ready marker or the existence of a Unix socket.
	var recovered *protocol.Client
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		recovered, err = protocol.Dial(ctx, dial, info.DeviceID)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err = unix.Kill(info.PID, 0); err != nil {
		t.Fatal("cancelled generation lost its no-force guard on restart", err)
	}
	if err = recovered.Call(ctx, "worker.maintenance.get", query, &first); err != nil || first.Phase != domain.WorkerMaintenanceCancelled {
		t.Fatal(first, err)
	}
	signals, _ = os.ReadFile(info.Log)
	if string(signals) != "T" {
		t.Fatal("cancelled recovery resent a signal", string(signals))
	}
}
