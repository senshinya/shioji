package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/managedexec"
	"shioji/internal/store"
	"testing"
	"time"
)

type namingHarness struct {
	memoryHarness
	started chan string
	release chan string
}

type processNamingRuntime struct{ harness.Runtime }

func (r *processNamingRuntime) GenerateTitle(ctx context.Context, input harness.TitleInput) (string, error) {
	cmd, in, out, err := input.Scope.StartOwned(ctx, "title-test", []string{"/usr/bin/true"}, []string{"PATH=/usr/bin:/bin"}, nil)
	if err != nil {
		return "", err
	}
	in.Close()
	io.Copy(io.Discard, out)
	return "短标题", cmd.Wait()
}

func TestTitleTasksRetireOwnedProcessRecords(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := db.CreateWorkspace("titles", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &processNamingRuntime{}
	s := &Server{ctx: ctx, store: db, runtime: r, config: Config{DefaultHarnessID: "other"}}
	manager, err := managedexec.New(t.TempDir(), os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second", "third"} {
		_, err = db.AcceptForHarness(domain.Submission{OperationID: id, WorkspaceID: w.ID, Text: id}, "other", domain.LaunchContext{CWD: w.Folders[0]})
		if err != nil {
			t.Fatal(err)
		}
		job, err := db.ClaimTitleGeneration()
		if err != nil {
			t.Fatal(err)
		}
		if title := s.generateTitle(manager, job); title != "短标题" {
			t.Fatal("owned title did not run", title)
		}
		if count := reflect.ValueOf(manager).Elem().FieldByName("live").Len(); count != 0 {
			t.Fatal("finished title retained process ownership records", count)
		}
	}
}

func TestBrokenOldTitleScopeDoesNotDisableNamingOtherSessions(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "title-executions", "old-session")
	os.MkdirAll(bad, 0700)
	os.WriteFile(filepath.Join(bad, "scope.json"), []byte("broken"), 0600)
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := db.CreateWorkspace("titles", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.AcceptForHarness(domain.Submission{OperationID: "new", WorkspaceID: w.ID, Text: "新请求"}, "other", domain.LaunchContext{CWD: w.Folders[0]})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan string, 1), make(chan string, 1)
	s := &Server{ctx: ctx, store: db, runtime: &namingRuntime{started: started, release: release}, config: Config{Directory: dir, LauncherExecutable: os.Args[0], DefaultHarnessID: "other"}, titleWake: make(chan struct{}, 1)}
	s.wg.Add(1)
	done := make(chan struct{})
	go func() { s.titleLoop(); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
		release <- "新标题"
	case <-done:
		t.Fatal("one broken old title scope disabled unrelated naming")
	case <-time.After(10 * time.Second):
		t.Fatal("new title task never ran")
	}
}

func (a namingHarness) Start(ctx context.Context, opts harness.StartOptions) (harness.Runtime, error) {
	r, err := a.memoryHarness.Start(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &namingRuntime{Runtime: r, started: a.started, release: a.release}, nil
}

type namingRuntime struct {
	harness.Runtime
	started chan string
	release chan string
}

func (r *namingRuntime) GenerateTitle(ctx context.Context, input harness.TitleInput) (string, error) {
	select {
	case r.started <- input.Prompt:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case title := <-r.release:
		return title, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestAutomaticNamingRunsIndependentlyAndPreservesUserRename(t *testing.T) {
	for _, manual := range []bool{false, true} {
		started, release := make(chan string, 2), make(chan string, 1)
		s, w := memoryWorker(t, namingHarness{memoryHarness: memoryHarness{id: "other"}, started: started, release: release})
		in := domain.Submission{OperationID: "initial", WorkspaceID: w.ID, HarnessID: "other", Text: "检查旋转边界", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}}
		op, err := s.submit(in)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case prompt := <-started:
			if prompt != in.Text {
				t.Fatal("title prompt differs", prompt)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("automatic title was never started")
		}
		if got := awaitReceipt(t, s, in.OperationID); got.State != "confirmed" {
			t.Fatal("naming blocked normal reply", got)
		}
		if manual {
			if _, err = s.store.RenameSession(op.SessionID, "手动名称", 0, false); err != nil {
				t.Fatal(err)
			}
		}
		release <- "旋转边界检查"
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		snapshot, _ := s.store.Snapshot()
		want := "旋转边界检查"
		if manual {
			want = "手动名称"
		}
		for {
			d, e := s.store.Session(op.SessionID, 0, 100)
			if e != nil {
				t.Fatal(e)
			}
			if d.Session.Title == want {
				if len(d.Items) != 2 || d.Session.Status != "completed" {
					t.Fatal("auxiliary task polluted conversation", d)
				}
				break
			}
			if e = s.store.WaitEvents(ctx, d.Sequence, snapshot.Generation, time.Second); ctx.Err() != nil {
				t.Fatal("title never updated", e)
			}
		}
		cancel()
	}
}
