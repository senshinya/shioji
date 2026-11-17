package worker

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
	"syscall"
	"testing"
	"time"
)

type archiveShutdownHarness struct{ maintenanceHarness }

func (a archiveShutdownHarness) Start(ctx context.Context, o harness.StartOptions) (harness.Runtime, error) {
	r, err := a.maintenanceHarness.Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &archiveShutdownRuntime{r.(*maintenanceRuntime)}, nil
}

type archiveShutdownRuntime struct{ *maintenanceRuntime }

func (*archiveShutdownRuntime) ManagedCoverage() bool { return false }

func TestExplicitArchiveDeletionRetiresUncoveredIdleEnvironment(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		s, w := memoryWorker(t, archiveShutdownHarness{})
		d := submitIdleFixture(t, s, w, "explicit-delete", "")
		r := s.currentRuntime(d.Session.ID)
		a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
		if err != nil {
			t.Fatal(err)
		}
		method := "session.delete"
		values := map[string]any{"sessionId": a.ID, "revision": a.Revision, "closeRuntime": true, "operationId": "delete-idle"}
		if workspace {
			aw, e := s.store.SetWorkspaceArchived(w.ID, w.Revision, true)
			if e != nil {
				t.Fatal(e)
			}
			method = "workspace.delete"
			values = map[string]any{"workspaceId": aw.ID, "revision": aw.Revision, "closeRuntime": true, "operationId": "delete-workspace"}
		}
		body, _ := json.Marshal(values)
		var in protocol.ArchiveDeleteInput
		if err = json.Unmarshal(body, &in); err != nil {
			t.Fatal(err)
		}
		s.dispatchMu.Lock()
		_, err = s.deleteArchive(method, in)
		s.dispatchMu.Unlock()
		if err != nil {
			t.Fatal("explicit deletion still blocked by configuration coverage", err)
		}
		if e := s.cleanupArchives(time.Now()); e != nil {
			t.Fatal(e)
		}
		if !r.Stopped() {
			t.Fatal("deleted history without retiring its runtime")
		}
		if _, err = s.store.Session(a.ID, 0, 1); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("deleted history retained", err)
		}
	}
}

func TestExplicitArchiveDeletionRetiresActiveWork(t *testing.T) {
	s, w := memoryWorker(t, archiveShutdownHarness{})
	d := submitIdleFixture(t, s, w, "active-delete", "")
	r := s.currentRuntime(d.Session.ID)
	_, err := s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "late", Title: "Active child", State: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.store.Session(d.Session.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Lock()
	_, err = s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: a.ID, Revision: a.Revision, CloseRuntime: true, OperationID: "active-delete"})
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if e := s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	if !r.Stopped() {
		t.Fatal("accepted deletion did not retire owned work")
	}
	if _, err = s.store.Session(a.ID, 0, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted archive remained", err)
	}
}

func TestExplicitArchiveDeletionClosesObservedDetachedChildren(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	s, w := memoryWorker(t, archiveShutdownHarness{})
	d := submitIdleFixture(t, s, w, "detached-archive", "")
	scope, err := s.executions.Existing(d.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code := "import subprocess,sys\np=subprocess.Popen(['/bin/sleep','60'],start_new_session=True)\nprint(p.pid,flush=True)\nsys.stdin.readline()"
	cmd, stdin, stdout, err := scope.StartOwned(ctx, "archive-child", []string{python, "-u", "-c", code}, []string{"PATH=/usr/bin:/bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Wait() }()
	if _, err = bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	observation, err := scope.InspectExplicitMaintenance()
	if err != nil {
		t.Fatal(err)
	}
	observed := 0
	for _, p := range observation.Processes {
		if p.Evidence == "observed" {
			observed++
		}
	}
	if observed == 0 {
		t.Fatal("fixture did not create a detached descendant")
	}
	unrelated := exec.Command("/bin/sleep", "60")
	if err = unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { unrelated.Process.Kill(); unrelated.Wait() }()
	a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Lock()
	_, err = s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: a.ID, Revision: a.Revision, CloseRuntime: true, OperationID: "delete-detached"})
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal("normal retirement did not handle detached child", err)
	}
	if err = s.executions.Reap(ctx); err != nil {
		t.Fatal("deleted scope remained in live monitoring", err)
	}
	if err = unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("unrelated process was affected", err)
	}
}

func TestForceArchiveDeletionRemovesGuardedHistoryAndKeepsCleanupOwnership(t *testing.T) {
	s, w := memoryWorker(t, archiveShutdownHarness{})
	d := submitIdleFixture(t, s, w, "force-archive", "")
	a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.store.Apply(a.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "late", Title: "stale activity", State: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.store.Session(a.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"sessionId": a.ID, "revision": a.Revision, "operationId": "force-delete", "closeRuntime": true, "force": true})
	var in protocol.ArchiveDeleteInput
	json.Unmarshal(body, &in)
	s.dispatchMu.Lock()
	_, err = s.deleteArchive("session.delete", in)
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal("force deletion was blocked by activity guard", err)
	}
	if _, err = s.store.Session(a.ID, 0, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("force deletion retained visible history", err)
	}
	if _, err = s.store.Apply(a.ID, domain.Event{Type: "runtime.lost"}); err == nil {
		t.Fatal("late runtime event restored deleted history")
	}
}

func TestForceArchiveDeletionClosesAnUnresponsiveOwnedProcess(t *testing.T) {
	s, w := memoryWorker(t, archiveShutdownHarness{})
	d := submitIdleFixture(t, s, w, "force-process", "")
	maintenanceTool(t, s, d.Session.ID)
	a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Lock()
	_, err = s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: a.ID, Revision: a.Revision, CloseRuntime: true, Force: true, OperationID: "force-process"})
	s.dispatchMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if e := s.cleanupArchives(time.Now()); e != nil {
		t.Fatal(e)
	}
	if err = s.executions.Reap(context.Background()); err != nil {
		t.Fatal("deleted scope remained in live monitoring", err)
	}
}

func TestArchiveCleanupRecoversOnlyDurableForceConsentAfterDeletionCommit(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			s, w := memoryWorker(t, archiveShutdownHarness{})
			d := submitIdleFixture(t, s, w, "crash-after-delete", "")
			cmd, _ := maintenanceTool(t, s, d.Session.ID)
			a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := s.executions.Existing(a.ID)
			if err != nil {
				t.Fatal(err)
			}
			observation, err := scope.InspectExplicitMaintenance()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = scope.BeginExplicitStop("archive-crash:"+a.ID, observation.Ref, force); err != nil {
				t.Fatal(err)
			}
			// Crash point: deletion committed, but Accept/ForceManualStop has
			// not run. Reopen scope/state through a fresh owning worker.
			if _, err = s.store.ForceDeleteArchivedSession(a.ID); err != nil {
				t.Fatal(err)
			}
			config := s.config
			_ = s.Close() // pending unaccepted shutdown is expected here
			restarted, err := New(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { restarted.Close() })
			restarted.dispatchMu.Lock()
			err = restarted.cleanArchiveGarbage()
			restarted.dispatchMu.Unlock()
			if force {
				if err != nil {
					t.Fatal("committed forced deletion could not resume cleanup", err)
				}
				if _, err = os.Stat(scope.Directory); !os.IsNotExist(err) {
					t.Fatal("forced generation retained after cleanup", err)
				}
			} else {
				if err == nil {
					t.Fatal("normal-only preparation was implicitly accepted")
				}
				if err = cmd.Process.Signal(syscall.Signal(0)); err != nil {
					t.Fatal("normal-only process was killed without consent", err)
				}
			}
		})
	}
}

func TestForceArchiveDeletionWarnsAndRetainsUnverifiableCleanup(t *testing.T) {
	s, w := memoryWorker(t, archiveShutdownHarness{})
	d := submitIdleFixture(t, s, w, "force-unknown", "")
	a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.executions.Directory, a.ID, "scope.json")
	if err = os.WriteFile(path, []byte("unreadable ownership"), 0600); err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Lock()
	result, err := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: a.ID, Revision: a.Revision, CloseRuntime: true, Force: true, OperationID: "force-unknown"})
	s.dispatchMu.Unlock()
	if err != nil || result.Warning == "" {
		t.Fatal("cleanup failure vetoed force deletion or lost warning", result, err)
	}
	if _, err = s.store.Session(a.ID, 0, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("visible archive retained", err)
	}
	items, err := s.store.ArchiveGarbage()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.SessionID == a.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("cleanup ownership was discarded")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("unverified execution records were discarded", err)
	}
	if err = s.recoverManagedScopes(context.Background()); err != nil {
		t.Fatal("deleted cleanup prevented worker recovery", err)
	}
	other := submitIdleFixture(t, s, w, "force-after-unknown", "")
	archived, err := s.store.SetSessionArchived(other.Session.ID, other.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Lock()
	result, err = s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: archived.ID, Revision: archived.Revision, CloseRuntime: true, Force: true, OperationID: "force-other"})
	s.dispatchMu.Unlock()
	if err != nil || result.Warning != "" {
		t.Fatal("unrelated pending cleanup blocked another deletion", result, err)
	}
	_ = s.cleanupArchives(time.Now())
	if s.currentRuntime(archived.ID) != nil {
		t.Fatal("unrelated pending cleanup blocked healthy cleanup")
	}
}

func TestForceArchivedWorkspaceDeletionKeepsProjectAndRejectsRestoredSession(t *testing.T) {
	s, w := memoryWorker(t, archiveShutdownHarness{})
	d := submitIdleFixture(t, s, w, "force-workspace", "")
	// A non-archived object is never included merely because force was set.
	s.dispatchMu.Lock()
	_, err := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: d.Session.ID, Force: true, CloseRuntime: true, OperationID: "unarchived"})
	s.dispatchMu.Unlock()
	if err == nil {
		t.Fatal("force deleted a restored/non-archived session")
	}
	aw, err := s.store.SetWorkspaceArchived(w.ID, w.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(w.Folders[0], "project.txt")
	if err = os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "late", Title: "unconfirmed", State: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	s.dispatchMu.Lock()
	result, err := s.deleteArchive("workspace.delete", protocol.ArchiveDeleteInput{WorkspaceID: aw.ID, Revision: aw.Revision, Force: true, CloseRuntime: true, OperationID: "force-workspace"})
	s.dispatchMu.Unlock()
	if err != nil || result.WorkspaceID != w.ID {
		t.Fatal("force workspace deletion blocked", err)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal("force deletion touched project files", err)
	}
}

func TestArchiveDeletionRetiresUncoveredRuntimeWithoutExtraAction(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		s, w := memoryWorker(t, memoryHarness{id: "idle"})
		d := submitIdleFixture(t, s, w, "unverified", "")
		r := s.currentRuntime(d.Session.ID)
		a, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
		if err != nil {
			t.Fatal(err)
		}
		method, in := "session.delete", protocol.ArchiveDeleteInput{SessionID: a.ID, Revision: a.Revision}
		if workspace {
			aw, e := s.store.SetWorkspaceArchived(w.ID, w.Revision, true)
			if e != nil {
				t.Fatal(e)
			}
			method, in = "workspace.delete", protocol.ArchiveDeleteInput{WorkspaceID: w.ID, Revision: aw.Revision}
		}
		s.dispatchMu.Lock()
		_, err = s.deleteArchive(method, in)
		s.dispatchMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if e := s.cleanupArchives(time.Now()); e != nil {
			t.Fatal(e)
		}
		if !r.Stopped() {
			t.Fatal("single deletion did not retire captured runtime")
		}
		if err = s.cleanupArchives(a.ArchivedAt.Add(31 * 24 * time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.Session(a.ID, 0, 1); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("deleted archive reappeared", err)
		}
	}
}

func TestArchiveDeletionCleansDiffCommentDependencies(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(map[bool]string{false: "exclusive", true: "shared"}[shared], func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			snapshot, err := s.putDiffResource(w.ID, domain.DiffSnapshotResource, "diff-snapshot.json", domain.FileVersions{Path: "private.go", New: "private source"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.attachments.RetainDiffSnapshot(w.ID, snapshot); err != nil {
				t.Fatal(err)
			}
			add := func(id string) domain.Session {
				d := submitIdleFixture(t, s, w, id, "")
				comment, e := s.putDiffResource(w.ID, domain.DiffCommentResource, "private.go:1", domain.DiffCommentRecord{Snapshot: snapshot, Content: domain.CodeCommentContent{Comment: id}})
				if e != nil {
					t.Fatal(e)
				}
				_, e = s.store.Apply(d.Session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "comment", Kind: "user", Attachments: []domain.Attachment{comment}}})
				if e != nil {
					t.Fatal(e)
				}
				d, e = s.store.Session(d.Session.ID, 0, 1)
				if e != nil {
					t.Fatal(e)
				}
				a, e := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
				if e != nil {
					t.Fatal(e)
				}
				return a
			}
			first := add("first")
			var second domain.Session
			if shared {
				second = add("second")
			}
			remove := func(a domain.Session) {
				s.dispatchMu.Lock()
				_, e := s.deleteArchive("session.delete", protocol.ArchiveDeleteInput{SessionID: a.ID, Revision: a.Revision})
				s.dispatchMu.Unlock()
				if e != nil {
					t.Fatal(e)
				}
				for i := 0; i < 3; i++ {
					s.dispatchMu.Lock()
					e = s.cleanArchiveGarbage()
					s.dispatchMu.Unlock()
					if e != nil {
						t.Fatal(e)
					}
				}
			}
			path := filepath.Join(s.attachments.Directory, w.ID, snapshot.ID)
			remove(first)
			if shared {
				if _, err = os.Stat(path); err != nil {
					t.Fatal("shared snapshot removed", err)
				}
				remove(second)
			}
			for _, suffix := range []string{"", ".json", ".retained"} {
				if _, err = os.Stat(path + suffix); !os.IsNotExist(err) {
					t.Fatal("deleted comment retained private snapshot", suffix, err)
				}
			}
		})
	}
}

func TestWorkerArchiveCleanupUsesOwningClockAndRetiresExpiredWork(t *testing.T) {
	s, w := memoryWorker(t, idleHarness{})
	d := submitIdleFixture(t, s, w, "archive-first", "")
	runtime := s.currentRuntime(d.Session.ID)
	archived, err := s.store.SetSessionArchived(d.Session.ID, d.Session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := s.store.ArchivePolicy()
	if _, err = s.store.ConfigureArchive(7, policy.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupArchives(archived.ArchivedAt.Add(7*24*time.Hour - time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Session(archived.ID, 0, 1); err != nil {
		t.Fatal("deleted early", err)
	}
	_, err = s.store.Apply(archived.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "late", Title: "active child", State: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.cleanupArchives(archived.ArchivedAt.Add(8 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.Session(archived.ID, 0, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired archive retained", err)
	}
	if !runtime.Stopped() || s.currentRuntime(archived.ID) != nil {
		t.Fatal("deleted archive kept runtime ownership")
	}
	if _, err = os.Stat(filepath.Join(s.config.Directory, "executions", archived.ID)); !os.IsNotExist(err) {
		t.Fatal("deleted scope retained", err)
	}
	if _, err = s.store.Apply(archived.ID, domain.Event{Type: "runtime.lost"}); err == nil {
		t.Fatal("late callback restored deleted state")
	}
}
