package transport

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeRuntimeInstallsSignedBundleOnlyOnce(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "Shioji Runtime.app")
	macos := filepath.Join(pkg, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(helper, []byte(`package main
import ("fmt"; "os")
func main() {
	if len(os.Args) != 3 || os.Args[1] != "--action" || os.Args[2] != "register" { os.Exit(2) }
	fmt.Println("{\"format\":1,\"state\":\"enabled\"}")
}`), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-o", filepath.Join(macos, "shioji-runtime"), helper).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	info := []byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>dev.shioji.runtime</string><key>CFBundleExecutable</key><string>shioji-runtime</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleVersion</key><string>1</string></dict></plist>`)
	if err := os.WriteFile(filepath.Join(pkg, "Contents", "Info.plist"), info, 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", pkg).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	home := t.TempDir()
	opened := 0
	remote := RemoteWorker{Run: func(ctx context.Context, command string, in io.Reader, out, stderr io.Writer) (int, error) {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Env = append(cmd.Environ(), "HOME="+home)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
		err := cmd.Run()
		return cmd.ProcessState.ExitCode(), err
	}}
	artifact := func(ctx context.Context) (io.ReadCloser, string, error) {
		opened++
		return archiveRuntimeBundle(ctx, pkg)
	}
	for range 2 {
		result, err := remote.RuntimeRegistration(context.Background(), "register", artifact)
		if err != nil || result.State != "enabled" || opened != 1 {
			t.Fatalf("signed runtime install: %+v, %v, package opens=%d", result, err, opened)
		}
	}
	target := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", "Shioji Runtime.app")
	if err := verifyRuntimeBundle(context.Background(), target); err != nil {
		t.Fatal("installed runtime signature:", err)
	}
}

func TestNativeRuntimeStatusDoesNotStageAndRejectsPrivateDirectories(t *testing.T) {
	home := t.TempDir()
	working := filepath.Join(home, "Library", "Application Support", "Shioji", "worker")
	runs, opened := 0, 0
	remote := RemoteWorker{Directory: working, Run: func(ctx context.Context, command string, in io.Reader, out, stderr io.Writer) (int, error) {
		runs++
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Env = append(cmd.Environ(), "HOME="+home)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
		err := cmd.Run()
		return cmd.ProcessState.ExitCode(), err
	}}
	artifact := func(context.Context) (io.ReadCloser, string, error) {
		opened++
		return nil, "", errors.New("candidate unavailable")
	}
	status, err := remote.RuntimeRegistration(context.Background(), "status", artifact)
	if err != nil || status.State != "notInstalled" || runs != 1 || opened != 0 {
		t.Fatalf("read-only status prepared a mutation: %+v, %v, commands=%d, packages=%d", status, err, runs, opened)
	}
	status, err = remote.RuntimeRegistration(context.Background(), "register", artifact)
	if err == nil || !strings.Contains(err.Error(), "candidate unavailable") || opened != 1 {
		t.Fatalf("registration did not request the missing signed app: %+v, %v, packages=%d", status, err, opened)
	}
	remote.Directory = filepath.Join(home, "other-worker")
	status, err = remote.RuntimeRegistration(context.Background(), "register", artifact)
	if err != nil || status.State != "unavailable" || opened != 1 {
		t.Fatalf("isolated worker must not modify login items: %+v, %v, packages=%d", status, err, opened)
	}
}
