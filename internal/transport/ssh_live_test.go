package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSSHLiveRejectsChangedHostKeyBeforeRemoteCommand(t *testing.T) {
	target := os.Getenv("SHIOJI_TEST_SSH_TARGET")
	if target == "" {
		t.Skip("explicit SSH target required for live host-key rejection")
	}
	host := target
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wire := []byte{}
	wire = binary.BigEndian.AppendUint32(wire, uint32(len("ssh-ed25519")))
	wire = append(wire, []byte("ssh-ed25519")...)
	wire = binary.BigEndian.AppendUint32(wire, uint32(len(pub)))
	wire = append(wire, pub...)
	known := filepath.Join(t.TempDir(), "known_hosts")
	if err = os.WriteFile(known, []byte(host+" ssh-ed25519 "+base64.StdEncoding.EncodeToString(wire)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd, err := (SSH{Target: target}).command(ctx, "/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	index := 0
	for i, arg := range cmd.Args {
		if arg == "--" {
			index = i
			break
		}
	}
	args := append([]string{}, cmd.Args[:index]...)
	args = append(args, "-S", "none", "-o", "UserKnownHostsFile="+known, "-o", "GlobalKnownHostsFile=/dev/null")
	cmd.Args = append(args, cmd.Args[index:]...)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 255 || !strings.Contains(string(output), "Host key verification failed") {
		t.Fatalf("changed host key was not specifically rejected: %v %s", err, output)
	}
}
