package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"shioji/internal/testutil"
	"shioji/internal/transport"
)

// This mirrors the production failure: a client starts a large storage scan,
// then the fixed service asks this exact child to drain. Closing its socket
// alone must not leave the scan alive past the common shutdown deadline.
func TestPrivateLifecycleStorageScanReleasesDrainedReceipt(t *testing.T) {
	bin := os.Getenv("SHIOJI_STORAGE_DRAIN_WORKER")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "worker")
		if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
		if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", bin).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	dir, err := os.MkdirTemp("/tmp", "sh-storage-drain-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	leaf := filepath.Join(dir, "worktrees", strings.Repeat("nested/", 40))
	if err = os.MkdirAll(leaf, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50000; i++ {
		f, e := os.Create(filepath.Join(leaf, fmt.Sprintf("file-%05d", i)))
		if e != nil {
			t.Fatal(e)
		}
		f.Close()
	}
	p := testutil.NewProvider(t)
	log, err := os.Create(filepath.Join(t.TempDir(), "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(bin, "--service-child", "--state-dir", dir, "--codex", transport.ExistingCodex())
	cmd.Env = append(os.Environ(), "CODEX_HOME="+p.Home, "SHIOJI_WORKER_INSTANCE=storage-drain")
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	settled := false
	defer func() {
		if !settled {
			cmd.Process.Kill()
			<-exited
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var client *protocol.Client
	for ctx.Err() == nil {
		conn, e := net.DialTimeout("unix", filepath.Join(dir, "worker.sock"), 50*time.Millisecond)
		if e == nil {
			client, e = protocol.Connect(ctx, conn, "")
			if e == nil {
				break
			}
			conn.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if client == nil {
		data, _ := os.ReadFile(log.Name())
		t.Fatal("worker never became ready", string(data))
	}
	defer client.Close()
	scan := make(chan error, 1)
	go func() { scan <- client.Call(ctx, "storage.usage", nil, nil) }()
	// The original scan takes much longer than this on the deliberately deep
	// fixture. Confirm it is still pending before issuing the drain.
	select {
	case e := <-scan:
		t.Fatal("scan completed before drain fixture", e)
	case <-time.After(100 * time.Millisecond):
	}
	conn, err := net.Dial("unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		t.Fatal(err)
	}
	drain, err := protocol.Connect(ctx, conn, client.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer drain.Close()
	in := supervisor.Control{Instance: "storage-drain", OperationID: "storage-upgrade", Replacement: domain.WorkerReplacement{Policy: "when-idle", Explicit: true}}
	var result domain.WorkerUpdateResult
	started := time.Now()
	if err = drain.Call(ctx, "worker.drain", in, &result); err != nil || !result.Ready {
		t.Fatal("drain not accepted", result, err)
	}
	select {
	case err = <-exited:
		settled = true
	case <-time.After(8 * time.Second):
		t.Fatal("storage scan outlived shutdown budget")
	}
	elapsed := time.Since(started)
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 43 {
		data, _ := os.ReadFile(log.Name())
		t.Fatal("worker exited without drained receipt", elapsed, err, string(data))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "supervisor/drained/storage-upgrade-storage-drain.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt supervisor.Drained
	if json.Unmarshal(raw, &receipt) != nil || receipt.PID != cmd.Process.Pid || receipt.DeviceID != client.DeviceID || receipt.OperationID != "storage-upgrade" {
		t.Fatal("wrong drained identity", string(raw))
	}
	t.Log("active storage scan cancelled, exact drained receipt persisted, exit elapsed", elapsed)
}
