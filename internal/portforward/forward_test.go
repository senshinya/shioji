package portforward

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestURLTargetsPreserveURLAndRejectPublicOrCredentials(t *testing.T) {
	for _, tc := range []struct {
		raw, host string
		port      int
	}{
		{"http://LOCALHOST.:5173/a%2Fb?q=x#part", "localhost", 5173},
		{"https://[::1]/settings", "::1", 443},
		{"http://0.0.0.0:3000/", "127.0.0.1", 3000},
		{"http://[::]:3000/", "::1", 3000},
		{"http://192.168.1.23:8080/", "192.168.1.23", 8080},
		{"http://100.80.1.2/", "100.80.1.2", 80},
		{"http://[fd12::1]/", "fd12::1", 80},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			target, err := URLTarget(tc.raw)
			if err != nil || target.Host != tc.host || target.Port != tc.port {
				t.Fatalf("target=%+v err=%v", target, err)
			}
		})
	}
	for _, raw := range []string{"http://example.com/", "http://8.8.8.8/", "file:///tmp/a", "http://u:p@localhost:3000/", "http://localhost:0/", "http://169.254.1.2/", "http://localhost.evil:3000/"} {
		if _, err := URLTarget(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	got, err := LocalURL("https://localhost:5173/a%2Fb?q=x#part", 56173)
	if err != nil || got != "https://127.0.0.1:56173/a%2Fb?q=x#part" {
		t.Fatal(got, err)
	}
}

func TestManualTargetValidation(t *testing.T) {
	for _, host := range []string{"dev.internal", "localhost", "::1", "192.168.7.2"} {
		if err := (Target{host, 8080}).Validate(); err != nil {
			t.Fatal(host, err)
		}
	}
	for _, target := range []Target{{"", 80}, {"http://localhost", 80}, {"x\n", 80}, {"fe80::1", 80}, {"localhost", 0}, {"localhost", 65536}} {
		if target.Validate() == nil {
			t.Fatal("accepted", target)
		}
	}
}

func TestForwardRelaysHalfCloseAndReleasesListener(t *testing.T) {
	remote, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	go func() {
		for {
			conn, err := remote.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); body, _ := io.ReadAll(conn); conn.Write(append([]byte("reply:"), body...)) }()
		}
	}()
	m := New(context.Background(), nil)
	defer m.Close()
	dial := func(ctx context.Context, _ Target) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", remote.Addr().String())
	}
	item, err := m.Create(context.Background(), Request{DeviceID: "device-a", ConnectionID: "a", Name: "Mac mini", Target: Target{"localhost", 3000}, LocalPort: 0}, dial)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", item.LocalAddress())
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("binary\x00payload"))
	conn.(*net.TCPConn).CloseWrite()
	body, err := io.ReadAll(conn)
	conn.Close()
	if err != nil || string(body) != "reply:binary\x00payload" {
		t.Fatalf("%q %v", body, err)
	}
	if err = m.CloseID(item.ID); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", item.LocalAddress())
	if err != nil {
		t.Fatal("listener leaked", err)
	}
	l.Close()
	if len(m.Snapshot()) != 0 {
		t.Fatal("mapping retained")
	}
}

func TestAutomaticPortFallbackAndDeviceScopedReuse(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	dial := func(context.Context, Target) (net.Conn, error) { a, b := net.Pipe(); go b.Close(); return a, nil }
	m := New(context.Background(), nil)
	defer m.Close()
	req := Request{DeviceID: "a", Target: Target{"localhost", port}, LocalPort: port, AutoPort: true}
	var wg sync.WaitGroup
	items := make(chan Mapping, 8)
	for range 8 {
		wg.Go(func() {
			item, err := m.Create(context.Background(), req, dial)
			if err != nil {
				t.Error(err)
				return
			}
			items <- item
		})
	}
	wg.Wait()
	close(items)
	var id string
	for item := range items {
		if item.LocalPort == port {
			t.Fatal("did not fall back")
		}
		if id != "" && item.ID != id {
			t.Fatal("duplicate mapping")
		}
		id = item.ID
	}
	req.DeviceID = "b"
	b, err := m.Create(context.Background(), req, dial)
	if err != nil || b.ID == id {
		t.Fatal("cross-device reuse", b, err)
	}
	req.DeviceID = "c"
	req.AutoPort = false
	if _, err = m.Create(context.Background(), req, dial); err == nil {
		t.Fatal("manual port silently changed")
	}
}

func TestCloseDuringCreateCancelsAndCannotResurrect(t *testing.T) {
	started := make(chan struct{})
	m := New(context.Background(), nil)
	defer m.Close()
	done := make(chan error, 1)
	go func() {
		_, err := m.Create(context.Background(), Request{DeviceID: "a", Target: Target{"localhost", 3000}}, func(ctx context.Context, _ Target) (net.Conn, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		done <- err
	}()
	<-started
	items := m.Snapshot()
	if len(items) != 1 {
		t.Fatal(items)
	}
	if err := m.CloseID(items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("cancelled create succeeded")
	}
	if len(m.Snapshot()) != 0 {
		t.Fatal("late create resurrected mapping")
	}
}

func TestForwardBoundsConcurrentClientStreams(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	started := make(chan struct{}, 40)
	m := New(context.Background(), nil)
	defer m.Close()
	item, err := m.Create(context.Background(), Request{DeviceID: "a", Target: Target{"localhost", 3000}}, func(ctx context.Context, _ Target) (net.Conn, error) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			a, b := net.Pipe()
			b.Close()
			return a, nil
		}
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	var clients []net.Conn
	defer func() {
		for _, c := range clients {
			c.Close()
		}
	}()
	for range 32 {
		c, err := net.Dial("tcp", item.LocalAddress())
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
		<-started
	}
	extra, err := net.Dial("tcp", item.LocalAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	var b [1]byte
	extra.SetReadDeadline(time.Now().Add(time.Minute))
	if _, err = extra.Read(b[:]); err == nil {
		t.Fatal("extra stream accepted")
	}
	if len(started) != 0 {
		t.Fatal("more than 32 remote streams were dialed")
	}
	if err = m.CloseID(item.ID); err != nil {
		t.Fatal(err)
	}
	opened := false
	if err = m.Open(item.ID, "http://localhost:3000/", func(string) error { opened = true; return nil }); err == nil || opened {
		t.Fatal("closed mapping opened a browser")
	}
}

func TestAcceptedClosePreventsLateBrowserOpen(t *testing.T) {
	m := New(context.Background(), nil)
	defer m.Close()
	item, err := m.Create(context.Background(), Request{DeviceID: "a", Target: Target{"localhost", 3000}}, func(context.Context, Target) (net.Conn, error) { a, b := net.Pipe(); b.Close(); return a, nil })
	if err != nil {
		t.Fatal(err)
	}
	m.CancelID(item.ID)
	opened := false
	if err = m.Open(item.ID, "http://localhost:3000/", func(string) error { opened = true; return nil }); err == nil || opened {
		t.Fatal("accepted close permitted a late browser open")
	}
	m.CloseID(item.ID)
}
