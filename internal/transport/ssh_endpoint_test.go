package transport

import (
	"context"
	"testing"
)

func TestSSHEndpointUsesOpenSSHUserAndPortForEachRoute(t *testing.T) {
	// -G reads configuration only: these deliberately unreachable hosts never dial.
	// Explicit users/ports must override the controlling Mac's account/defaults.
	for _, tt := range []struct {
		ssh  SSH
		user string
		port int
	}{
		{SSH{Target: "alice@192.0.2.10", Port: 2222}, "alice", 2222},
		{SSH{Target: "bob@100.64.0.10", Port: 2200, TailnetPeer: "peer"}, "bob", 2200},
	} {
		e, err := tt.ssh.Endpoint(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if e.User != tt.user || e.Port != tt.port {
			t.Fatal("wrong path's effective login", e)
		}
	}
}

func TestSSHEndpointParseKeepsConfiguredIdentityWithoutInferringIt(t *testing.T) {
	e, err := parseSSHEndpoint("host air\nhostname 100.64.0.11\nuser remote-user\nport 2223\n")
	if err != nil || e.User != "remote-user" || e.Host != "100.64.0.11" || e.Port != 2223 {
		t.Fatal(e, err)
	}
	for _, output := range []string{"hostname h\nport 22\n", "hostname h\nuser u\nport 0\n"} {
		if _, err := parseSSHEndpoint(output); err == nil {
			t.Fatal("incomplete effective config accepted")
		}
	}
}
