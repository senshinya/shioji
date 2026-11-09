package app

import (
	"context"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"strconv"
	"testing"
)

func TestRemoteBrowserDoesNotExposeURLUntilForwardCanBeEstablished(t *testing.T) {
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	var forwards signInForwards
	defer forwards.Close()
	in := protocol.SignInInput{ID: "browser-one", HarnessID: "custom"}
	status := domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInWaiting, Challenge: domain.SignInChallenge{URL: "https://example.test/login", Callback: &domain.LoopbackEndpoint{Host: "localhost", Port: port}}}
	result, err := forwards.prepare(context.Background(), transport.SSH{Target: "air"}, "device-b", in, status)
	if err != nil || result.Challenge.URL != "" || result.Message == "" || result.Phase != domain.SignInWaiting {
		t.Fatal("unforwarded browser URL exposed", result, err)
	}
}
func TestTerminalSignInReleasesOnlyItsForward(t *testing.T) {
	temp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := temp.Addr().(*net.TCPAddr).Port
	temp.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forward, err := (transport.SSH{Target: "air"}).ForwardLoopback(ctx, domain.LoopbackEndpoint{Host: "localhost", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	close(ready)
	forwards := signInForwards{entries: map[string]*signInForward{"one": {device: "device-b", forward: forward, cancel: cancel, ready: ready, ctx: ctx}}}
	defer forwards.Close()
	_, err = forwards.prepare(context.Background(), transport.SSH{Target: "air"}, "another-device", protocol.SignInInput{ID: "one", HarnessID: "custom"}, domain.SignInStatus{ID: "one", HarnessID: "custom", Phase: domain.SignInCompleted})
	if err == nil {
		t.Fatal("another device retired the callback")
	}
	_, err = forwards.prepare(context.Background(), transport.SSH{Target: "air"}, "device-b", protocol.SignInInput{ID: "one", HarnessID: "custom"}, domain.SignInStatus{ID: "one", HarnessID: "custom", Phase: domain.SignInCompleted})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "::1"} {
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			t.Fatal("terminal callback retained", err)
		}
		listener.Close()
	}
}
func TestDeviceCodeReplyDoesNotCreateForward(t *testing.T) {
	var forwards signInForwards
	defer forwards.Close()
	in := protocol.SignInInput{ID: "device-code", HarnessID: "custom"}
	status := domain.SignInStatus{ID: in.ID, HarnessID: in.HarnessID, Phase: domain.SignInWaiting, Challenge: domain.SignInChallenge{URL: "https://example.test/device", Code: "ABCD-EFGH"}}
	got, err := forwards.prepare(context.Background(), transport.SSH{Target: "air"}, "device-b", in, status)
	if err != nil || got.Challenge.URL != status.Challenge.URL || len(forwards.entries) != 0 {
		t.Fatal("device code acquired tunnel", got, err)
	}
	status.ID = "other"
	if _, err = forwards.prepare(context.Background(), transport.SSH{Target: "air"}, "device-b", in, status); err == nil {
		t.Fatal("mismatched reply accepted")
	}
}
