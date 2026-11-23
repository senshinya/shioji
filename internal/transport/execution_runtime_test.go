package transport

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/supervisor"
)

func TestExecutionRegistrationPreflightBindsWorkerAndLaunchAgent(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), runtimeBundleName)
	worker := filepath.Join(bundle, "Contents", "MacOS", "shioji-worker")
	plist := executionRuntimePlist(bundle)
	for _, name := range []string{worker, plist} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	contents := []byte("fixture candidate worker")
	if err := os.WriteFile(worker, contents, 0700); err != nil {
		t.Fatal(err)
	}
	build := fmt.Sprintf("%x", sha256.Sum256(contents))
	for _, tc := range []struct {
		label, processType, build string
		wantOK                    bool
	}{
		{"dev.shioji.runtime.agent", "Interactive", build, true},
		{"dev.shioji.network.agent", "Interactive", build, false},
		{"dev.shioji.runtime.agent", "Background", build, false},
		{"dev.shioji.runtime.agent", "Interactive", strings.Repeat("a", 64), false},
	} {
		data := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProcessType</key><string>%s</string></dict></plist>`, tc.label, tc.processType)
		if err := os.WriteFile(plist, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := verifyInteractiveExecutionRuntime(t.Context(), bundle, tc.build); (err == nil) != tc.wantOK {
			t.Fatalf("preflight on %q/%q for %q: %v", tc.label, tc.processType, tc.build, err)
		}
	}
}

func TestExecutionRegistrationResumesAfterUnregister(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, runtimeBundleName)
	bin := filepath.Join(bundle, "Contents", "MacOS", "shioji-runtime")
	if err := os.MkdirAll(filepath.Dir(bin), 0700); err != nil {
		t.Fatal(err)
	}
	// Exercise the same helper subprocess and persisted supervisor operation
	// as installation, with a private substitute for ServiceManagement.
	const helper = `#!/bin/sh
dir="$SHIOJI_TEST_REGISTRATION"
action="$2"
printf '%s\n' "$action" >> "$dir/calls"
case "$action" in
  unregister) printf 'notRegistered' > "$dir/state" ;;
  register)
    if [ ! -e "$dir/allow" ]; then
      printf '{"format":1,"state":"failed","message":"registration temporarily unavailable"}\n'
      exit 1
    fi
    printf 'enabled' > "$dir/state" ;;
esac
printf '{"format":1,"state":"%s"}\n' "$(/bin/cat "$dir/state")"
`
	t.Setenv("SHIOJI_TEST_REGISTRATION", dir)
	if err := os.WriteFile(bin, []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state"), []byte("enabled"), 0600); err != nil {
		t.Fatal(err)
	}
	op := supervisor.Operation{Legacy: true, Phase: "installing", DeviceID: "device",
		Input:                 domain.WorkerReplacement{ID: "original-installation", RuntimeHandoff: true},
		ExecutionRegistration: supervisor.RefreshExecutionRegistration}
	if err := completeExecutionRegistration(t.Context(), dir, bundle, &op); err == nil {
		t.Fatal("temporary registration failure was ignored")
	}
	resumed, ok, err := supervisor.PendingInstallation(dir)
	if err != nil || !ok || resumed.Input != op.Input || resumed.ExecutionRegistration != supervisor.RestoreExecutionRegistration {
		t.Fatalf("registration gap lost the resumable installation: %+v, %v", resumed, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "allow"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := completeExecutionRegistration(t.Context(), dir, bundle, &resumed); err != nil {
		t.Fatal(err)
	}
	if err := completeExecutionRegistration(t.Context(), dir, bundle, &resumed); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil || strings.TrimSpace(string(calls)) != "status\nunregister\nregister\nstatus\nregister\nstatus" {
		t.Fatalf("retry repeated or skipped a system action: %q, %v", calls, err)
	}
}

func TestExecutionRuntimePreparationPreservesRegisteredBundleAndDoesNotLaunch(t *testing.T) {
	home := t.TempDir()
	worker := filepath.Join(home, "Library", "Application Support", "Shioji", "worker")
	canonical := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", runtimeBundleName)
	for _, dir := range []string{worker, canonical} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(canonical, "existing-host")
	if err := os.WriteFile(marker, []byte("keep registered host"), 0600); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), runtimeBundleName)
	macos := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "main.go")
	// Any attempt to run, register, or query a native login host fails this
	// fixture. Preparation is allowed only its metadata command.
	program := `package main
import("fmt";"os")
func main(){if len(os.Args)!=2 || os.Args[1]!="--execution-describe"{os.Exit(91)};fmt.Println("{\"Version\":1,\"Role\":\"execution\"}")}`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-o", filepath.Join(macos, "shioji-runtime"), source).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>dev.shioji.runtime</string><key>CFBundleExecutable</key><string>shioji-runtime</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", bundle).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	remote := RemoteWorker{Directory: worker, Run: func(ctx context.Context, command string, input io.Reader, output, diagnostics io.Writer) (int, error) {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Env = append(cmd.Environ(), "HOME="+home)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, diagnostics
		err := cmd.Run()
		if cmd.ProcessState == nil {
			return -1, err
		}
		return cmd.ProcessState.ExitCode(), err
	}}
	for range 2 {
		if err := remote.PrepareExecutionRuntime(t.Context(), func(ctx context.Context) (io.ReadCloser, string, error) {
			return archiveRuntimeBundle(ctx, bundle)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep registered host" {
		t.Fatal("artifact staging replaced the registered host", string(content), err)
	}
	t.Setenv("HOME", home)
	if _, err := executionRuntime(t.Context(), worker); err != nil {
		t.Fatal("prepared execution host is not usable", err)
	}
}
