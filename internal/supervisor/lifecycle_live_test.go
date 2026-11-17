package supervisor

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/store"
	"shioji/internal/testutil"
	"testing"
	"time"
)

func TestPrivateLifecycleSignedWorkerRetriesAfterTimeoutWithoutSocket(t *testing.T) {
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
	dir, err := os.MkdirTemp("/tmp", "sh-control-retry-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "state.sqlite")
	initial, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	device := initial.DeviceID()
	initial.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	h := &host{ctx: ctx, device: device, options: Options{Directory: dir, Arguments: []string{"--state-dir", dir, "--codex", codex}, ReadyTimeout: 10 * time.Second, KillWait: 2 * time.Second}}
	child, err := h.launch(bin, described.Worker, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer h.stopExact(child, 8*time.Second)
	if _, err = h.ready(ctx, child, false); err != nil {
		t.Fatal(err)
	}
	// Suspend only this isolated, idle child while its first delivery expires.
	if err = child.cmd.Process.Signal(unix.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	defer child.cmd.Process.Signal(unix.SIGCONT)
	first, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	admissionDeadline, _ := first.Deadline()
	_, err = child.lifecycle.request(first, Control{Instance: child.instance, OperationID: "expired-bootstrap", AdmitBefore: admissionDeadline, Replacement: domain.WorkerReplacement{Policy: "when-idle"}})
	stop()
	if err == nil {
		t.Fatal("suspended worker did not delay admission")
	}
	if err = child.cmd.Process.Signal(unix.SIGCONT); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "worker.sock")); err != nil {
		t.Fatal(err)
	}
	if err = h.shutdown(child, "retry-without-socket", 5*time.Second, Control{Replacement: domain.WorkerReplacement{Policy: "when-idle"}}); err != nil {
		t.Fatal("same signed worker lost independent control after transient timeout", err)
	}
	if _, err = ReadDrainIntent(dir, "expired-bootstrap"); !os.IsNotExist(err) {
		t.Fatal("expired attempt acquired drain authority", err)
	}
}
