package ui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/protocol"
	"shioji/internal/testutil"
	"shioji/internal/worker"
)

func TestScheduleNativeWorkerFlow(t *testing.T) {
	dir, err := os.MkdirTemp("", "schedule-ui-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := testutil.NewProvider(t)
	server, err := worker.New(ctx, worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: provider.Home}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go server.Serve()
	var dialer net.Dialer
	client, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var w domain.Workspace
	if err = client.Call(ctx, "workspace.create", protocol.WorkspaceInput{Name: "定时工作区", Folders: []string{t.TempDir()}}, &w); err != nil {
		t.Fatal(err)
	}

	m := app.NewModel()
	m.FirstRun = false
	m.Drafts["home"] = "保留普通草稿"
	controller := app.NewController(dir)
	done := make(chan struct{})
	go func() { controller.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	v := New(m, controller, "")
	defer v.CloseTransient()
	ui := n.NewTester(v.View, 1280, 800)
	waitNative(t, ui, func() bool { return m.Connected && len(m.Snapshot.Workspaces) == 1 && len(m.Readiness.Models) != 0 })
	if err = ui.Click("定时任务"); err != nil {
		t.Fatal(err)
	}
	if err = ui.Click("新建任务"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if v.schedules.draft == nil {
		t.Fatal("editor did not open")
	}
	name, ok := ui.Find("任务名称")
	if !ok {
		t.Fatal("missing task name field")
	}
	ui.ClickAt(name.X+8, name.Y+name.H+12)
	ui.Type("原生测试")
	if err = ui.Click("每次运行的提示词"); err != nil {
		t.Fatal(err)
	}
	ui.Type("SHIOJI_FAST")
	if draft := v.schedules.draft.task; draft.Name != "原生测试" || draft.Text != "SHIOJI_FAST" {
		t.Fatalf("native fields did not retain the input: name=%q text=%q", draft.Name, draft.Text)
	}
	if err = ui.Click("保存任务"); err != nil {
		t.Fatal(err)
	}
	waitNative(t, ui, func() bool { return v.schedules.draft == nil && v.schedules.detail.Schedule.Name == "原生测试" })
	created := v.schedules.detail.Schedule
	if created.DeviceID != w.DeviceID || created.WorkspaceID != w.ID || m.Drafts["home"] != "保留普通草稿" {
		t.Fatal("created task went to the wrong worker or affected another draft", created)
	}
	if _, ok := ui.Find("关闭消息"); ok {
		if err = ui.Click("关闭消息"); err != nil {
			t.Fatal(err)
		}
		ui.Advance(time.Second)
	}
	saveNativeSkillImage(t, ui, "schedules-worker-created.png")
	if err = ui.Click("立即运行"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for len(v.schedules.detail.Runs) != 1 || v.schedules.detail.Runs[0].State != domain.ScheduleCompleted {
		ui.Frame()
		ui.Advance(20 * time.Millisecond)
		if time.Now().After(deadline) {
			var workerDetail domain.ScheduleDetail
			err := client.Call(ctx, "schedule.get", protocol.ScheduleCommand{ID: created.ID}, &workerDetail)
			t.Fatalf("immediate run did not settle: UI=%v worker=%v message=%s busy=%t receipts=%v worker error=%v", v.schedules.detail.Runs, workerDetail.Runs, v.schedules.message, v.schedules.busy, v.schedules.unconfirmedRuns, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = ui.Click("删除任务"); err != nil {
		t.Fatal(err)
	}
	waitNative(t, ui, func() bool { return v.schedules.confirmDelete })
	// A second client edits the task while the confirmation is visible.
	created.Name = "另一端更新"
	var updated domain.Schedule
	if err = client.Call(ctx, "schedule.save", created, &updated); err != nil {
		t.Fatal(err)
	}
	v.schedules.refreshAt = time.Time{}
	ui.Frame()
	waitNative(t, ui, func() bool {
		got, ok := v.schedules.task(scheduleIdentity(updated))
		return ok && got.Revision == updated.Revision
	})
	ui.Advance(400 * time.Millisecond)
	if err = ui.Click("确认删除任务"); err != nil {
		t.Fatal(err)
	}
	waitNative(t, ui, func() bool { return !v.schedules.busy && v.schedules.message != "" })
	var detail domain.ScheduleDetail
	if err = client.Call(ctx, "schedule.get", protocol.ScheduleCommand{ID: updated.ID}, &detail); err != nil || detail.Schedule.Name != updated.Name {
		t.Fatal("stale confirmation deleted another client's accepted edit", detail, err)
	}
}
