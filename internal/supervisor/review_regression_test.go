package supervisor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"testing"
	"time"
)

func seedFixtureState(t *testing.T, dir string) {
	t.Helper()
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec("UPDATE metadata SET device_id='fixture-device' WHERE id=1")
	raw.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func regressionOptions(t *testing.T) (string, Options) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sh-review-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	seedFixtureState(t, dir)
	for _, build := range []string{"old", "next"} {
		if err = os.Symlink(os.Args[0], filepath.Join(dir, "fixture-"+build)); err != nil {
			t.Fatal(err)
		}
	}
	o := Options{Directory: dir, InitialPath: filepath.Join(dir, "fixture-old"), Arguments: []string{"--state-dir", dir}, ReadyTimeout: 500 * time.Millisecond, DrainTimeout: time.Second, StopGrace: 100 * time.Millisecond, KillWait: time.Second}
	o.Verify = func(ctx context.Context, build string) (string, domain.WorkerBuild, error) {
		if build == "" {
			if _, e := os.Stat(filepath.Join(dir, "fail-start")); e == nil {
				return "", domain.WorkerBuild{}, errors.New("temporary launch failure")
			}
			build = "old"
			var selected string
			if readJSON(filepath.Join(dir, "selected.json"), &selected) == nil {
				build = selected
			}
		} else if _, e := os.Stat(filepath.Join(dir, "hold-verification")); e == nil {
			_, bounded := ctx.Deadline()
			os.WriteFile(filepath.Join(dir, "verification-entered"), []byte(fmt.Sprint(bounded)), 0600)
			for {
				if _, e = os.Stat(filepath.Join(dir, "hold-verification")); os.IsNotExist(e) {
					break
				}
				select {
				case <-ctx.Done():
					return "", domain.WorkerBuild{}, ctx.Err()
				case <-time.After(5 * time.Millisecond):
				}
			}
		}
		schema := store.SchemaVersion
		if data, e := os.ReadFile(filepath.Join(dir, "schema-"+build)); e == nil {
			fmt.Sscan(string(data), &schema)
		}
		return filepath.Join(dir, "fixture-"+build), domain.WorkerBuild{BuildID: build, Schema: schema, Lifecycle: 1, SafeUpdate: true}, nil
	}
	o.Select = func(build string) error { return atomicJSON(filepath.Join(dir, "selected.json"), build) }
	return dir, o
}

func regressionHost(t *testing.T, o Options, phase string) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, o); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("regression host did not stop")
		}
	})
	awaitRegressionPhase(t, o.Directory, "", phase)
	return cancel, done
}

func awaitRegressionPhase(t *testing.T, dir, id, phase string) Operation {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		op, err := Call(ctx, dir, Request{Action: "status", ID: id})
		cancel()
		if err == nil && op.Phase == phase {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("phase %s not observed for %s", phase, id)
	return Operation{}
}

func fixtureReadyPID(t *testing.T, dir string, previous int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		client, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
		}, "")
		var ready Ready
		if err == nil {
			err = client.Call(ctx, "worker.local-ready", Control{}, &ready)
			client.Close()
		}
		cancel()
		if err == nil && ready.PID > 1 && ready.PID != previous {
			var health serviceHealth
			if readJSON(filepath.Join(dir, "supervisor", "health.json"), &health) == nil && health.Phase == "ready" {
				return ready.PID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := Call(context.Background(), dir, Request{Action: "status"})
	health, _ := os.ReadFile(filepath.Join(dir, "supervisor", "health.json"))
	t.Fatal("fixture did not restart", previous, current, string(health))
	return 0
}

func TestCrashCircuitResetsWithSupervisorAndDoesNotRewriteHistoricalReceipt(t *testing.T) {
	dir, o := regressionOptions(t)
	o.RetryMin, o.RetryMax = time.Millisecond, time.Millisecond
	cancel, done := regressionHost(t, o, "ready")
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "completed", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitRegressionPhase(t, dir, "completed", "applied")
	previous := 0
	for attempt := range 10 {
		pid := fixtureReadyPID(t, dir, previous)
		t.Log("crash", attempt, pid)
		previous = pid
		p, err := os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Kill(); err != nil {
			t.Fatal(err)
		}
	}
	awaitRegressionPhase(t, dir, "", "blocked")
	if op, err := Call(context.Background(), dir, Request{Action: "status", ID: "completed"}); err != nil || op.Phase != "applied" {
		t.Fatal("service health changed an old receipt", op, err)
	}
	cancel()
	<-done
	regressionHost(t, o, "applied")
	if op, err := Call(context.Background(), dir, Request{Action: "status", ID: "completed"}); err != nil || op.Phase != "applied" {
		t.Fatal("supervisor restart rewrote the historical receipt", op, err)
	}
}

func TestFirstStartupFailureCanBeColdRepairedUsingDurableIdentity(t *testing.T) {
	dir, o := regressionOptions(t)
	os.WriteFile(filepath.Join(dir, "fail-start"), []byte("yes"), 0600)
	regressionHost(t, o, "blocked")
	os.Remove(filepath.Join(dir, "fail-start"))
	op, err := Call(context.Background(), dir, Request{Action: "repair", DeviceID: "fixture-device", Input: domain.WorkerReplacement{ID: "startup-repair", BuildID: "next", Policy: "immediate", Explicit: true}})
	if err != nil {
		t.Fatal("startup failure lost its authoritative identity and cannot be retried", op, err)
	}
	awaitRegressionPhase(t, dir, "startup-repair", "applied")
}

func TestColdRepairRejectsSchemaChangeWithNoInMemoryChild(t *testing.T) {
	dir, o := regressionOptions(t)
	j := journal{filepath.Join(dir, "supervisor")}
	prior := Operation{Input: domain.WorkerReplacement{ID: "failed-update", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}, DeviceID: "fixture-device", Phase: "blocked"}
	if err := j.save(prior); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "schema-next"), []byte(fmt.Sprint(store.SchemaVersion+1)), 0600)
	regressionHost(t, o, "blocked")
	_, err := Call(context.Background(), dir, Request{Action: "repair", DeviceID: "fixture-device", Input: domain.WorkerReplacement{ID: "incompatible-repair", BuildID: "next", Policy: "immediate", Explicit: true}})
	if err == nil {
		awaitRegressionPhase(t, dir, "incompatible-repair", "blocked")
	}
	if _, err = os.Stat(filepath.Join(dir, "started-next")); !os.IsNotExist(err) {
		t.Fatal("incompatible candidate touched real state", err)
	}
}

func TestRepairPreparationKeepsControlResponsiveAndCanBeCancelled(t *testing.T) {
	dir, o := regressionOptions(t)
	if err := (journal{filepath.Join(dir, "supervisor")}).save(Operation{Input: domain.WorkerReplacement{ID: "blocked-update", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}, DeviceID: "fixture-device", Phase: "blocked"}); err != nil {
		t.Fatal(err)
	}
	regressionHost(t, o, "blocked")
	os.WriteFile(filepath.Join(dir, "hold-verification"), []byte("yes"), 0600)
	defer os.Remove(filepath.Join(dir, "hold-verification"))
	call, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go Call(call, dir, Request{Action: "repair", DeviceID: "fixture-device", Input: domain.WorkerReplacement{ID: "preparing-repair", BuildID: "next", Policy: "immediate", Explicit: true}})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "verification-entered")); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	if _, err := Call(ctx, dir, Request{Action: "status"}); err != nil {
		t.Fatal("repair preparation monopolized the independent control channel", err)
	}
	bounded, _ := os.ReadFile(filepath.Join(dir, "verification-entered"))
	if string(bounded) != "true" {
		t.Fatal("repair verification lacks a bounded context")
	}
	if _, err := Call(ctx, dir, Request{Action: "cancel", ID: "preparing-repair", DeviceID: "fixture-device"}); err != nil {
		t.Fatal("preparation cannot be cancelled", err)
	}
	awaitRegressionPhase(t, dir, "preparing-repair", "blocked")
}

func TestHistoricalAppliedReceiptRemainsTerminalDuringDifferentUpdate(t *testing.T) {
	dir, o := regressionOptions(t)
	regressionHost(t, o, "ready")
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "completed", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitRegressionPhase(t, dir, "completed", "applied")
	os.WriteFile(filepath.Join(dir, "hold-verification"), []byte("yes"), 0600)
	defer os.Remove(filepath.Join(dir, "hold-verification"))
	_, err = Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "new-update", ExpectedBuildID: "next", BuildID: "old", Policy: "when-idle", Explicit: true}})
	if err != nil {
		t.Fatal(err)
	}
	op, err := Call(context.Background(), dir, Request{Action: "status", ID: "completed"})
	if err != nil || op.Phase != "applied" {
		t.Fatal("unrelated update reopened the old terminal receipt", op.Phase, err)
	}
}

func TestHostPublishesCompleteChildIdentity(t *testing.T) {
	dir, o := regressionOptions(t)
	h := &host{ctx: context.Background(), options: o}
	type launched struct {
		c   *child
		err error
	}
	done := make(chan launched, 1)
	go func() {
		c, err := h.launch(filepath.Join(dir, "fixture-old"), domain.WorkerBuild{BuildID: "old", Schema: store.SchemaVersion, Lifecycle: 1}, false, "")
		done <- launched{c, err}
	}()
	partial := false
	var result launched
loop:
	for {
		select {
		case result = <-done:
			break loop
		default:
			h.mu.Lock()
			if h.child != nil && h.child.identity.Identity.PID == 0 {
				partial = true
			}
			h.mu.Unlock()
		}
	}
	if result.c != nil {
		defer h.stopExact(result.c, 100*time.Millisecond)
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	if partial {
		t.Fatal("control observed a child before its process identity was initialized")
	}
}
