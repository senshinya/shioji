package mobileclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

func TestMobileBootstrapRecognizesAuthenticatedLegacyOwner(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	host, user := testSigner(t), testSigner(t)
	server := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if ssh.FingerprintSHA256(key) != ssh.FingerprintSHA256(user.PublicKey()) {
			return nil, errors.New("SSH login not authorized")
		}
		return nil, nil
	}}
	server.AddHostKey(host)
	endpoint := Endpoint{Address: bootstrapFixtureSSH(t, ctx, server, home), User: "fixture",
		HostFingerprint: ssh.FingerprintSHA256(host.PublicKey()), WorkerPath: filepath.Join(home, "shioji-worker"), StateDirectory: home}
	installer := func(context.Context) (io.ReadCloser, string, error) {
		t.Error("an existing legacy worker must never trigger a fresh install")
		return nil, "", errors.New("unexpected install")
	}
	for _, test := range []struct {
		name  string
		reply any
		owner string
	}{
		{"legacy frame", protocol.Frame{Type: "worker-legacy", Major: 1, Minor: 3, DeviceID: "legacy-device"}, ""},
		{"verified info", transport.WorkerInfo{Directory: home, Worker: endpoint.WorkerPath, DeviceID: "legacy-device",
			Legacy: &domain.LegacyWorkerInfo{DeviceID: "legacy-device", Major: 1, Minor: 3, OwnerRef: "old-owner", Supported: true}}, "old-owner"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.reply)
			if err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\n[ \"$1\" = --ensure ] || exit 93\nprintf '%s\\n' " + quote(string(raw)) + "\n"
			if err = os.WriteFile(endpoint.WorkerPath, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", endpoint.WorkerPath).CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
			verified, info, err := endpoint.Bootstrap(ctx, user, nil, installer)
			if err != nil || verified.DeviceID != "legacy-device" || info.Legacy == nil || info.Legacy.OwnerRef != test.owner {
				t.Fatalf("old service did not reach the migration flow: %+v %v", info, err)
			}
			foreign := endpoint
			foreign.DeviceID = "another-enrolled-device"
			if _, _, err := foreign.Bootstrap(ctx, user, nil, installer); err == nil {
				t.Fatal("known route accepted a different old service")
			}
		})
	}
}

// Exercises the actual signed package, isolated shell, SSH wire exchange and
// private worker. No system SSH listener or installed Shioji is needed.
func TestPackagedMobileWorkerInstallsOverPinnedSSHAndReusesIdentity(t *testing.T) {
	asset := os.Getenv("SHIOJI_MOBILE_BOOTSTRAP_ASSET")
	if asset == "" {
		t.Skip("set SHIOJI_MOBILE_BOOTSTRAP_ASSET to the packaged simulator resource")
	}
	home, err := os.MkdirTemp("/tmp", "shioji-mobile-bootstrap-")
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, "state")
	t.Cleanup(func() { stopBootstrapFixture(t, home); os.RemoveAll(home) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	host, user := testSigner(t), testSigner(t)
	server := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if metadata.User() != "fixture" || ssh.FingerprintSHA256(key) != ssh.FingerprintSHA256(user.PublicKey()) {
			return nil, errors.New("SSH login not authorized")
		}
		return nil, nil
	}}
	server.AddHostKey(host)
	endpoint := Endpoint{Address: bootstrapFixtureSSH(t, ctx, server, home), User: "fixture",
		HostFingerprint: ssh.FingerprintSHA256(host.PublicKey()), WorkerPath: filepath.Join(home, "selected", "shioji-worker"), StateDirectory: state}
	opened := 0
	installer := func(context.Context) (io.ReadCloser, string, error) {
		opened++
		return OpenWorkerArtifact(asset)
	}
	if _, _, err := endpoint.Bootstrap(ctx, user, nil, nil); !errors.Is(err, transport.ErrWorkerMissing) {
		t.Fatalf("authenticated probe did not report missing worker: %v", err)
	}
	other := endpoint
	other.DeviceID = "an-already-enrolled-device"
	if _, _, err := other.Bootstrap(ctx, user, nil, installer); !errors.Is(err, transport.ErrWorkerMissing) || opened != 0 {
		t.Fatalf("known route could install a new device: %v (opened %d times)", err, opened)
	}
	installed, info, err := endpoint.Bootstrap(ctx, user, nil, installer)
	if err != nil {
		t.Fatal(err)
	}
	if installed.DeviceID == "" || installed.StateDirectory != state || !info.Started || info.Build == nil || opened != 1 {
		t.Fatalf("invalid install result: %+v, %+v (opened %d times)", installed, info, opened)
	}
	viaTailnet := endpoint
	viaTailnet.TailnetPeerID = "paired-peer"
	routeCalls := 0
	route := func(ctx context.Context, peer, address string) (net.Conn, error) {
		if peer != viaTailnet.TailnetPeerID || address != endpoint.Address {
			return nil, errors.New("wrong destination for routed SSH")
		}
		routeCalls++
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	verified, existing, err := viaTailnet.Bootstrap(ctx, user, route, installer)
	if err != nil || existing.Build == nil || info.Build.BuildID != existing.Build.BuildID || existing.Started || opened != 1 {
		t.Fatalf("lost original installation on retry: %+v (%+v), %v (opened %d)", verified, existing, err, opened)
	}
	if verified.DeviceID != installed.DeviceID || verified.StateDirectory != installed.StateDirectory || routeCalls != 2 {
		t.Fatalf("alternate path changed device or bypassed dialer: %+v (%d dials)", verified, routeCalls)
	}
	client, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) { return installed.Dial(ctx, user) }, installed.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var snapshot domain.Snapshot
	if err = client.Call(ctx, "snapshot", nil, &snapshot); err != nil || snapshot.DeviceID != installed.DeviceID {
		t.Fatalf("reconnect did not reach installed worker: %+v, %v", snapshot, err)
	}
}

func bootstrapFixtureSSH(t *testing.T, ctx context.Context, config *ssh.ServerConfig, home string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var clients sync.WaitGroup
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			clients.Go(func() {
				defer raw.Close()
				stop := context.AfterFunc(ctx, func() { raw.Close() })
				defer stop()
				conn, channels, requests, err := ssh.NewServerConn(raw, config)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				incoming := <-channels
				if incoming == nil {
					return
				}
				channel, reqs, err := incoming.Accept()
				if err != nil {
					return
				}
				defer channel.Close()
				req := <-reqs
				var payload struct{ Command string }
				if req == nil || req.Type != "exec" || ssh.Unmarshal(req.Payload, &payload) != nil {
					return
				}
				req.Reply(true, nil)
				remote := strings.ReplaceAll(payload.Command, "/Applications/Shioji.app", home+"/Applications/Shioji.app")
				cmd := exec.CommandContext(ctx, "/bin/sh", "-c", remote)
				cmd.Env = append(os.Environ(), "HOME="+home)
				cmd.Stdin, cmd.Stdout, cmd.Stderr = channel, channel, channel.Stderr()
				_ = cmd.Run()
				channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{uint32(cmd.ProcessState.ExitCode())}))
			})
		}
	}()
	t.Cleanup(func() { listener.Close(); <-closed; clients.Wait() })
	return listener.Addr().String()
}

func stopBootstrapFixture(t *testing.T, home string) {
	t.Helper()
	data, _ := exec.Command("/bin/ps", "-axo", "pid=,args=").Output()
	for _, line := range strings.Split(string(data), "\n") {
		pidText, args, _ := strings.Cut(strings.TrimSpace(line), " ")
		pid, _ := strconv.Atoi(pidText)
		if pid <= 1 || !strings.HasPrefix(strings.TrimSpace(args), home+"/") || strings.Contains(args, "--service-child") {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Error(err)
		}
		for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
			if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}
