package mobileclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestEndpointPinsHostKeyAndQuotesWorkerPath(t *testing.T) {
	key := testSigner(t).PublicKey()
	endpoint := Endpoint{
		Address: "worker.example:22", User: "dev", HostFingerprint: ssh.FingerprintSHA256(key),
		WorkerPath: "/Applications/Shioji's test.app/shioji-worker",
	}
	if err := endpoint.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.verifyHostKey("", nil, key); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.verifyHostKey("", nil, testSigner(t).PublicKey()); err == nil {
		t.Fatal("accepted a replacement host key")
	}
	if got, want := endpoint.command(), `'/Applications/Shioji'\''s test.app/shioji-worker' --bridge`; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	endpoint.HostFingerprint = ""
	if err := endpoint.Validate(); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("unverified host: %v", err)
	}
}

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestHostInspectionStopsBeforeAuthentication(t *testing.T) {
	host := testSigner(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	auth := make(chan bool, 1)
	config := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		auth <- true
		return nil, fmt.Errorf("unexpected authentication")
	}}
	config.AddHostKey(host)
	server := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_, _, _, err = ssh.NewServerConn(conn, config)
		}
		server <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := Endpoint{Address: listener.Addr().String(), User: "owner"}
	fingerprint, err := e.InspectHost(ctx, nil)
	if err != nil || fingerprint != ssh.FingerprintSHA256(host.PublicKey()) {
		t.Fatalf("fingerprint = %q, err = %v", fingerprint, err)
	}
	if err := <-server; err == nil {
		t.Fatal("inspection authenticated")
	}
	select {
	case <-auth:
		t.Fatal("inspection used authentication")
	default:
	}
}

func TestDialAuthenticatesAndBridgesToWorker(t *testing.T) {
	host := testSigner(t)
	client := testSigner(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint := Endpoint{Address: listener.Addr().String(), User: "owner",
		HostFingerprint: ssh.FingerprintSHA256(host.PublicKey()), WorkerPath: DefaultWorkerPath}
	config := &ssh.ServerConfig{PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if metadata.User() != endpoint.User || ssh.FingerprintSHA256(key) != ssh.FingerprintSHA256(client.PublicKey()) {
			return nil, fmt.Errorf("unauthorized client")
		}
		return nil, nil
	}}
	config.AddHostKey(host)
	closed := make(chan error, 1)
	go func() {
		closed <- serveBridgeFixture(listener, config, endpoint.command(), func(channel ssh.Channel) error {
			msg := make([]byte, len("client request"))
			if _, err := io.ReadFull(channel, msg); err != nil || string(msg) != "client request" {
				return fmt.Errorf("request: %q, %v", msg, err)
			}
			_, err := io.WriteString(channel, "worker response")
			return err
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := endpoint.Dial(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	deadline, _ := ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stream, "client request"); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len("worker response"))
	if _, err := io.ReadFull(stream, out); err != nil || string(out) != "worker response" {
		t.Fatalf("response: %q, %v", out, err)
	}
	stream.Close()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("closing the protocol stream left the SSH session open")
	}
}

func serveBridgeFixture(listener net.Listener, config *ssh.ServerConfig, command string, bridge func(ssh.Channel) error) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	server, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return err
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	incoming := <-channels
	if incoming == nil || incoming.ChannelType() != "session" {
		return fmt.Errorf("missing SSH session")
	}
	channel, reqs, err := incoming.Accept()
	if err != nil {
		return err
	}
	defer channel.Close()
	req := <-reqs
	var payload struct{ Command string }
	if req == nil || req.Type != "exec" || ssh.Unmarshal(req.Payload, &payload) != nil || payload.Command != command {
		return fmt.Errorf("unexpected exec command: %q", payload.Command)
	}
	if err := req.Reply(true, nil); err != nil {
		return err
	}
	if err := bridge(channel); err != nil {
		return err
	}
	server.Wait()
	return nil
}
