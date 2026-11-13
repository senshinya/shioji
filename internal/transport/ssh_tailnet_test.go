package transport

import (
	"strings"
	"testing"
)

func TestTailnetSSHNeverFallsBackToSystemRouting(t *testing.T) {
	s := SSH{Target: "user@100.64.0.1", TailnetPeer: "peer-one"}
	if _, err := s.arguments(); err == nil {
		t.Fatal("tailnet SSH without private route must fail closed")
	}
	s.ProxySocket = "/private/tmp/shioji/ssh.sock"
	args, err := s.arguments()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"ProxyCommand=", "/usr/bin/nc", "-U", "ssh.sock", "ControlPath=none", "ProxyJump=none"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, args)
		}
	}
	cmd, err := s.KeySetupCommand()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cmd, " "), "ProxyCommand=") {
		t.Fatal("ssh-copy-id bypasses embedded network")
	}
}

func TestTailnetSSHDefaultPortMatchesPinnedBridge(t *testing.T){s:=SSH{Target:"user@100.64.0.1",TailnetPeer:"peer",ProxySocket:"/tmp/private.sock"};args,err:=s.arguments();if err!=nil{t.Fatal(err)};if !strings.Contains(strings.Join(args," "),"Port=22"){t.Fatal("SSH config may select a different port than the pinned bridge",args)}}
