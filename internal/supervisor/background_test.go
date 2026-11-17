package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
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
