package transport

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", script)
	cmd.Stdin = strings.NewReader(string(data))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	staged, err := os.ReadFile(filepath.Join(dir, "updates", digest, "shioji-worker"))
	if err != nil || string(staged) != string(data) {
		t.Fatal("upload changed bytes", err)
	}
	if strings.TrimSpace(string(output)) != digest {
		t.Fatal("invalid upload acknowledgement", string(output))
	}
	// A symlink must not redirect a later upload outside the worker state.
	other := t.TempDir()
	os.RemoveAll(filepath.Join(dir, "updates"))
	os.Symlink(other, filepath.Join(dir, "updates"))
	cmd = exec.Command("/bin/sh", "-c", script)
	cmd.Stdin = strings.NewReader(string(data))
	if err = cmd.Run(); err == nil {
		t.Fatal("symlink update root accepted")
	}
}
