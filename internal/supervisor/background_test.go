package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackgroundHostAcknowledgesAndDeduplicatesBeforeNativeCompletion(t *testing.T) {
	dir, o := regressionOptions(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	o.DisableBackground = func(context.Context) (domain.BackgroundService, error) {
		calls.Add(1)
		close(entered)
		<-release
		return domain.BackgroundService{Format: 1, State: "notRegistered"}, nil
	}
	defer close(release)
	regressionHost(t, o, "ready")
	instance, err := os.ReadFile(filepath.Join(dir, "started-old"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := Request{Action: "background-stop", ID: "disable-once", DeviceID: "fixture-device", Instance: string(instance)}
	state, err := BackgroundCall(ctx, dir, req)
	if err != nil || state.State != "disabling" {
		t.Fatal("native action blocked acceptance", state, err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	state, err = BackgroundCall(ctx, dir, req)
	if err != nil || state.State != "disabling" || calls.Load() != 1 {
		t.Fatal("native action replayed", state, err, calls.Load())
	}
	req.Instance = "foreign"
	if _, err = BackgroundCall(ctx, dir, req); err == nil {
		t.Fatal("foreign worker reused accepted operation")
	}
	req.Action = "background-status"
	req.Instance = ""
	if state, err = BackgroundCall(ctx, dir, req); err != nil || state.State != "disabling" {
		t.Fatal("read-only observer cannot query receipt", state, err)
	}
}

func TestBackgroundHostRestoresOnlyKnownUnstartedAction(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(map[bool]string{true: "not-started", false: "unknown"}[known], func(t *testing.T) {
			dir, o := regressionOptions(t)
			o.DisableBackground = func(context.Context) (domain.BackgroundService, error) {
				return domain.BackgroundService{Format: 1, State: "unavailable", NotStarted: known}, errors.New("isolated helper failure")
			}
			regressionHost(t, o, "ready")
			instance, err := os.ReadFile(filepath.Join(dir, "started-old"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req := Request{Action: "background-stop", ID: "disable-first", DeviceID: "fixture-device", Instance: string(instance)}
			if _, err = BackgroundCall(ctx, dir, req); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				state, e := BackgroundCall(ctx, dir, Request{Action: "background-status", ID: req.ID, DeviceID: req.DeviceID})
				if e != nil && state.State == "unavailable" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("no terminal receipt", state, e)
				}
				time.Sleep(10 * time.Millisecond)
			}
			raw, e := os.ReadFile(filepath.Join(dir, "cancel-background"))
			if known && (e != nil || string(raw) != req.ID) {
				t.Fatal("unstarted action did not cancel exact preparation", string(raw), e)
			}
			if !known && !os.IsNotExist(e) {
				t.Fatal("unknown action reopened admission", string(raw), e)
			}
			req.ID = "disable-second"
			_, err = BackgroundCall(ctx, dir, req)
			if known && err != nil || !known && err == nil {
				t.Fatal("incorrect follow-up admission", known, err)
			}
		})
	}
}

// A native unregister owns a stop intent even after its callback completes.
// Restarting a crashed child here could accept work that unregister then kills.
func TestBackgroundStopSuppressesCrashRestart(t *testing.T) {
	dir, o := regressionOptions(t)
	o.RetryMin, o.RetryMax = time.Millisecond, time.Millisecond
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	o.DisableBackground = func(context.Context) (domain.BackgroundService, error) {
		close(entered)
		<-release
		return domain.BackgroundService{Format: 1, State: "notRegistered"}, nil
	}
	defer finish()
	regressionHost(t, o, "ready")
	pid := fixtureReadyPID(t, dir, 0)
	instance, err := os.ReadFile(filepath.Join(dir, "started-old"))
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Action: "background-stop", ID: "stop-before-crash", DeviceID: "fixture-device", Instance: string(instance)}
	if _, err = BackgroundCall(context.Background(), dir, req); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("native stop did not start")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Kill(); err != nil {
		t.Fatal(err)
	}
	assertNotReplaced := func() {
		t.Helper()
		deadline := time.Now().Add(250 * time.Millisecond)
		for time.Now().Before(deadline) {
			got, err := os.ReadFile(filepath.Join(dir, "started-old"))
			if err != nil || string(got) != string(instance) {
				t.Fatal("stop intent allowed an ordinary replacement", string(got), err)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	assertNotReplaced()
	finish()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := BackgroundCall(context.Background(), dir, Request{Action: "background-status", ID: req.ID, DeviceID: req.DeviceID})
		if err == nil && state.State == "notRegistered" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native stop did not complete", state, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	assertNotReplaced()
}

func backgroundFixtureRequest(t *testing.T, dir, id string) Request {
	t.Helper()
	instance, err := os.ReadFile(filepath.Join(dir, "started-old"))
	if err != nil {
		t.Fatal(err)
	}
	return Request{Action: "background-stop", ID: id, DeviceID: "fixture-device", Instance: string(instance)}
}

func awaitBackgroundResult(t *testing.T, dir string, req Request) domain.BackgroundService {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	for {
		state, err := BackgroundCall(ctx, dir, Request{Action: "background-status", ID: req.ID, DeviceID: req.DeviceID})
		if state.State == "unavailable" && err != nil {
			return state
		}
		select {
		case <-ctx.Done():
			t.Fatal("background restoration did not settle", state, err)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Exhaust the actual native-action context, then require the captured worker's
// cancel RPC to run. Reusing that parent context can never send this request.
func TestBackgroundCancelAfterNativeDeadlineHasFreshBudget(t *testing.T) {
	dir, o := regressionOptions(t)
	o.DisableBackground = func(ctx context.Context) (domain.BackgroundService, error) {
		<-ctx.Done()
		return domain.BackgroundService{Format: 1, State: "unavailable", NotStarted: true}, ctx.Err()
	}
	regressionHost(t, o, "ready")
	req := backgroundFixtureRequest(t, dir, "expired-native-budget")
	if _, err := BackgroundCall(context.Background(), dir, req); err != nil {
		t.Fatal(err)
	}
	state := awaitBackgroundResult(t, dir, req)
	raw, err := os.ReadFile(filepath.Join(dir, "cancel-background"))
	if !state.NotStarted || err != nil || string(raw) != req.ID {
		t.Fatal("expired native budget stranded the worker preparation", state, string(raw), err)
	}
}

func TestBackgroundCancelRetriesUntilExactWorkerRestores(t *testing.T) {
	dir, o := regressionOptions(t)
	var calls atomic.Int32
	o.DisableBackground = func(context.Context) (domain.BackgroundService, error) {
		calls.Add(1)
		return domain.BackgroundService{Format: 1, State: "unavailable", NotStarted: true}, errors.New("native action never started")
	}
	if err := os.WriteFile(filepath.Join(dir, "reject-background-cancel"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	regressionHost(t, o, "ready")
	req := backgroundFixtureRequest(t, dir, "retry-exact-undo")
	if _, err := BackgroundCall(context.Background(), dir, req); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "cancel-background-attempt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("exact-worker undo was not attempted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	state, err := BackgroundCall(context.Background(), dir, Request{Action: "background-status", ID: req.ID, DeviceID: req.DeviceID})
	if err != nil || state.State != "disabling" {
		t.Fatal("failed undo was published as terminal", state, err)
	}
	if _, err = BackgroundCall(context.Background(), dir, req); err != nil || calls.Load() != 1 {
		t.Fatal("pending query replayed native action", calls.Load(), err)
	}
	if err = os.Remove(filepath.Join(dir, "reject-background-cancel")); err != nil {
		t.Fatal(err)
	}
	awaitBackgroundResult(t, dir, req)
	raw, err := os.ReadFile(filepath.Join(dir, "cancel-background"))
	if err != nil || string(raw) != req.ID || calls.Load() != 1 {
		t.Fatal("exact restoration did not retry independently", string(raw), calls.Load(), err)
	}
}

func TestBackgroundKnownUnstartedChildExitAllowsRecoveryWithoutStaleUndo(t *testing.T) {
	dir, o := regressionOptions(t)
	o.RetryMin, o.RetryMax = time.Millisecond, time.Millisecond
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	o.DisableBackground = func(context.Context) (domain.BackgroundService, error) {
		close(entered)
		<-release
		return domain.BackgroundService{Format: 1, State: "unavailable", NotStarted: true}, errors.New("unstarted action")
	}
	regressionHost(t, o, "ready")
	pid := fixtureReadyPID(t, dir, 0)
	req := backgroundFixtureRequest(t, dir, "unstarted-old-exit")
	if _, err := BackgroundCall(context.Background(), dir, req); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("native preflight did not enter")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Kill(); err != nil {
		t.Fatal(err)
	}
	finish()
	awaitBackgroundResult(t, dir, req)
	fixtureReadyPID(t, dir, pid)
	if raw, err := os.ReadFile(filepath.Join(dir, "cancel-background")); !os.IsNotExist(err) {
		t.Fatal("undo reached a replacement instead of the captured child", string(raw), err)
	}
}
