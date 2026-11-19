package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteWorkerStagingResumesFromReceivedOffset(t *testing.T) {
	data, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	script, err := sshWorkerStageScript(dir, digest)
	if err != nil {
		t.Fatal(err)
	}
	run := func(input []byte) (string, error) {
		cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", script, "stage", fmt.Sprint(len(data)))
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.Output()
		return string(out), err
	}
	cut := len(data) / 2
	if _, err := run(data[:cut]); err == nil {
		t.Fatal("a truncated transfer reported success")
	}
	path := filepath.Join(dir, "updates", digest, "shioji-worker")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a truncated transfer published a worker: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", script, "stage", fmt.Sprint(len(data)))
	if err := transferWorker(t.Context(), cmd, digest, bytes.NewReader(data), int64(len(data)), nil); err != nil {
		t.Fatalf("reconnected transfer did not resume at %d: %v", cut, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("reconnected transfer did not publish the verified bytes", err)
	}
	out, err := run(nil)
	if err != nil || out != fmt.Sprintf("%d\n%s\n", len(data), digest) {
		t.Fatalf("verified artifact was not reused: %q, %v", out, err)
	}
}

func TestRemoteWorkerStagingVerifiesArtifactWithoutExecutingIt(t *testing.T) {
	data, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	dir := filepath.Join(t.TempDir(), "worker ' $(touch injected)")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	script, err := sshWorkerStageScript(dir, digest)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", script, "stage", fmt.Sprint(len(data)))
	cmd.Stdin = strings.NewReader(string(data))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	staged, err := os.ReadFile(filepath.Join(dir, "updates", digest, "shioji-worker"))
	if err != nil || string(staged) != string(data) {
		t.Fatal("upload changed bytes", err)
	}
	if string(output) != "0\n"+digest+"\n" {
		t.Fatal("invalid upload acknowledgement", string(output))
	}
	// A symlink must not redirect a later upload outside the worker state.
	other := t.TempDir()
	os.RemoveAll(filepath.Join(dir, "updates"))
	os.Symlink(other, filepath.Join(dir, "updates"))
	cmd = exec.Command("/bin/sh", "-c", script, "stage", fmt.Sprint(len(data)))
	cmd.Stdin = strings.NewReader(string(data))
	if err = cmd.Run(); err == nil {
		t.Fatal("symlink update root accepted")
	}
}
