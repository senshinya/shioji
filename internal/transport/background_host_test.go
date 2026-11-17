package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/supervisor"
	"shioji/internal/testutil"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBackgroundHostUnregisterDoesNotWaitForWorkerShutdown(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "shioji-worker")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/shioji-worker").CombinedOutput(); err != nil {
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
	helperDir := t.TempDir()
	helperSource := filepath.Join(helperDir, "main.go")
	helperBin := filepath.Join(helperDir, "helper")
	const helperProgram = `package main
import("os";"path/filepath";"time";"fmt")
func main(){dir:=os.Getenv("SHIOJI_AUDIT_GATE_DIR");ready:=filepath.Join(dir,"helper-entered");if err:=os.WriteFile(ready+".tmp",[]byte(fmt.Sprint(os.Getpid())),0600);err!=nil{panic(err)};if err:=os.Rename(ready+".tmp",ready);err!=nil{panic(err)};if os.Getenv("SHIOJI_AUDIT_WAIT_FOR_HOST")=="1"{for{if _,err:=os.Stat(filepath.Join(dir,"release-helper"));err==nil{break};if _,err:=os.Stat(dir);os.IsNotExist(err){break};time.Sleep(10*time.Millisecond)}};fmt.Println("{\"format\":1,\"state\":\"notRegistered\"}")}`
	if err = os.WriteFile(helperSource, []byte(helperProgram), 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command("go", "build", "-o", helperBin, helperSource).CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	helperBytes, err := os.ReadFile(helperBin)
	if err != nil {
		t.Fatal(err)
	}
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, wait := range []bool{false, true} {
		t.Run(map[bool]string{false: "helper-immediate", true: "helper-awaits-host-exit"}[wait], func(t *testing.T) {
			home, err := os.MkdirTemp("/tmp", "h-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(home)
			home, err = filepath.EvalSymlinks(home)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("SHIOJI_AUDIT_GATE_DIR", home)
			if wait {
				t.Setenv("SHIOJI_AUDIT_WAIT_FOR_HOST", "1")
			} else {
				t.Setenv("SHIOJI_AUDIT_WAIT_FOR_HOST", "0")
			}
			target := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", "Shioji Runtime.app")
			macos := filepath.Join(target, "Contents", "MacOS")
			if err = os.MkdirAll(macos, 0755); err != nil {
				t.Fatal(err)
			}
			plist := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>dev.shioji.runtime</string><key>CFBundleExecutable</key><string>shioji-runtime</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1</string></dict></plist>`
			if err = os.WriteFile(filepath.Join(target, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(macos, "shioji-runtime"), helperBytes, 0755); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"--force", "--deep", "--sign", "-", target}, {"--verify", "--deep", "--strict", target}} {
				if out, e := exec.Command("/usr/bin/codesign", args...).CombinedOutput(); e != nil {
					t.Fatal(e, string(out))
				}
			}
			dir := filepath.Join(home, "Library", "Application Support", "Shioji", "worker")
			if err = os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			db, e := store.Open(filepath.Join(dir, "state.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			db.Close()
			provider := testutil.NewProvider(t)
			t.Setenv("CODEX_HOME", provider.Home)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hostDone := make(chan error, 1)
			go func() { hostDone <- RunWorkerSupervisor(ctx, dir, bin, codex, true) }()
			settled := false
			defer func() {
				os.WriteFile(filepath.Join(home, "release-helper"), []byte("release"), 0600)
				cancel()
				if !settled {
					select {
					case <-hostDone:
					case <-time.After(12 * time.Second):
						t.Error("isolated host did not exit")
					}
				}
			}()
			deadline := time.Now().Add(15 * time.Second)
			for {
				op, err := supervisor.Query(ctx, dir, "")
				if err == nil && op.Phase == "ready" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("host not ready", op, err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			request, cancelRequest := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancelRequest()
			client, e := protocol.Dial(request, func(ctx context.Context) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
			}, "")
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			disableDone := make(chan error, 1)
			go func() {
				var state domain.BackgroundService
				err := client.Call(request, "worker.background.stop", protocol.BackgroundStopInput{ID: "isolated-stop"}, &state)
				if err == nil && state.State != "disabling" {
					err = fmt.Errorf("unexpected acceptance: %+v", state)
				}
				disableDone <- err
			}()
			deadline = time.Now().Add(12 * time.Second)
			for {
				if _, e = os.Stat(filepath.Join(home, "helper-entered")); e == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("real isolated helper did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}

			// Capture the exact helper identity before releasing or deleting its temporary home.
			pidRaw, e := os.ReadFile(filepath.Join(home, "helper-entered"))
			if e != nil {
				t.Fatal(e)
			}
			helperPID, e := strconv.Atoi(strings.TrimSpace(string(pidRaw)))
			if e != nil {
				t.Fatal(e)
			}
			processes, e := managedexec.Processes()
			if e != nil {
				t.Fatal(e)
			}
			var helperIdentity managedexec.ProcessInfo
			for _, proc := range processes {
				if proc.Identity.PID == helperPID {
					helperIdentity = proc
					break
				}
			}
			if wait && helperIdentity.Identity.PID == 0 {
				t.Fatal("waiting helper identity not observed")
			}
			defer func() {
				os.WriteFile(filepath.Join(home, "release-helper"), []byte("release"), 0600)
				if helperIdentity.Identity.PID == 0 {
					return
				}
				deadline := time.Now().Add(2 * time.Second)
				for managedexec.ProcessState(helperIdentity) == "running" && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if managedexec.ProcessState(helperIdentity) == "running" {
					_ = managedexec.SignalExact(helperIdentity, unix.SIGKILL)
					t.Error("isolated helper needed exact-identity cleanup")
				}
			}()
			select {
			case e = <-disableDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("worker RPC waited for native helper")
			}
			// Model the service manager stopping only this isolated host, without SMAppService.
			started := time.Now()
			cancel()
			var stopErr error
			select {
			case stopErr = <-hostDone:
				settled = true
			case <-time.After(12 * time.Second):
				t.Fatal("host teardown did not finish")
			}
			receipt, e := supervisor.Query(context.Background(), dir, "")
			if e != nil {
				t.Fatal(e)
			}
			os.WriteFile(filepath.Join(home, "release-helper"), []byte("release"), 0600)
			t.Logf("signedWorker=%s privateControl=%d helperWaitsForHost=%v hostStopMs=%d hostStopError=%v durablePhase=%s", described.Worker.BuildID, described.Worker.PrivateControl, wait, time.Since(started).Milliseconds(), stopErr, receipt.Phase)
			if stopErr != nil || receipt.Phase == "blocked" {
				t.Error("unregister helper prevented an idle real worker from producing its drained receipt")
			}
		})
	}
}
