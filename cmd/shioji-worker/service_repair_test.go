package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/store"
	"shioji/internal/supervisor"
	"shioji/internal/testutil"
	"shioji/internal/transport"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSignedColdInstallerEstablishesMissingControlEntry(t *testing.T) {
	p := testutil.NewProvider(t)
	t.Setenv("CODEX_HOME", p.Home)
	dir, err := os.MkdirTemp("/tmp", "sh-repair-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	state, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	device := state.DeviceID()
	if err = state.Close(); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "installer")
	if out, e := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	if out, e := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", binary).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	candidate, err := transport.StageWorker(ctx, dir, binary)
	if err != nil {
		t.Fatal(err)
	}
	original, err := supervisor.AcceptLegacy(dir, domain.WorkerReplacement{ID: "interrupted-migration", ExpectedBuildID: "old-source", BuildID: candidate.BuildID, Policy: "when-idle"}, device)
	if err != nil {
		t.Fatal(err)
	}
	original.Phase = "blocked"
	original.Error = "migration stopped before fixed control endpoint"
	if err = supervisor.SaveLegacy(dir, original); err != nil {
		t.Fatal(err)
	}
	if err = transport.SaveActiveWorker(dir, candidate.BuildID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		conn, e := net.Dial("unix", filepath.Join(dir, "supervisor.sock"))
		if e != nil {
			return
		}
		raw, _ := conn.(*net.UnixConn).SyscallConn()
		var pid int
		_ = raw.Control(func(fd uintptr) { pid, _ = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) })
		conn.Close()
		list, _ := managedexec.Processes()
		for _, process := range list {
			if process.Identity.PID == pid {
				_ = managedexec.SignalExact(process, unix.SIGTERM)
				deadline := time.Now().Add(8 * time.Second)
				for managedexec.ProcessState(process) == "running" && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
			}
		}
	}()
	input := domain.WorkerReplacement{ID: "cold-install", BuildID: candidate.BuildID, Policy: "immediate", Explicit: true}
	data, _ := json.Marshal(input)
	command := exec.CommandContext(ctx, binary, "--state-dir", dir, "--service-control", "repair", "--service-device", device)
	command.Stdin = bytes.NewReader(data)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Operation supervisor.Operation
		Error     string
	}
	if json.Unmarshal(output, &response) != nil || response.Error != "" {
		t.Fatal("trusted installer cannot establish missing fixed control entry", string(output))
	}
	for ctx.Err() == nil {
		op, e := supervisor.Query(ctx, dir, input.ID)
		if e == nil && op.Terminal() {
			if op.Phase != "applied" {
				t.Fatal(op)
			}
			old, e := supervisor.Query(ctx, dir, original.Input.ID)
			if e != nil || old.Phase != "blocked" || old.Error != original.Error {
				t.Fatal("repair lost original interrupted migration", old, e)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("cold installer did not restore acceptance")
}
