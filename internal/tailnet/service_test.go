package tailnet

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func startNetworkTestService(t *testing.T) (*Node, string, context.CancelFunc, <-chan error) {
	t.Helper()
	dir := t.TempDir()
	if err := writeIntent(dir, nodeIntent{Mode: "paused", Hostname: "retained-node"}); err != nil {
		t.Fatal(err)
	}
	n := New(dir, func() ([]byte, error) { t.Error("paused service read credentials"); return nil, nil })
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, n) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("service failed to stop")
		}
	})
	path := ServiceSocket(dir)
	deadline := time.Now().Add(3 * time.Second)
	for {
		// The control listener is created before the node publishes its
		// initial paused state. Wait for that publication too, so fixtures
		// cannot insert a bridge that startup immediately drops as offline.
		if s, err := os.Stat(path); err == nil && s.Mode()&os.ModeSocket != 0 && n.Snapshot().State == "Stopped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not finish paused startup")
		}
		time.Sleep(time.Millisecond)
	}
	return n, path, stop, done
}

func networkTestCall(t *testing.T, path string, req serviceRequest) serviceReply {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if err = json.NewEncoder(c).Encode(req); err != nil {
		t.Fatal(err)
	}
	var reply serviceReply
	if err = json.NewDecoder(c).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestNetworkServiceOutlivesItsClients(t *testing.T) {
	_, path, _, _ := startNetworkTestService(t)
	first := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "hello"})
	if first.Error != "" || first.Client == "" || first.Instance == "" {
		t.Fatal(first)
	}
	// Every call closes its client socket. A fresh client still reaches the
	// same node owner, rather than silently creating a second instance.
	second := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "hello"})
	if second.Error != "" || second.Instance != first.Instance || second.Client == first.Client {
		t.Fatal(second)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("unsafe control socket", st, err)
	}
}

func TestNetworkRefreshPreservesPausedIdentity(t *testing.T) {
	n, path, _, _ := startNetworkTestService(t)
	hello := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "hello"})
	reply := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "action", Client: hello.Client, Instance: hello.Instance, Sequence: 1, Action: Action{Kind: "refresh", ID: "refresh-current"}})
	if reply.Error != "" {
		t.Fatal("refresh rejected", reply.Error)
	}
	deadline := time.Now().Add(time.Second)
	for n.Snapshot().OperationID != "refresh-current" {
		if time.Now().After(deadline) {
			t.Fatal("refresh never reached owner")
		}
		time.Sleep(time.Millisecond)
	}
	s := n.Snapshot()
	if s.State != "Stopped" || s.Hostname != "retained-node" || !s.InboundEnabled {
		t.Fatal("refresh changed paused node", s)
	}
	intent, err := readIntent(n.directory)
	if err != nil || intent.Mode != "paused" || intent.Hostname != "retained-node" {
		t.Fatal("refresh changed saved intent", intent, err)
	}
	if _, err := os.Stat(filepath.Join(n.directory, "identity")); !os.IsNotExist(err) {
		t.Fatal("refresh created a node identity", err)
	}
}

func TestNetworkActionsFenceOldInstancesAndDuplicateSequences(t *testing.T) {
	n, path, _, _ := startNetworkTestService(t)
	hello := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "hello"})
	request := serviceRequest{Version: 1, Kind: "action", Client: hello.Client, Instance: "old-instance", Sequence: 1, Action: Action{Kind: "incoming-off", ID: "turn-off"}}
	if reply := networkTestCall(t, path, request); reply.Error == "" {
		t.Fatal("old instance accepted action")
	}
	request.Instance = hello.Instance
	if reply := networkTestCall(t, path, request); reply.Error != "" {
		t.Fatal(reply)
	}
	deadline := time.Now().Add(time.Second)
	for n.Snapshot().OperationID != "turn-off" {
		if time.Now().After(deadline) {
			t.Fatal("action did not reach owner")
		}
		time.Sleep(time.Millisecond)
	}
	if n.Snapshot().InboundEnabled {
		t.Fatal("forwarding not disabled")
	}
	request.Action = Action{Kind: "incoming-on", ID: "reused-sequence"}
	if reply := networkTestCall(t, path, request); reply.Error == "" {
		t.Fatal("sequence reuse with changed input accepted")
	}
	if n.Snapshot().InboundEnabled {
		t.Fatal("duplicate sequence mutated network")
	}
	if _, err := os.Stat(filepath.Join(n.directory, "identity")); !os.IsNotExist(err) {
		t.Fatal("paused node identity created", err)
	}
}

func TestNetworkAuthorizationURLOnlyReachesInitiatingClient(t *testing.T) {
	n, path, _, _ := startNetworkTestService(t)
	a := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "hello"})
	b := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "hello"})
	// Install the same transient state a real enrollment would publish,
	// without enrolling a test node or asking Keychain for credentials.
	n.publish(Snapshot{State: "NeedsLogin", Authorizing: true, AuthURL: "https://login.example/private", OperationID: "login"}, func(Snapshot) {})
	r := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "snapshot", Instance: a.Instance, Client: b.Client})
	if r.AuthorizationURL != "" {
		t.Fatal("unowned browser URL leaked", r.AuthorizationURL)
	}
}

func TestEnrollmentKeepsURLScopeWhileWaitingForNode(t *testing.T) {
	n := New(t.TempDir(), nil)
	s := &networkService{node: n, instance: "test", clients: map[string]*serviceClient{}, changed: make(chan struct{})}
	a := s.handle(context.Background(), serviceRequest{Version: 1, Kind: "hello"})
	b := s.handle(context.Background(), serviceRequest{Version: 1, Kind: "hello"})
	s.handle(context.Background(), serviceRequest{Version: 1, Kind: "action", Client: a.Client, Instance: "test", Sequence: 1, Action: Action{Kind: "join", ID: "auth"}})
	n.publish(Snapshot{State: "NeedsLogin", OperationID: "auth", Authorizing: true, AuthURL: "https://login.example/private"}, func(Snapshot) {})
	owner := s.handle(context.Background(), serviceRequest{Version: 1, Kind: "snapshot", Client: a.Client, Instance: "test"})
	other := s.handle(context.Background(), serviceRequest{Version: 1, Kind: "snapshot", Client: b.Client, Instance: "test"})
	if owner.AuthorizationURL != "https://login.example/private" || other.AuthorizationURL != "" {
		t.Fatal("authorization scope lost or leaked", owner.AuthorizationURL, other.AuthorizationURL)
	}
	reconnected := s.handle(context.Background(), serviceRequest{Version: 1, Kind: "hello", Client: a.Client, Instance: "test"})
	if reconnected.Client != a.Client || reconnected.AuthorizationURL != "https://login.example/private" {
		t.Fatal("same GUI lost enrollment after socket reconnect", reconnected.Client)
	}
}

func TestNetworkClientDetachKeepsServiceAndManualPause(t *testing.T) {
	n, path, _, _ := startNetworkTestService(t)
	ctx, stop := context.WithCancel(context.Background())
	updates := make(chan Snapshot, 32)
	client := NewClient(n.directory, nil, func(pid int) error {
		if pid != os.Getpid() {
			t.Error("wrong owner PID", pid)
		}
		return nil
	})
	done := make(chan struct{})
	go func() { defer close(done); client.Run(ctx, func(s Snapshot) { updates <- s }) }()
	wait := func(id string) {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case s := <-updates:
				if id == "" && s.State == "Stopped" || s.OperationID == id && id != "" {
					return
				}
			case <-timer.C:
				t.Fatal("client did not receive state", id)
			}
		}
	}
	wait("")
	if !client.Do(Action{Kind: "incoming-off", ID: "off"}) {
		t.Fatal("action rejected")
	}
	wait("off")
	stop()
	<-done
	r := networkTestCall(t, path, serviceRequest{Version: 1, Kind: "hello"})
	if r.Error != "" || r.Snapshot.State != "Stopped" || r.Snapshot.InboundEnabled {
		t.Fatal("GUI detach stopped service or changed preferences", r)
	}
}

func TestClientExitPreservesActiveServiceStreamAndNewConnections(t *testing.T) {
	n, path, stop, _ := startNetworkTestService(t)
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	b, err := newBridge(filepath.Join(filepath.Dir(path), "retained.sock"), func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", echo.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	n.bridges[routeKey{"test", "100.64.0.1", 22}] = b
	n.mu.Unlock()
	conn, err := net.Dial("unix", b.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	check := func(c net.Conn, text string) {
		t.Helper()
		c.SetDeadline(time.Now().Add(time.Second))
		if _, e := io.WriteString(c, text); e != nil {
			t.Fatal(e)
		}
		got := make([]byte, len(text))
		if _, e := io.ReadFull(c, got); e != nil || string(got) != text {
			t.Fatal("retained stream interrupted", e, string(got))
		}
	}
	check(conn, "before")
	ctx, cancel := context.WithCancel(context.Background())
	c := NewClient(n.directory, nil, func(int) error { return nil })
	updated := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx, func(Snapshot) {
			select {
			case updated <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not subscribe")
	}
	cancel()
	<-done
	b.mu.Lock()
	b.lastUse = time.Now().Add(-time.Hour)
	b.mu.Unlock()
	n.mu.Lock()
	n.pruneBridgesLocked(time.Now())
	n.mu.Unlock()
	check(conn, "after-GUI-exit")
	next, e := net.Dial("unix", b.listener.Addr().String())
	if e != nil {
		t.Fatal("new connection unavailable after GUI exit", e)
	}
	defer next.Close()
	check(next, "new-stream")
	stop()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("actual network service stop did not close streams")
	}
}

func TestMalformedIntentCannotLeaveACommandAcceptingDeadHost(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "intent.json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	n := New(dir, func() ([]byte, error) { t.Error("malformed settings accessed credentials"); return nil, nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Serve(ctx, n); err == nil {
		t.Fatal("dead node consumer was reported as a healthy service")
	}
	if _, err := os.Stat(ServiceSocket(dir)); !os.IsNotExist(err) {
		t.Fatal("dead host retained control socket", err)
	}
	if err := writeIntent(dir, nodeIntent{Mode: "paused"}); err != nil {
		t.Fatal(err)
	}
	ctx2, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx2, n) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ServiceSocket(dir)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("repaired settings could not restart host")
		}
		time.Sleep(time.Millisecond)
	}
	hello := networkTestCall(t, ServiceSocket(dir), serviceRequest{Version: 1, Kind: "hello"})
	if hello.Error != "" {
		t.Fatal(hello.Error)
	}
}
