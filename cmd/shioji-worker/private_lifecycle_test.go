package main

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
	"shioji/internal/testutil"
	"shioji/internal/transport"
	"testing"
	"time"
)

func TestPrivateLifecycleSignedChildDrainsBeforeOrWithoutBusinessSocket(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "worker")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", bin).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	for _, early := range []bool{false, true} {
		name := "missing-business-socket"
		if early {
			name = "before-business-ready"
		}
		t.Run(name, func(t *testing.T) {
			p := testutil.NewProvider(t)
			dir, err := os.MkdirTemp("/tmp", "sh-private-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err != nil {
				t.Fatal(err)
			}
			unix.CloseOnExec(fds[0])
			unix.CloseOnExec(fds[1])
			parentFile := os.NewFile(uintptr(fds[0]), "test parent")
			childFile := os.NewFile(uintptr(fds[1]), "test child")
			parent, err := net.FileConn(parentFile)
			parentFile.Close()
			if err != nil {
				childFile.Close()
				t.Fatal(err)
			}
			defer parent.Close()
			cmd := exec.Command(bin, "--service-child", "--state-dir", dir, "--codex", transport.ExistingCodex())
			cmd.Env = append(os.Environ(), "CODEX_HOME="+p.Home, "SHIOJI_WORKER_INSTANCE=captured", "SHIOJI_WORKER_LIFECYCLE_FD=3")
			cmd.ExtraFiles = []*os.File{childFile}
			log, err := os.Create(filepath.Join(t.TempDir(), "worker.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd.Stdout, cmd.Stderr = log, log
			if err = cmd.Start(); err != nil {
				childFile.Close()
				t.Fatal(err)
			}
			childFile.Close()
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			settled := false
			defer func() {
				if !settled {
					_ = cmd.Process.Kill()
					<-exited
				}
			}()
			device := ""
			if !early {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for ctx.Err() == nil {
					conn, e := net.DialTimeout("unix", filepath.Join(dir, "worker.sock"), 50*time.Millisecond)
					if e == nil {
						client, e := protocol.Connect(ctx, conn, "")
						if e == nil {
							device = client.DeviceID
							if client.Worker.PrivateControl != supervisor.PrivateControlVersion {
								t.Fatal("child did not advertise private control")
							}
							client.Close()
							break
						}
						conn.Close()
					}
					time.Sleep(10 * time.Millisecond)
				}
				if device == "" {
					data, _ := os.ReadFile(log.Name())
					t.Fatal("business endpoint never became ready", string(data))
				}
				if err = os.Remove(filepath.Join(dir, "worker.sock")); err != nil {
					t.Fatal(err)
				}
			}
			parent.SetDeadline(time.Now().Add(10 * time.Second))
			in := supervisor.Control{Instance: "captured", OperationID: "private-stop", Replacement: domain.WorkerReplacement{Policy: "immediate", Explicit: true}}
			if err = json.NewEncoder(parent).Encode(in); err != nil {
				t.Fatal(err)
			}
			var reply struct {
				Result domain.WorkerUpdateResult `json:"result"`
				Error  string                    `json:"error"`
			}
			if err = json.NewDecoder(parent).Decode(&reply); err != nil || reply.Error != "" || !reply.Result.Ready {
				data, _ := os.ReadFile(log.Name())
				t.Fatal("private admission failed", reply, err, string(data))
			}
			select {
			case err = <-exited:
				settled = true
			case <-time.After(9 * time.Second):
				t.Fatal("child failed to drain")
			}
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 43 {
				data, _ := os.ReadFile(log.Name())
				t.Fatal("missing drained exit", err, string(data))
			}
			raw, err := os.ReadFile(filepath.Join(dir, "supervisor", "drained", "private-stop-captured.json"))
			if err != nil {
				t.Fatal(err)
			}
			var receipt supervisor.Drained
			if err = json.Unmarshal(raw, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.PID != cmd.Process.Pid || receipt.OperationID != "private-stop" || receipt.Instance != "captured" || receipt.DeviceID == "" || device != "" && receipt.DeviceID != device {
				t.Fatal("drain identity mismatch", receipt)
			}
		})
	}
}
