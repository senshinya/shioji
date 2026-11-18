package worker_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/worker"
)

func TestSchedulesBelongToWorkspaceWorker(t *testing.T) {
	_, a, _ := startServer(t)
	_, b, _ := startServer(t)
	var workspace domain.Workspace
	call(t, b, "workspace.create", protocol.WorkspaceInput{Name: "B", Folders: []string{t.TempDir()}}, &workspace)
	var ready domain.Readiness
	call(t, b, "readiness", nil, &ready)
	input := map[string]any{
		"id": "scheduled-test", "workspaceId": workspace.ID, "name": "摘要", "text": "Summarize changes",
		"enabled": true, "rule": map[string]any{"kind": "daily", "time": "09:00"},
		"config": domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"},
	}
	if err := a.Call(context.Background(), "schedule.save", input, nil); err == nil {
		t.Fatal("A accepted a schedule for B's workspace")
	}
	var saved struct {
		ID, DeviceID, WorkspaceID string
		Revision                  int64
	}
	call(t, b, "schedule.save", input, &saved)
	if saved.ID != "scheduled-test" || saved.DeviceID != workspace.DeviceID || saved.WorkspaceID != workspace.ID || saved.Revision != 1 {
		t.Fatal("invalid schedule ownership", saved)
	}
	var list []struct {
		ID, Text string
	}
	call(t, a, "schedule.list", nil, &list)
	if len(list) != 0 {
		t.Fatal("B's tasks leaked into A's worker", list)
	}
	call(t, b, "schedule.list", nil, &list)
	if len(list) != 1 || list[0].ID != saved.ID || list[0].Text != "" {
		t.Fatal("invalid list projection", list)
	}
	input["revision"] = saved.Revision
	input["name"] = "新摘要"
	call(t, b, "schedule.save", input, &saved)
	if err := b.Call(context.Background(), "schedule.save", input, nil); err == nil {
		t.Fatal("stale editor overwrote the accepted revision")
	}
}

func TestScheduleManualRunSurvivesClientDisconnect(t *testing.T) {
	server, c, _ := startServer(t)
	var w domain.Workspace
	call(t, c, "workspace.create", protocol.WorkspaceInput{Name: "Schedule", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	task := domain.Schedule{ID: domain.NewID(), WorkspaceID: w.ID, Name: "摘要", Text: "SHIOJI_FAST",
		Enabled: true, Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"},
		Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}
	call(t, c, "schedule.save", task, &task)
	next := task.NextAt
	q := protocol.ScheduleCommand{ID: task.ID, Revision: task.Revision, RunID: domain.NewID()}
	var run domain.ScheduleRun
	call(t, c, "schedule.run", q, &run)
	c.Close()
	second := connect(t, server.Directory())
	call(t, second, "schedule.run", q, &run)
	var detail domain.ScheduleDetail
	wait(t, func() bool {
		call(t, second, "schedule.get", q, &detail)
		return len(detail.Runs) == 1 && detail.Runs[0].State == domain.ScheduleCompleted
	})
	result := detail.Runs[0]
	if result.ID != q.RunID || result.SessionID == "" || result.SessionLink != "open" || !detail.Schedule.NextAt.Equal(next) {
		t.Fatalf("invalid run result: %+v", detail)
	}
	var session domain.SessionDetail
	call(t, second, "session", protocol.SessionQuery{ID: result.SessionID}, &session)
	var archived domain.Session
	call(t, second, "session.archive", protocol.SessionChangeInput{SessionID: result.SessionID, Revision: session.Session.Revision, Archived: true}, &archived)
	call(t, second, "schedule.get", q, &detail)
	if detail.Runs[0].SessionLink != "archived" {
		t.Fatal(detail.Runs[0])
	}
	call(t, second, "session.delete", protocol.ArchiveDeleteInput{SessionID: result.SessionID, Revision: archived.Revision}, nil)
	call(t, second, "schedule.get", q, &detail)
	if detail.Runs[0].State != domain.ScheduleCompleted || detail.Runs[0].SessionLink != "deleted" {
		t.Fatal("deleting the session lost its run history", detail.Runs[0])
	}
}

func TestScheduleStartupDispatchesDueRunWithoutClient(t *testing.T) {
	_, c, provider := startServer(t)
	var ready domain.Readiness
	call(t, c, "readiness", nil, &ready)
	dir, err := os.MkdirTemp("", "schedule-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	w, err := db.CreateWorkspace("Clock", []string{folder})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	now := time.Now()
	task, err := db.SaveSchedule(domain.Schedule{ID: domain.NewID(), WorkspaceID: w.ID, Name: "定时运行", Text: "SHIOJI_FAST",
		Enabled: true, Rule: domain.ScheduleRule{Kind: "interval", Minutes: 60},
		Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, now.Add(-61*time.Minute), time.Local)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	server, err := worker.New(context.Background(), worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: provider.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	// Observe the result through a read-only handle while no GUI is connected.
	reader, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "state.sqlite")+"?mode=ro&_pragma=busy_timeout%3D2500")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var settled int
		if err := reader.QueryRow("SELECT count(*) FROM schedule_runs WHERE schedule_id=? AND state='completed'", task.ID).Scan(&settled); err != nil {
			t.Fatal(err)
		}
		if settled == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("autonomous dispatch did not settle")
		}
		time.Sleep(100 * time.Millisecond)
	}
	go server.Serve()
	client := connect(t, dir)
	var detail domain.ScheduleDetail
	call(t, client, "schedule.get", protocol.ScheduleCommand{ID: task.ID}, &detail)
	if len(detail.Runs) != 1 || detail.Runs[0].Manual || !detail.Runs[0].At.Equal(task.NextAt) {
		t.Fatal("unexpected automatic run", detail.Runs)
	}
}
