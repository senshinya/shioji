package ui

import (
	"context"
	"fmt"
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
	"shioji/internal/store"
	"shioji/internal/worker"
)

func TestArchiveBackgroundDeleteThroughOwningWorker(t *testing.T) {
	dir, err := os.MkdirTemp("", "sh-archive-ui-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.CreateWorkspace("归档删除实测", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var keep domain.Session
	for i := 0; i < 17; i++ {
		op, err := db.Accept(domain.Submission{OperationID: fmt.Sprint("archive-ui-", i), WorkspaceID: w.ID, Text: "isolated archive fixture", Config: domain.RunConfig{Model: "test", Permission: "read-only"}})
		if err != nil {
			t.Fatal(err)
		}
		if err = db.SetOperation(op.ID, "confirmed", ""); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}}); err != nil {
			t.Fatal(err)
		}
		d, err := db.Session(op.SessionID, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if i == 16 {
			keep = d.Session
		} else if _, err = db.SetSessionArchived(d.Session.ID, d.Session.Revision, true); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := worker.New(ctx, worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: filepath.Join(dir, "codex"), Executable: "/usr/bin/false"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go server.Serve()
	m := app.NewModel()
	m.FirstRun = false
	m.Drafts["home"] = "保留普通草稿"
	c := app.NewController(dir)
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	v := New(m, c, "")
	defer v.CloseTransient()
	ui := n.NewTester(v.View, 1240, 820)
	waitNative(t, ui, func() bool { return m.Connected && len(m.Snapshot.Sessions) == 17 })
	v.openArchive()
	ui.Frame()
	ui.Advance(time.Second)
	if err = ui.Click("删除全部"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	start := time.Now()
	if err = ui.Click("彻底删除"); err != nil {
		t.Fatal(err)
	}
	if err = ui.Click("新建会话"); err != nil {
		t.Fatal("real worker deletion blocked navigation:", err)
	}
	waitNative(t, ui, func() bool { return v.archive.deletion == nil || !v.archive.deletion.busy })
	if len(m.Snapshot.Sessions) != 1 || m.Snapshot.Sessions[0].ID != keep.ID || v.archive.open || m.Drafts["home"] != "保留普通草稿" {
		t.Fatal("real delete batch changed an uncaptured session, navigation or draft", v.archive.deletion, m.Snapshot.Sessions)
	}
	var dialer net.Dialer
	client, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
	}, m.Snapshot.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var snapshot domain.Snapshot
	if err = client.Call(ctx, "snapshot", nil, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0].ID != keep.ID {
		t.Fatal("UI removal preceded owning-worker deletion", snapshot.Sessions)
	}
	t.Logf("native View → Controller → authenticated worker RPC → durable deletion: 16 archives; retained unarchived session and draft; elapsed=%s", time.Since(start))
}
