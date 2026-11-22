package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/protocol"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("SHIOJI_BOOTSTRAP_FIXTURE") == "1" && len(os.Args) > 1 && os.Args[1] == "--describe" {
		exe, _ := os.Executable()
		built := int64(42)
		if b, e := os.ReadFile(exe + ".built-at"); e == nil {
			built, _ = strconv.ParseInt(string(b), 10, 64)
		}
		if built == 0 {
			fmt.Println(`{"type":"worker-build","worker":{}}`)
		} else {
			fmt.Printf("{\"type\":\"worker-build\",\"worker\":{\"builtAt\":%d}}\n", built)
		}
		os.Exit(0)
	}
	if os.Getenv("SHIOJI_BOOTSTRAP_FIXTURE") == "1" && len(os.Args) > 1 && os.Args[1] == "--ensure" {
		if reject := os.Getenv("SHIOJI_BOOTSTRAP_REJECT"); reject != "" {
			fmt.Fprintln(os.Stderr, "existing worker unavailable")
			exit, _ := strconv.Atoi(reject)
			os.Exit(exit)
		}
		exe, _ := os.Executable()
		dir := ""
		for i, a := range os.Args {
			if a == "--state-dir" && i+1 < len(os.Args) {
				dir = os.Args[i+1]
			}
		}
		if dir == "" {
			dir = filepath.Join(os.Getenv("SHIOJI_BOOTSTRAP_HOME"), "Library/Application Support/Shioji/worker")
		}
		json.NewEncoder(os.Stdout).Encode(WorkerInfo{Home: os.Getenv("SHIOJI_BOOTSTRAP_HOME"), Directory: dir, Worker: exe, DeviceID: "existing-device", Minor: 35})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func bootstrapFixture(t *testing.T) (SSH, string, func(context.Context, string) (*exec.Cmd, error), *int) {
	t.Helper()
	root := t.TempDir()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(root, "installed worker's folder", "shioji-worker")
	if err = os.MkdirAll(filepath.Dir(worker), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(worker, data, 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "state ' $(touch injected)")
	calls := new(int)
	command := func(ctx context.Context, remote string) (*exec.Cmd, error) {
		*calls++
		// Model the remote user's home and application directory without touching
		// this Mac's installed App, credentials, worker, or network services.
		remote = strings.ReplaceAll(remote, "$HOME", root)
		remote = strings.ReplaceAll(remote, "/Applications/Shioji.app", root+"/Applications/Shioji.app")
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", remote)
		cmd.Env = append(os.Environ(), "SHIOJI_BOOTSTRAP_FIXTURE=1", "SHIOJI_BOOTSTRAP_HOME="+root)
		return cmd, nil
	}
	return SSH{Target: "fixture", Worker: worker, StateDirectory: dir}, root, command, calls
}

func TestSSHBootstrapReusesExistingWorkerWithoutReadingOrUploadingClientBinary(t *testing.T) {
	ssh, root, command, calls := bootstrapFixture(t)
	info, err := ssh.bootstrap(context.Background(), filepath.Join(root, "client-does-not-have-a-worker"), command)
	if err != nil {
		t.Fatal("existing device unnecessarily required a local deployment artifact:", err)
	}
	if info.DeviceID != "existing-device" || info.Worker != ssh.Worker || info.Directory != ssh.StateDirectory {
		t.Fatal("existing identity/path changed", info)
	}
	if *calls != 1 {
		t.Fatal("existing device started an upload", *calls)
	}
	if _, err = os.Stat(filepath.Join(root, "Library/Application Support/Shioji/Runtime/development")); !os.IsNotExist(err) {
		t.Fatal("existing device created deployment files", err)
	}
}

func TestWorkerBootstrapWithoutInstallerReportsOnlyVerifiedMissingWorker(t *testing.T) {
	ssh, root, command, calls := bootstrapFixture(t)
	if err := os.Remove(ssh.Worker); err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, remote string, input io.Reader, stdout, stderr io.Writer) (int, error) {
		cmd, err := command(ctx, remote)
		if err != nil {
			return -1, err
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = input, stdout, stderr
		err = cmd.Run()
		return cmd.ProcessState.ExitCode(), err
	}
	_, err := BootstrapWorker(context.Background(), ssh.Worker, ssh.StateDirectory, run, nil)
	if !errors.Is(err, ErrWorkerMissing) || *calls != 1 {
		t.Fatalf("probe = %v after %d commands", err, *calls)
	}
	if _, err = os.Stat(filepath.Join(root, "Library/Application Support/Shioji/Runtime/development")); !os.IsNotExist(err) {
		t.Fatal("a probe deployed files", err)
	}
}

func TestSSHBootstrapDoesNotRedeployAfterExistingWorkerFailure(t *testing.T) {
	ssh, root, command, calls := bootstrapFixture(t)
	run := func(ctx context.Context, remote string) (*exec.Cmd, error) {
		cmd, err := command(ctx, remote)
		if cmd != nil {
			cmd.Env = append(cmd.Env, "SHIOJI_BOOTSTRAP_REJECT=23")
		}
		return cmd, err
	}
	_, err := ssh.bootstrap(context.Background(), ssh.Worker, run)
	if err == nil || !strings.Contains(err.Error(), "existing worker unavailable") {
		t.Fatal("existing service failure was hidden", err)
	}
	if *calls != 1 {
		t.Fatal("existing worker error triggered deployment", *calls)
	}
	if _, err = os.Stat(filepath.Join(root, "Library/Application Support/Shioji/Runtime/development")); !os.IsNotExist(err) {
		t.Fatal("failed probe created a deployment", err)
	}
}

func TestSSHBootstrapFindsInstalledAndSelectedWorker(t *testing.T) {
	for _, kind := range []string{"app", "selected", "standalone", "unknown-time", "unknown-time-stdout-error"} {
		t.Run(kind, func(t *testing.T) {
			ssh, root, command, calls := bootstrapFixture(t)
			data, _ := os.ReadFile(ssh.Worker)
			switch kind {
			case "app":
				ssh.Worker = filepath.Join(root, "Applications/Shioji.app/Contents/MacOS/shioji-worker")
			case "selected":
				if e := os.MkdirAll(ssh.StateDirectory, 0700); e != nil {
					t.Fatal(e)
				}
				digest := strings.Repeat("a", 64)
				if e := SaveActiveWorker(ssh.StateDirectory, digest); e != nil {
					t.Fatal(e)
				}
				ssh.Worker = filepath.Join(ssh.StateDirectory, "updates", digest, "shioji-worker")
			case "standalone":
				ssh.Worker = filepath.Join(root, "Library/Application Support/Shioji/Runtime/development/older", "shioji-worker")
				os.MkdirAll(filepath.Dir(ssh.Worker), 0700)
				os.WriteFile(ssh.Worker, data, 0700)
				os.WriteFile(ssh.Worker+".built-at", []byte("10"), 0600)
				ssh.Worker = filepath.Join(root, "Library/Application Support/Shioji/Runtime/development/newer", "shioji-worker")
			case "unknown-time", "unknown-time-stdout-error":
				ssh.Worker = filepath.Join(root, "Library/Application Support/Shioji/Runtime/development/unknown", "shioji-worker")
			}
			want := ssh.Worker
			if e := os.MkdirAll(filepath.Dir(want), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(want, data, 0700); e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(kind, "unknown-time") {
				os.WriteFile(want+".built-at", []byte("0"), 0600)
			}
			ssh.Worker = ""
			if kind == "unknown-time-stdout-error" {
				// Older plutil versions can emit extraction errors on stdout.
				// Exercise that command boundary on newer macOS too.
				plutil := filepath.Join(root, "plutil")
				script := "#!/bin/sh\nif [ \"$2\" = worker.builtAt ]; then echo 'Could not extract value'; exit 1; fi\nexec /usr/bin/plutil \"$@\"\n"
				if err := os.WriteFile(plutil, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				original := command
				command = func(ctx context.Context, remote string) (*exec.Cmd, error) {
					return original(ctx, strings.ReplaceAll(remote, "/usr/bin/plutil", plutil))
				}
			}
			info, e := ssh.bootstrap(context.Background(), "/no-client-worker", command)
			if e != nil || info.Worker != want || *calls != 1 {
				t.Fatal(info, e, *calls)
			}
		})
	}
}

func TestSSHBootstrapDeploysOnlyMissingWorkerAndThenReusesArtifact(t *testing.T) {
	ssh, root, command, calls := bootstrapFixture(t)
	local := ssh.Worker
	ssh.Worker = ""
	run := func(ctx context.Context, remote string) (*exec.Cmd, error) {
		cmd, e := command(ctx, remote)
		if *calls == 2 { // A slow transfer must outlive the connection-only timeout.
			args := cmd.Args
			cmd = exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 10.1; exec /bin/sh -c "+shellQuote(args[len(args)-1]))
			cmd.Env = append(os.Environ(), "SHIOJI_BOOTSTRAP_FIXTURE=1", "SHIOJI_BOOTSTRAP_HOME="+root)
		}
		return cmd, e
	}
	start := time.Now()
	info, e := ssh.bootstrap(context.Background(), local, run)
	if e != nil || *calls != 2 || info.Directory != ssh.StateDirectory {
		t.Fatal(info, e, *calls)
	}
	if time.Since(start) < 10*time.Second {
		t.Fatal("slow deployment was not exercised")
	}
	first := info.Worker
	*calls = 0
	info, e = ssh.bootstrap(context.Background(), "/no-client-worker", command)
	if e != nil || info.Worker != first || *calls != 1 {
		t.Fatal("cached deployment uploaded again", info, e, *calls)
	}
}

func TestSSHBootstrapCancellationStopsDeployment(t *testing.T) {
	ssh, root, command, calls := bootstrapFixture(t)
	local := ssh.Worker
	ssh.Worker = ""
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := filepath.Join(root, "transfer-started")
	go func() {
		for ctx.Err() == nil {
			if _, e := os.Stat(ready); e == nil {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	run := func(ctx context.Context, remote string) (*exec.Cmd, error) {
		cmd, e := command(ctx, remote)
		if *calls == 2 {
			cmd = exec.CommandContext(ctx, "/bin/sh", "-c", "touch "+shellQuote(ready)+"; exec /bin/sleep 30")
		}
		return cmd, e
	}
	start := time.Now()
	if _, e := ssh.bootstrap(ctx, local, run); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled deployment did not report cancellation", e)
	}
	if *calls != 2 || time.Since(start) > 2*time.Second {
		t.Fatal("cancel did not stop deployment", *calls, time.Since(start))
	}
}

func TestSSHBootstrapDoesNotTreatArbitraryExit44AsMissing(t *testing.T) {
	ssh, _, command, calls := bootstrapFixture(t)
	run := func(ctx context.Context, remote string) (*exec.Cmd, error) {
		cmd, e := command(ctx, remote)
		cmd.Env = append(cmd.Env, "SHIOJI_BOOTSTRAP_REJECT=44")
		return cmd, e
	}
	if _, e := ssh.bootstrap(context.Background(), ssh.Worker, run); e == nil || *calls != 1 {
		t.Fatal("existing worker failure triggered fallback", e, *calls)
	}
}

func TestSSHBootstrapRejectsInvalidExistingArtifactWithoutUpload(t *testing.T) {
	ssh, _, command, calls := bootstrapFixture(t)
	if e := os.WriteFile(ssh.Worker, []byte("not a signed worker"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := ssh.bootstrap(context.Background(), "/no-client-worker", command); e == nil || *calls != 1 {
		t.Fatal("invalid signature was treated as missing", e, *calls)
	}
}

func TestSSHBootstrapDoesNotDeployOverUnidentifiedExistingEndpoint(t *testing.T) {
	ssh, _, command, calls := bootstrapFixture(t)
	ssh.Worker = ""
	if e := os.MkdirAll(ssh.StateDirectory, 0700); e != nil {
		t.Fatal(e)
	}
	// Even a stale endpoint is existing state to diagnose, not deployment consent.
	if e := os.WriteFile(filepath.Join(ssh.StateDirectory, "worker.sock"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ssh.bootstrap(context.Background(), "/no-client-worker", command); e == nil || *calls != 1 {
		t.Fatal("unidentified endpoint triggered deployment", e, *calls)
	}
}

func TestSSHBootstrapRejectsUnsafeStateBeforeDeployment(t *testing.T) {
	ssh, root, command, calls := bootstrapFixture(t)
	ssh.Worker = ""
	if e := os.Symlink(root, ssh.StateDirectory); e != nil {
		t.Fatal(e)
	}
	if _, e := ssh.bootstrap(context.Background(), "/no-client-worker", command); e == nil || *calls != 1 {
		t.Fatal("symlink state triggered deployment", e, *calls)
	}
}

// Opt-in, read-only check of discovery against an already-running native owner.
// It deliberately has no deployable client binary and changes no SSH trust.
func TestSSHBootstrapLiveLocalOwner(t *testing.T) {
	dir := os.Getenv("SHIOJI_TEST_EXISTING_WORKER_DIR")
	if dir == "" {
		t.Skip("explicit existing worker required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, e := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
	if e != nil {
		t.Fatal(e)
	}
	client, e := protocol.Connect(ctx, conn, "")
	if e != nil {
		conn.Close()
		t.Fatal(e)
	}
	defer client.Close()
	command := func(ctx context.Context, remote string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "/bin/sh", "-c", remote), nil
	}
	info, e := (SSH{Target: "local-read-only", StateDirectory: dir}).bootstrap(ctx, "/no-deployable-client-worker", command)
	if e != nil || info.DeviceID != client.DeviceID || info.Build == nil || client.Worker == nil || info.Build.BuildID != client.Worker.BuildID || info.Started {
		t.Fatal("did not reuse the actual live owner", info, e)
	}
	t.Log("reused native owner, device identity and build; no upload or restart", info.Worker)
}
