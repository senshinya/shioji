package transport

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
