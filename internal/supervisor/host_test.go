package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	name := filepath.Base(os.Args[0])
	if strings.HasPrefix(name, "fixture-") {
		os.Exit(fixtureHost(name))
	}
	os.Exit(m.Run())
}
func fixtureHost(name string) int {
	if strings.Contains(name, "private-control") {
		return privateControlFixture(name)
	}
	buildID := "old"
	if strings.Contains(name, "next") {
		buildID = "next"
	}
	build := domain.WorkerBuild{BuildID: buildID, Schema: store.SchemaVersion, Lifecycle: 1, SafeUpdate: true}
	if strings.Contains(name, "schema15") {
		build.Schema = 15
	}
	var dir string
	probe := false
	for i, arg := range os.Args {
		if arg == "--state-dir" && i+1 < len(os.Args) {
			dir = os.Args[i+1]
		}
		if arg == "--probe" {
			probe = true
		}
	}
	if probe {
		if strings.Contains(name, "schema-migration") && strings.HasPrefix(filepath.Base(dir), "schema-migration-") {
			db, err := store.Open(filepath.Join(dir, "state.sqlite"))
			if err != nil || db.Close() != nil {
				return 1
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(build)
		return 0
	}
	if raw, e := os.ReadFile(filepath.Join(dir, "startup-exit")); e == nil {
		if string(raw) == "3" {
			return 3
		}
		return 2
	}
	leaseName := "fixture-owner.lock"
	if strings.Contains(name, "lostreceipt") {
		leaseName = "owner.lock"
	}
	lease, err := os.OpenFile(filepath.Join(dir, leaseName), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 1
	}
	defer lease.Close()
	if unix.Flock(int(lease.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return 2
	}
	_ = os.WriteFile(filepath.Join(dir, "started-"+buildID), []byte(os.Getenv("SHIOJI_WORKER_INSTANCE")), 0600)
	socket := filepath.Join(dir, "worker.sock")
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return 3
	}
	defer listener.Close()
	instance := os.Getenv("SHIOJI_WORKER_INSTANCE")
	paused := os.Getenv("SHIOJI_WORKER_PAUSED") == "true"
	done := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	drainRequests := 0
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				codec, epoch, e := protocol.Welcome(conn, "fixture-device", build)
				if e != nil {
					return
				}
				for {
					var f protocol.Frame
					if codec.Read(&f) != nil {
						return
					}
					var control Control
					_ = json.Unmarshal(f.Body, &control)
					loseReceipt := strings.Contains(name, "lostreceipt") && control.OperationID == "operation"
					var out any
					var failure error
					stop := false
					switch f.Method {
					case "worker.local-ready":
						if _, e := os.Stat(filepath.Join(dir, "hide-ready")); e == nil {
							continue
						}
						mu.Lock()
						p := paused
						mu.Unlock()
						if strings.Contains(name, "unready") {
							p = !p
						}
						out = Ready{PID: os.Getpid(), Instance: instance, DeviceID: "fixture-device", Build: build, Paused: p}
					case "worker.local-background-ready":
						out = domain.WorkerUpdateResult{Ready: control.Instance == instance}
					case "worker.local-background-cancel":
						_ = os.WriteFile(filepath.Join(dir, "cancel-background-attempt"), []byte(control.OperationID), 0600)
						if _, e := os.Stat(filepath.Join(dir, "reject-background-cancel")); e == nil {
							failure = errors.New("background preparation temporarily cannot be restored")
						} else {
							_ = os.WriteFile(filepath.Join(dir, "cancel-background"), []byte(control.OperationID), 0600)
						}
					case "worker.local-backup", "worker.prepare-drain":
						if f.Method == "worker.prepare-drain" {
							if _, e := os.Stat(filepath.Join(dir, "hold-prepare-drain")); e == nil {
								_ = os.WriteFile(filepath.Join(dir, "prepare-drain-entered"), []byte("ready"), 0600)
								for {
									if _, e = os.Stat(filepath.Join(dir, "hold-prepare-drain")); os.IsNotExist(e) {
										break
									}
									time.Sleep(time.Millisecond)
								}
							}
						}
						out = domain.WorkerUpdateResult{Ready: true}
					case "worker.resume":
						if _, e := os.Stat(filepath.Join(dir, "reject-resume")); e == nil {
							_ = os.WriteFile(filepath.Join(dir, "resume-rejected"), []byte(instance), 0600)
							failure = errors.New("resume temporarily unavailable")
							break
						}
						if strings.Contains(name, "holdresume") {
							_ = os.WriteFile(filepath.Join(dir, "resume-entered"), []byte("ready"), 0600)
							for i := 0; i < 300; i++ {
								if _, e := os.Stat(filepath.Join(dir, "release-resume")); e == nil {
									break
								}
								time.Sleep(10 * time.Millisecond)
							}
						}

						j := journal{filepath.Join(dir, "supervisor")}
						op, e := j.current()
						if e != nil || (op.Phase != "applied" && op.Phase != "rolled-back") {
							failure = errors.New("acceptance opened before commit")
						} else {
							mu.Lock()
							paused = false
							mu.Unlock()
							_ = os.WriteFile(filepath.Join(dir, "accepted-"+buildID), []byte(op.Phase), 0600)
						}
					case "worker.drain":
						out = domain.WorkerUpdateResult{Ready: true}
						drainRequests++
						if strings.Contains(name, "busy") && drainRequests == 1 {
							out = domain.WorkerUpdateResult{Ready: false, Reason: "new reservation"}
						} else {
							stop = true
						}
						if stop && loseReceipt {
							failure = SaveDrainIntent(dir, DrainIntent{ID: control.OperationID, Instance: instance, DeviceID: "fixture-device", BuildID: buildID})
						}
					default:
						failure = fmt.Errorf("unsupported fixture method %s", f.Method)
					}
					response := protocol.Frame{Type: "result", ID: f.ID, DeviceID: "fixture-device", Epoch: epoch}
					response.Body, _ = json.Marshal(out)
					if failure != nil {
						response.Error = failure.Error()
					}
					if codec.Write(response) != nil {
						return
					}
					if stop {
						lease.Close()
						if !strings.Contains(name, "nodrain") && !loseReceipt {
							_ = WriteDrained(dir, Drained{PID: os.Getpid(), Instance: instance, OperationID: control.OperationID, DeviceID: "fixture-device", BuildID: buildID})
						}
						once.Do(func() { close(done) })
						return
					}
				}
			}()
		}
	}()
	<-done
	return 43
}

func testHost(t *testing.T, oldMode, nextMode string, selectFailure bool) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sh-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	seedFixtureState(t, dir)
	paths := map[string]string{}
	for build, mode := range map[string]string{"old": oldMode, "next": nextMode} {
		path := filepath.Join(dir, "fixture-"+build+mode)
		if err = os.Symlink(os.Args[0], path); err != nil {
			t.Fatal(err)
		}
		paths[build] = path
	}
	selected := "old"
	var mu sync.Mutex
	options := Options{Directory: dir, InitialPath: paths["old"], Arguments: []string{"--state-dir", dir}, ReadyTimeout: 700 * time.Millisecond, DrainTimeout: time.Second, StopGrace: 100 * time.Millisecond, KillWait: time.Second}
	options.Verify = func(ctx context.Context, build string) (string, domain.WorkerBuild, error) {
		mu.Lock()
		defer mu.Unlock()
		if build == "" {
			build = selected
		}
		return paths[build], domain.WorkerBuild{BuildID: build, Schema: store.SchemaVersion, Lifecycle: 1, SafeUpdate: true}, nil
	}
	options.Select = func(build string) error {
		if selectFailure && build == "next" {
			return errors.New("selected fsync failed")
		}
		mu.Lock()
		selected = build
		mu.Unlock()
		return atomicJSON(filepath.Join(dir, "selected.json"), build)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, options) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("supervisor did not shut down")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(dir, "started-old")); err == nil {
			op, e := Call(context.Background(), dir, Request{Action: "status"})
			if e == nil && op.Phase == "ready" {
				return dir, cancel, done
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture supervisor did not start")
	return "", nil, nil
}
func awaitHostOperation(t *testing.T, dir string) Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		op, err := Call(ctx, dir, Request{Action: "status", ID: "operation"})
		if err == nil && op.Terminal() {
			return op
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("update never reached durable terminal phase")
	return Operation{}
}
func TestHostCommitsBeforeOpeningAcceptanceAndDeduplicates(t *testing.T) {
	dir, _, _ := testHost(t, "", "", false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	in := domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	first, err := Call(ctx, dir, Request{Action: "start", Input: in})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Call(ctx, dir, Request{Action: "start", Input: in})
	if err != nil || !second.Created.Equal(first.Created) {
		t.Fatal("same operation repeated", second, err)
	}
	op := awaitHostOperation(t, dir)
	if op.Phase != "applied" {
		t.Fatal(op)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if raw, e := os.ReadFile(filepath.Join(dir, "accepted-next")); e == nil && string(raw) == "applied" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("committed candidate did not open acceptance")
}
func TestHostWithoutDrainNeverStartsCandidate(t *testing.T) {
	dir, _, _ := testHost(t, "-nodrain", "", false)
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitHostOperation(t, dir)
	if op.Phase != "blocked" {
		t.Fatal(op)
	}
	if _, err = os.Stat(filepath.Join(dir, "started-next")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("candidate started without source drain", err)
	}
}

func TestHostRestartsSourceAfterAcceptedDrainLosesReceipt(t *testing.T) {
	dir, _, _ := testHost(t, "-lostreceipt", "", false)
	before, err := os.ReadFile(filepath.Join(dir, "started-old"))
	if err != nil {
		t.Fatal(err)
	}
	in := domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	if _, err := Call(context.Background(), dir, Request{Action: "start", Input: in}); err != nil {
		t.Fatal(err)
	}
	op := awaitHostOperation(t, dir)
	if op.Phase != "failed" || !strings.Contains(op.Error, "without a drained receipt") {
		t.Fatalf("original update must report the missing receipt, got %s: %s", op.Phase, op.Error)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
		if err == nil {
			client, err := protocol.Connect(ctx, conn, "fixture-device")
			if err == nil {
				var ready Ready
				after, readErr := os.ReadFile(filepath.Join(dir, "started-old"))
				err = client.Call(ctx, "worker.local-ready", Control{Instance: string(after)}, &ready)
				client.Close()
				if readErr == nil && err == nil && string(before) != ready.Instance && ready.Build.BuildID == "old" && !ready.Paused {
					if _, err := os.Stat(filepath.Join(dir, "started-next")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("update candidate started without the drained receipt", err)
					}
					return
				}
			} else {
				conn.Close()
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("supervisor did not restore business acceptance without a client recovery request")
}

func TestHostLostDrainKeepsServiceBlockedWithUnverifiedExecution(t *testing.T) {
	dir, _, _ := testHost(t, "-lostreceipt", "", false)
	before, err := os.ReadFile(filepath.Join(dir, "started-old"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "executions", "unverified-session"), 0700); err != nil {
		t.Fatal(err)
	}
	in := domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	if _, err := Call(context.Background(), dir, Request{Action: "start", Input: in}); err != nil {
		t.Fatal(err)
	}
	op := awaitHostOperation(t, dir)
	if op.Phase != "blocked" || !strings.Contains(op.Error, "旧进程登记记录不可读取") {
		t.Fatalf("unverified execution scope did not block automatic restart: %s: %s", op.Phase, op.Error)
	}
	after, err := os.ReadFile(filepath.Join(dir, "started-old"))
	if err != nil || string(after) != string(before) {
		t.Fatal("restarted the old writer before proving execution ownership", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "started-next")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("launched the candidate with an unverified execution scope", err)
	}
}
func TestHostUnreadyCandidateDrainsThenRollsBackOnce(t *testing.T) {
	dir, _, _ := testHost(t, "", "-unready", false)
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitHostOperation(t, dir)
	if op.Phase != "rolled-back" {
		t.Fatal(op)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if raw, e := os.ReadFile(filepath.Join(dir, "accepted-old")); e == nil && string(raw) == "rolled-back" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rollback did not resume original compatible build")
}
func TestHostSelectionFailureKeepsCandidatePaused(t *testing.T) {
	dir, _, _ := testHost(t, "", "", true)
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitHostOperation(t, dir)
	if op.Phase != "blocked" {
		t.Fatal(op)
	}
	if _, err = os.Stat(filepath.Join(dir, "accepted-next")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("uncommitted candidate accepted input", err)
	}
}

func TestHostRechecksBusinessIdleAtDrainAdmission(t *testing.T) {
	dir, _, _ := testHost(t, "-busy", "", false)
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitHostOperation(t, dir)
	if op.Phase != "applied" {
		t.Fatal("business admission race should wait without killing the worker", op)
	}
}

func TestHostReportsCompletionOnlyAfterAcceptanceOpened(t *testing.T) {
	dir, _, _ := testHost(t, "", "-holdresume", false)
	defer os.WriteFile(filepath.Join(dir, "release-resume"), []byte("yes"), 0600)
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(dir, "resume-entered")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	op, err := Call(context.Background(), dir, Request{Action: "status", ID: "operation"})
	if err != nil || op.Phase != "observing" {
		t.Fatal("reported applied while candidate remained paused", op.Phase, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "release-resume"), []byte("yes"), 0600)
	if op = awaitHostOperation(t, dir); op.Phase != "applied" {
		t.Fatal(op)
	}
}

func TestHostConcurrentClientJoinsSameUpdateIntent(t *testing.T) {
	dir, _, _ := testHost(t, "", "-holdresume", false)
	defer os.WriteFile(filepath.Join(dir, "release-resume"), []byte("yes"), 0600)
	first := domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	if _, err := Call(context.Background(), dir, Request{Action: "start", Input: first}); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = "another-client"
	second.Explicit = true
	joined, err := Call(context.Background(), dir, Request{Action: "start", Input: second})
	if err != nil || joined.Input.ID != first.ID {
		t.Fatal("same device/target did not share the original operation", joined, err)
	}
}

func TestColdRepairRequiresReleasedWriterAndRetainsOldBlockedReceipt(t *testing.T) {
	dir, _, _ := testHost(t, "-nodrain", "", false)
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitHostOperation(t, dir); op.Phase != "blocked" {
		t.Fatal(op)
	}
	input := domain.WorkerReplacement{ID: "cold-repair", BuildID: "next", Policy: "immediate", Explicit: true}
	writer, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err = unix.Flock(int(writer.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err = Call(context.Background(), dir, Request{Action: "repair", DeviceID: "fixture-device", Input: input}); err == nil {
		t.Fatal("cold repair bypassed the original writer lease")
	}
	if _, err = (journal{filepath.Join(dir, "supervisor")}).load(input.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused repair was accepted", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	repaired, err := Call(context.Background(), dir, Request{Action: "repair", DeviceID: "fixture-device", Input: input})
	if err != nil || repaired.Input.ID != "cold-repair" {
		t.Fatal("ordinary compatible installer cannot repair a quiescent blocked service", repaired, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		op, e := Call(context.Background(), dir, Request{Action: "status", ID: input.ID})
		if e == nil && op.Phase == "applied" {
			original, e := Call(context.Background(), dir, Request{Action: "status", ID: "operation"})
			if e != nil || original.Phase != "blocked" {
				t.Fatal("repair overwrote original blocked receipt", original, e)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("compatible cold repair did not open acceptance")
}

func TestColdRepairRefusesLiveUncommittedCandidate(t *testing.T) {
	dir, _, _ := testHost(t, "", "", true)
	_, err := Call(context.Background(), dir, Request{Action: "start", Input: domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}})
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitHostOperation(t, dir); op.Phase != "blocked" {
		t.Fatal(op)
	}
	_, err = Call(context.Background(), dir, Request{Action: "repair", DeviceID: "fixture-device", Input: domain.WorkerReplacement{ID: "cold-repair", BuildID: "next", Policy: "immediate", Explicit: true}})
	if err == nil {
		t.Fatal("cold repair replaced a live candidate")
	}
	if _, err = os.Stat(filepath.Join(dir, "accepted-next")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("uncommitted candidate opened acceptance", err)
	}
}
