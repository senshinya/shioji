package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestLodyMechanismRestartMustRetryTransientPreparationFailure(t *testing.T) {
	dir, o := regressionOptions(t)
	verify := o.Verify
	var selectedCalls atomic.Int32
	o.RetryMin, o.RetryMax = time.Millisecond, time.Millisecond
	o.Verify = func(ctx context.Context, build string) (string, domain.WorkerBuild, error) {
		if build == "" && selectedCalls.Add(1) == 2 {
			return "", domain.WorkerBuild{}, errors.New("transient launch-preparation failure")
		}
		return verify(ctx, build)
	}
	regressionHost(t, o, "ready")
	pid := fixtureReadyPID(t, dir, 0)
	p, e := os.FindProcess(pid)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Kill(); e != nil {
		t.Fatal(e)
	}
	fixtureReadyPID(t, dir, pid)
	if selectedCalls.Load() < 3 {
		t.Fatal("transient startup was not retried", selectedCalls.Load())
	}
}

func TestLodyMechanismSupervisorReflectsRepeatedMissingHealth(t *testing.T) {
	dir, o := regressionOptions(t)
	regressionHost(t, o, "ready")
	pid := fixtureReadyPID(t, dir, 0)
	marker := filepath.Join(dir, "hide-ready")
	if err := os.WriteFile(marker, []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitRegressionPhase(t, dir, "", "reconnecting")
	process, _ := os.FindProcess(pid)
	if err := process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("probe misses killed the worker", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	awaitRegressionPhase(t, dir, "", "ready")
}

func TestRetryableStartupDoesNotSpendOrdinaryCrashBudget(t *testing.T) {
	for _, code := range []string{"2", "3"} {
		t.Run(code, func(t *testing.T) {
			dir, o := regressionOptions(t)
			marker := filepath.Join(dir, "startup-exit")
			if err := os.WriteFile(marker, []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			verify := o.Verify
			var calls atomic.Int32
			o.RetryMin, o.RetryMax = time.Millisecond, time.Millisecond
			o.Verify = func(ctx context.Context, build string) (string, domain.WorkerBuild, error) {
				if calls.Add(1) == 12 {
					_ = os.Remove(marker)
				}
				return verify(ctx, build)
			}
			regressionHost(t, o, "ready")
			fixtureReadyPID(t, dir, 0)
			if calls.Load() < 12 {
				t.Fatal("retryable launch stopped before dependency recovered", calls.Load())
			}
		})
	}
}

func TestPermanentStartupDoesNotRetryAndTransientStartupStopsAtThreshold(t *testing.T) {
	for _, permanent := range []bool{true, false} {
		t.Run(map[bool]string{true: "permanent", false: "transient"}[permanent], func(t *testing.T) {
			dir, o := regressionOptions(t)
			var calls atomic.Int32
			o.RetryMin, o.RetryMax = time.Millisecond, time.Millisecond
			o.Verify = func(context.Context, string) (string, domain.WorkerBuild, error) {
				calls.Add(1)
				err := errors.New("invalid signature or temporarily inaccessible artifact")
				if permanent {
					err = PermanentStartup(err)
				}
				return "", domain.WorkerBuild{}, err
			}
			regressionHost(t, o, "blocked")
			status := awaitRegressionPhase(t, dir, "", "blocked")
			var health serviceHealth
			if err := readJSON(filepath.Join(dir, "supervisor", "health.json"), &health); err != nil {
				t.Fatal(err)
			}
			want := int32(10)
			if permanent {
				want = 1
			}
			if calls.Load() != want || health.CrashLoop == permanent {
				t.Fatal("wrong startup classification", calls.Load(), status)
			}
		})
	}
}
