package mobileclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"shioji/internal/domain"
)

func TestAgentBrowserCallbackUsesPinnedDirectSSHChannel(t *testing.T) {
	const payload = "code=fixture&state=original"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.RawQuery)
	}))
	defer server.Close()
	local, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	callback := domain.LoopbackEndpoint{Host: "127.0.0.1", Port: local.Addr().(*net.TCPAddr).Port}
	local.Close()
	sshListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sshListener.Close()
	host, user := testSigner(t), testSigner(t)
	config := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() != "fixture" || ssh.FingerprintSHA256(public) != ssh.FingerprintSHA256(user.PublicKey()) {
			return nil, errors.New("unauthorized")
		}
		return nil, nil
	}}
	config.AddHostKey(host)
	endpoint := Endpoint{Address: sshListener.Addr().String(), User: "fixture", HostFingerprint: ssh.FingerprintSHA256(host.PublicKey()), WorkerPath: DefaultWorkerPath, TailnetPeerID: "peer"}
	forwarded := make(chan error, 1)
	go func() {
		forwarded <- serveDirectCallback(sshListener, config, callback, server.Listener.Addr().String())
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	forward, err := endpoint.ForwardAgentCallback(ctx, user, func(ctx context.Context, peer, address string) (net.Conn, error) {
		if peer != "peer" || address != endpoint.Address {
			return nil, errors.New("callback bypassed enrolled route")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}, callback)
	if err != nil {
		t.Fatal(err)
	}
	defer forward.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + net.JoinHostPort(callback.Host, strconv.Itoa(callback.Port)) + "/callback?" + payload)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != payload {
		t.Fatalf("callback did not preserve bytes: %q, %v", body, err)
	}
	forward.Close()
	if err := <-forwarded; err != nil {
		t.Fatal(err)
	}
	local, err = net.Listen("tcp4", net.JoinHostPort(callback.Host, strconv.Itoa(callback.Port)))
	if err != nil {
		t.Fatal("callback listener remained open", err)
	}
	local.Close()
}

func serveDirectCallback(listener net.Listener, config *ssh.ServerConfig, callback domain.LoopbackEndpoint, target string) error {
	raw, err := listener.Accept()
	if err != nil {
		return err
	}
	defer raw.Close()
	conn, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		return err
	}
	defer conn.Close()
	go ssh.DiscardRequests(requests)
	channel := <-channels
	var destination struct {
		Host       string
		Port       uint32
		Originator string
		SourcePort uint32
	}
	if channel == nil || channel.ChannelType() != "direct-tcpip" || ssh.Unmarshal(channel.ExtraData(), &destination) != nil || destination.Host != callback.Host || destination.Port != uint32(callback.Port) {
		return errors.New("SSH did not request the exact direct loopback destination")
	}
	stream, pending, err := channel.Accept()
	if err != nil {
		return err
	}
	defer stream.Close()
	go ssh.DiscardRequests(pending)
	remote, err := net.Dial("tcp", target)
	if err != nil {
		return err
	}
	defer remote.Close()
	go func() { io.Copy(remote, stream); remote.Close() }()
	_, err = io.Copy(stream, remote)
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
