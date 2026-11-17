package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestCommittedResumeLateAckKeepsAppliedSelection(t *testing.T) {
	dir, o := regressionOptions(t)
	next := filepath.Join(dir, "fixture-next-holdresume")
	if err := os.Symlink(os.Args[0], next); err != nil {
		t.Fatal(err)
	}
	verify := o.Verify
	o.Verify = func(ctx context.Context, build string) (string, domain.WorkerBuild, error) {
		path, b, err := verify(ctx, build)
		if b.BuildID == "next" {
			path = next
		}
		return path, b, err
	}
	regressionHost(t, o, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	in := domain.WorkerReplacement{ID: "delayed-resume", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	if _, err := Call(ctx, dir, Request{Action: "start", Input: in}); err != nil {
		t.Fatal(err)
	}
	var result Operation
	for {
		var err error
		result, err = Call(ctx, dir, Request{Action: "status", ID: in.ID})
		if err != nil {
			t.Fatal(err)
		}
		if result.Terminal() {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	instance, err := os.ReadFile(filepath.Join(dir, "started-next"))
	if err != nil {
		t.Fatal(err)
	}
	check := &host{options: Options{Directory: dir}}
	var ready Ready
	captured := &child{instance: string(instance), build: domain.WorkerBuild{BuildID: "next"}}
	if err = check.workerCall(ctx, captured, "worker.local-ready", Control{Instance: string(instance)}, &ready); err != nil {
		t.Fatal(err)
	}
	var selected string
	if err = readJSON(filepath.Join(dir, "selected.json"), &selected); err != nil {
		t.Fatal(err)
	}
	_, accepted := os.Stat(filepath.Join(dir, "accepted-next"))
	_, retryErr := Call(ctx, dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "after-delayed-resume", ExpectedBuildID: "next", BuildID: "old", Policy: "when-idle"}})
	t.Logf("selected=%s phase=%s liveCandidatePaused=%v accepted=%v retryError=%v cause=%s", selected, result.Phase, ready.Paused, accepted == nil, retryErr, result.Error)
	if result.Phase != "applied" || ready.Paused || accepted != nil || retryErr != nil {
		t.Error("already applied and accepting candidate sealed as blocked solely because resume ACK was late")
	}
}

func TestCommittedResumeUncertainReceiptRecoversWithoutBlocking(t *testing.T) {
	dir, o := regressionOptions(t)
	regressionHost(t, o, "ready")
	if err := os.WriteFile(filepath.Join(dir, "reject-resume"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	in := domain.WorkerReplacement{ID: "uncertain-resume", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	if _, err := Call(ctx, dir, Request{Action: "start", Input: in}); err != nil {
		t.Fatal(err)
	}
	awaitRegressionPhase(t, dir, "", "reconnecting")
	persisted, err := (journal{filepath.Join(dir, "supervisor")}).load(in.ID)
	if err != nil || persisted.Phase != "applied" {
		t.Fatal("committed receipt was overwritten", persisted, err)
	}
	if err = os.Remove(filepath.Join(dir, "reject-resume")); err != nil {
		t.Fatal(err)
	}
	awaitRegressionPhase(t, dir, in.ID, "applied")
}

func TestCommittedCandidateExitUsesSelectedBuildRecovery(t *testing.T) {
	dir, o := regressionOptions(t)
	o.RetryMin = time.Millisecond
	o.RetryMax = 2 * time.Millisecond
	regressionHost(t, o, "ready")
	if err := os.WriteFile(filepath.Join(dir, "reject-resume"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	in := domain.WorkerReplacement{ID: "committed-exit", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	if _, err := Call(ctx, dir, Request{Action: "start", Input: in}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "resume-rejected")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resume not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pid := fixtureReadyPID(t, dir, 0)
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err = process.Kill(); err != nil {
		t.Fatal(err)
	}
	nextPID := fixtureReadyPID(t, dir, pid)
	awaitRegressionPhase(t, dir, in.ID, "applied")
	persisted, err := (journal{filepath.Join(dir, "supervisor")}).load(in.ID)
	var selected string
	selectErr := readJSON(filepath.Join(dir, "selected.json"), &selected)
	if err != nil || selectErr != nil || persisted.Phase != "applied" || selected != "next" || nextPID == pid {
		t.Fatal("committed crash lost selected build", persisted, selected, nextPID, err, selectErr)
	}
}
