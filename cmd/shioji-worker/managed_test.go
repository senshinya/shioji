package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestManagedLauncherWaitsForDurableAckAndRetainsGroupAfterRootExit(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "worker")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	cmd := exec.Command(binary, "--managed-exec", "fixture-launch")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	rootPID := cmd.Process.Pid
	rootIdentity, err := unix.SysctlKinfoProc("kern.proc.pid", rootPID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		p, e := unix.SysctlKinfoProc("kern.proc.pid", rootPID)
		if e == nil && p.Proc.P_starttime == rootIdentity.Proc.P_starttime {
			syscall.Kill(-rootPID, syscall.SIGKILL)
		}
		cmd.Wait()
	}()
	registration := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); registration <- line }()
	var line string
	select {
	case line = <-registration:
	case <-time.After(5 * time.Second):
		t.Fatal("launcher did not reach registration gate")
	}
	if !strings.HasPrefix(line, "SHIOJI_LAUNCH ") {
		t.Fatal("launcher did not reach registration gate", line, stderr.String())
	}
	var packet struct {
		ID           string
		Root, Anchor int
	}
	if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "SHIOJI_LAUNCH ")), &packet); err != nil {
		t.Fatal(err)
	}
	if packet.ID != "fixture-launch" || packet.Root != rootPID || packet.Anchor <= 1 {
		t.Fatal("invalid launch identity", packet)
	}
	anchor, err := unix.SysctlKinfoProc("kern.proc.pid", packet.Anchor)
	if err != nil {
		t.Fatal(err)
	}
	if int(anchor.Eproc.Pgid) != rootPID || int(anchor.Eproc.Ppid) != rootPID {
		t.Fatal("anchor left root group")
	}
	defer func() {
		p, e := unix.SysctlKinfoProc("kern.proc.pid", packet.Anchor)
		if e == nil && p.Proc.P_starttime == anchor.Proc.P_starttime {
			syscall.Kill(-rootPID, syscall.SIGKILL)
		}
	}()
	marker := filepath.Join(dir, "executed")
	time.Sleep(100 * time.Millisecond)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("user command executed before ACK")
	}
	ack := map[string]any{"ID": packet.ID, "Argv": []string{"/bin/sh", "-c", "printf done > \"$1\"", "probe", marker}, "Env": []string{"PATH=/usr/bin:/bin"}, "PipeStdin": false}
	if err = json.NewEncoder(stdin).Encode(ack); err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	// Anchor must not retain stdout/stderr/PTY handles after the root exits.
	drained := make(chan struct{})
	go func() { io.Copy(io.Discard, stdout); close(drained) }()
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("anchor retained user output pipes")
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal(err, stderr.String())
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "done" {
		t.Fatal("ACK did not release original command", err)
	}
	p, err := unix.SysctlKinfoProc("kern.proc.pid", packet.Anchor)
	if err != nil || p.Proc.P_stat == 5 {
		t.Fatal("anchor did not survive root exit", err)
	}
	if err = syscall.Kill(-rootPID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	p, err = unix.SysctlKinfoProc("kern.proc.pid", packet.Anchor)
	if err != nil || p.Proc.P_stat == 5 {
		t.Fatal("anchor died before forced cleanup could verify group", err)
	}
}
