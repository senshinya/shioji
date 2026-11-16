package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/supervisor"
	"sync/atomic"
	"testing"
	"time"
)

type countingCandidateAdapter struct {
	calls  *atomic.Int32
	titles *atomic.Int32
}

func (a countingCandidateAdapter) Descriptor() domain.HarnessDescriptor {
	return domain.HarnessDescriptor{ID: "candidate", Name: "candidate"}
}
func (a countingCandidateAdapter) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	a.calls.Add(1)
	base, err := (memoryHarness{id: "candidate"}).Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &countingCandidateRuntime{Runtime: base, titles: a.titles}, nil
}
func TestPausedCandidateLocalReadyDoesNotStartHarness(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sw-paused-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	var calls atomic.Int32
	s, err := New(context.Background(), Config{Directory: dir, StartPaused: true, ServiceInstance: "candidate", ServiceOperation: "update", Harnesses: []harness.Adapter{countingCandidateAdapter{calls: &calls}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, _ := json.Marshal(supervisor.Control{Instance: "candidate"})
	out, err := s.handle("worker.local-ready", raw)
	if err != nil || !out.(supervisor.Ready).Paused {
		t.Fatal("candidate not locally ready", out, err)
	}
	if calls.Load() != 0 {
		t.Fatal("paused candidate started a harness before commit", calls.Load())
	}
	if err = s.updateAdmission(); err == nil {
		t.Fatal("uncommitted candidate accepted work")
	}
	if err = supervisor.SaveDrainIntent(dir, supervisor.DrainIntent{ID: "update", Instance: "old", DeviceID: s.store.DeviceID()}); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(supervisor.Control{Instance: "candidate", OperationID: "update"})
	if _, err = s.handle("worker.resume", raw); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("committed candidate did not start its harness")
	}
}

type countingCandidateRuntime struct {
	harness.Runtime
	titles *atomic.Int32
}

func (r *countingCandidateRuntime) GenerateTitle(context.Context, harness.TitleInput) (string, error) {
	if r.titles != nil {
		r.titles.Add(1)
	}
	return "标题", nil
}
func TestPausedCandidateKeepsTitleConsumerForCommittedWork(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sw-title-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	var calls, titles atomic.Int32
	s, err := New(context.Background(), Config{Directory: dir, StartPaused: true, ServiceInstance: "candidate", ServiceOperation: "update", Harnesses: []harness.Adapter{countingCandidateAdapter{calls: &calls, titles: &titles}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	time.Sleep(30 * time.Millisecond)
	if err = supervisor.SaveDrainIntent(dir, supervisor.DrainIntent{ID: "update", Instance: "old", DeviceID: s.store.DeviceID()}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(supervisor.Control{Instance: "candidate", OperationID: "update"})
	if _, err = s.handle("worker.resume", raw); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.store.CreateWorkspace("title", []string{folder})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.submit(domain.Submission{OperationID: "title", WorkspaceID: w.ID, Text: "make a title", Config: domain.RunConfig{Model: "candidate-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	deadline = time.Now().Add(time.Second)
	for titles.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if titles.Load() == 0 {
		t.Fatal("paused candidate permanently lost title consumer")
	}
}
