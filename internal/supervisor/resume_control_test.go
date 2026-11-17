package supervisor

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"shioji/internal/testutil"
	"testing"
	"time"
)

func TestCommittedResumeRetriesTemporaryBusinessEndpointFailure(t *testing.T) {
	dir, o := regressionOptions(t)
	selectBuild := o.Select
	restored := make(chan error, 1)
	o.Select = func(build string) error {
		if err := selectBuild(build); err != nil {
			return err
		}
		if build == "next" {
			if err := os.Rename(filepath.Join(dir, "worker.sock"), filepath.Join(dir, "worker-held.sock")); err != nil {
				return err
			}
			go func() {
				time.Sleep(120 * time.Millisecond)
				restored <- os.Rename(filepath.Join(dir, "worker-held.sock"), filepath.Join(dir, "worker.sock"))
			}()
		}
		return nil
	}
	regressionHost(t, o, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	in := domain.WorkerReplacement{ID: "operation", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	if _, err := Call(ctx, dir, Request{Action: "start", Input: in}); err != nil {
		t.Fatal(err)
	}
	op := awaitHostOperation(t, dir)
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
	if op.Phase != "applied" {
		t.Fatal("temporary endpoint loss blocked committed candidate", op.Phase, op.Error)
	}
	if _, err := os.Stat(filepath.Join(dir, "accepted-next")); err != nil {
		t.Fatal("candidate did not open acceptance", err)
	}
}

func TestPrivateResumeKeepsChannelForLaterDrain(t *testing.T) {
	parent, child := net.Pipe()
	client := newLifecycleClient(parent)
	defer client.Close()
	defer child.Close()
	ended := make(chan bool, 1)
	go func() {
		accepted, _ := ServeLifecycle(child, func(Control) (domain.WorkerUpdateResult, error) { return domain.WorkerUpdateResult{Ready: true}, nil })
		ended <- accepted
	}()
	var resume Control
	if err := json.Unmarshal([]byte(`{"action":"resume","Instance":"captured","OperationID":"commit"}`), &resume); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if out, err := client.request(ctx, resume); err != nil || !out.Ready {
		t.Fatal(out, err)
	}
	select {
	case <-ended:
		t.Fatal("resume was mistaken for shutdown")
	case <-time.After(20 * time.Millisecond):
	}
	if out, err := client.request(ctx, Control{Instance: "captured", OperationID: "stop"}); err != nil || !out.Ready {
		t.Fatal(out, err)
	}
	select {
	case accepted := <-ended:
		if !accepted {
			t.Fatal("drain did not finish lifecycle")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestCommittedSignedCandidateResumesWithoutBusinessSocket(t *testing.T) {
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "worker")
	build := exec.Command("go", "build", "-o", bin, "./cmd/shioji-worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", bin).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	raw, err := exec.Command(bin, "--describe").Output()
	if err != nil {
		t.Fatal(err)
	}
	var described struct {
		Worker domain.WorkerBuild `json:"worker"`
	}
	if err = json.Unmarshal(raw, &described); err != nil {
		t.Fatal(err)
	}
	provider := testutil.NewProvider(t)
	t.Setenv("CODEX_HOME", provider.Home)
	dir, err := os.MkdirTemp("/tmp", "sh-deep-resume-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	device := db.DeviceID()
	db.Close()
	op := "deep-signed-commit"
	if err = SaveDrainIntent(dir, DrainIntent{ID: op, Instance: "previous-instance", DeviceID: device, BuildID: described.Worker.BuildID}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	h := &host{ctx: ctx, device: device, journal: journal{filepath.Join(dir, "supervisor")}, options: Options{Directory: dir, Arguments: []string{"--state-dir", dir, "--codex", codex}, ReadyTimeout: 10 * time.Second, KillWait: 2 * time.Second}}
	c, err := h.launch(bin, described.Worker, true, op)
	if err != nil {
		t.Fatal(err)
	}
	defer h.stopExact(c, 8*time.Second)
	if _, err = h.ready(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	if err = h.journal.save(Operation{Input: domain.WorkerReplacement{ID: op, BuildID: described.Worker.BuildID}, Phase: "applied", DeviceID: device}); err != nil {
		t.Fatal(err)
	}
	if c.lifecycle == nil {
		t.Fatal("real candidate has no inherited control")
	}
	if err = os.Rename(filepath.Join(dir, "worker.sock"), filepath.Join(dir, "worker-held.sock")); err != nil {
		t.Fatal(err)
	}
	resumeErr := h.resume(ctx, c, op)
	if resumeErr != nil {
		t.Fatal("private resume still depends on public endpoint", resumeErr)
	}
	if err = os.Rename(filepath.Join(dir, "worker-held.sock"), filepath.Join(dir, "worker.sock")); err != nil {
		t.Fatal(err)
	}
	if _, err = h.ready(ctx, c, false); err != nil {
		t.Fatal("private resume did not open acceptance", err)
	}
	if err = h.resume(ctx, c, op); err != nil {
		t.Fatal("restored public endpoint positive control", err)
	}
	if _, err = h.ready(ctx, c, false); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "worker.sock")); err != nil {
		t.Fatal(err)
	}
	if err = h.shutdown(c, "deep-signed-private-drain", 5*time.Second, Control{Replacement: domain.WorkerReplacement{Policy: "when-idle"}}); err != nil {
		t.Fatal("healthy inherited drain control failed", err)
	}
	t.Logf("build=%s privateControl=%d missingEndpointResume=%v restoredEndpointResume=PASS inheritedDrainWithoutSocket=PASS", described.Worker.BuildID, described.Worker.PrivateControl, resumeErr)
}
