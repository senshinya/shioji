package transport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupHostEntry(t *testing.T, name string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wire := binary.BigEndian.AppendUint32(nil, uint32(len("ssh-ed25519")))
	wire = append(wire, []byte("ssh-ed25519")...)
	wire = binary.BigEndian.AppendUint32(wire, uint32(len(pub)))
	wire = append(wire, pub...)
	return name + " ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire) + "\n"
}
func TestSSHHostTrustSavesOnlyReviewedKeyAndPreservesExistingEntries(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".ssh", "known_hosts")
	key := SSHHostKey{Target: "dev", Name: "[dev.example]:2222", File: file, Files: []string{file}, Entries: setupHostEntry(t, "[dev.example]:2222")}
	if err := key.Trust(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != key.Entries {
		t.Fatal("approved key was not saved", err)
	}
	key.Entries = setupHostEntry(t, key.Name)
	if err := key.Trust(context.Background()); err == nil {
		t.Fatal("replaced a host that became trusted after review")
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(got) {
		t.Fatal("existing trust changed")
	}
}
func TestSSHHostTrustRefusesAConcurrentTrustInAnotherConfiguredFile(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	name := "dev.example"
	existing := setupHostEntry(t, name)
	os.WriteFile(second, []byte(existing), 0600)
	key := SSHHostKey{Target: "dev", Name: name, File: first, Files: []string{first, second}, Entries: setupHostEntry(t, name)}
	if err := key.Trust(context.Background()); err == nil {
		t.Fatal("ignored the newly trusted key in another file")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatal("wrote unapproved replacement", err)
	}
}
func TestSSHSetupUsesStrictInteractiveKeyInstallation(t *testing.T) {
	args, err := (SSH{Target: "shinya@air", Port: 2222}).KeySetupCommand()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, want := range []string{"/usr/bin/ssh-copy-id", "StrictHostKeyChecking=yes", "BatchMode=no", "ForwardAgent=no", "-p\n2222", "shinya@air"} {
		if !strings.Contains(joined, want) {
			t.Fatal("missing setup protection", want, args)
		}
	}
	if _, err = (SSH{Target: "-oProxyCommand=bad"}).KeySetupCommand(); err == nil {
		t.Fatal("accepted injected target")
	}
}
func TestSSHKnownHostsUsesResolvedAliasAndRejectsAmbiguousPaths(t *testing.T) {
	cfg, err := parseSSHHostConfig("hostname real.example\nport 2222\nhostkeyalias dev\nuserknownhostsfile /Users/test/.ssh/known_hosts /Users/test/.ssh/other\nglobalknownhostsfile /etc/ssh/ssh_known_hosts\n")
	if err != nil || cfg.Name != "[dev]:2222" || cfg.User[0] != "/Users/test/.ssh/known_hosts" {
		t.Fatal(cfg, err)
	}
	if _, err = parseSSHHostConfig("hostname real.example\nport 22\nuserknownhostsfile /Users/Name With Spaces/.ssh/known_hosts\n"); err == nil {
		t.Fatal("guessed an ambiguous known_hosts destination")
	}
}

func TestSSHHostKeyScanUsesAnIsolatedRealServer(t *testing.T) {
	root := t.TempDir()
	keyFile := filepath.Join(root, "hostkey")
	if output, err := exec.Command("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyFile).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	port := freeCallbackPort(t)
	config := filepath.Join(root, "sshd_config")
	text := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\nStrictModes no\nLogLevel ERROR\n", port, keyFile, filepath.Join(root, "sshd.pid"))
	if err := os.WriteFile(config, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/sshd", "-D", "-e", "-f", config)
	diagnostic := &bytes.Buffer{}
	cmd.Stderr = diagnostic
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); cmd.Wait() }()
	ready := false
	for i := 0; i < 100; i++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("isolated SSH server did not listen")
	}
	file := filepath.Join(root, "known_hosts")
	cfg := sshHostConfig{Name: fmt.Sprintf("[127.0.0.1]:%d", port), User: []string{file}}
	ssh := SSH{Target: "127.0.0.1", Port: port}
	var required *SSHHostVerificationRequired
	if err := ssh.CheckAuthentication(ctx); !errors.As(err, &required) {
		t.Fatal("unknown host did not stop before account authentication", err)
	}
	reviewed, err := ssh.inspectHostKey(ctx, cfg)
	if err != nil || reviewed == nil || !strings.Contains(reviewed.Fingerprints, "SHA256:") {
		t.Fatal("unknown host was not offered for review", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("scan trusted a host without consent")
	}
	if err = reviewed.Trust(ctx); err != nil {
		t.Fatal(err)
	}
	if next, err := ssh.inspectHostKey(ctx, cfg); err != nil || next != nil {
		t.Fatal("trusted host asked again", err)
	}
	wrong := setupHostEntry(t, cfg.Name)
	os.WriteFile(file, []byte(wrong), 0600)
	if next, err := ssh.inspectHostKey(ctx, cfg); err == nil || next != nil {
		t.Fatal("changed host offered as new")
	}
	after, _ := os.ReadFile(file)
	if string(after) != wrong {
		t.Fatal("changed trust was overwritten")
	}
}
