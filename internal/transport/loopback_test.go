package transport

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"shioji/internal/domain"
	"strconv"
	"strings"
	"testing"
	"time"
)

func freeCallbackPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
func TestLoopbackForwardCarriesCallbackBytesAndReleasesBothLocalAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/callback" {
			io.WriteString(w, r.URL.RawQuery)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	endpoint := domain.LoopbackEndpoint{Host: "localhost", Port: freeCallbackPort(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forward, err := forwardLoopback(ctx, endpoint, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(server.URL, "http://"))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer forward.Close()
	if err = forward.Check(ctx); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: time.Second}
	for _, host := range []string{"127.0.0.1", "::1"} {
		response, err := client.Get("http://" + net.JoinHostPort(host, strconv.Itoa(endpoint.Port)) + "/auth/callback?code=opaque-test&state=same-state")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != "code=opaque-test&state=same-state" {
			t.Fatal("callback payload changed", string(body))
		}
	}
	cancel()
	forward.Close()
	for _, host := range []string{"127.0.0.1", "::1"} {
		l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(endpoint.Port)))
		if err != nil {
			t.Fatal("callback port retained", host, err)
		}
		l.Close()
	}
}
func TestLoopbackForwardRejectsNonLoopbackAndOccupiedPorts(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "192.168.7.119", "example.test"} {
		if _, err := forwardLoopback(context.Background(), domain.LoopbackEndpoint{Host: host, Port: 1455}, nil); err == nil {
			t.Fatal("non-loopback accepted", host)
		}
	}
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if _, err = forwardLoopback(context.Background(), domain.LoopbackEndpoint{Host: "localhost", Port: occupied.Addr().(*net.TCPAddr).Port}, nil); err == nil {
		t.Fatal("occupied callback port replaced")
	}
}
func TestSSHLoopbackUsesDirectChannelWithoutRemoteShell(t *testing.T) {
	cmd, err := (SSH{Target: "air", Port: 2222}).loopbackCommand(context.Background(), domain.LoopbackEndpoint{Host: "localhost", Port: 1455})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, "\n")
	for _, value := range []string{"-W\nlocalhost:1455\n--\nair", "StrictHostKeyChecking=yes", "ForwardAgent=no", "ClearAllForwardings=yes", "RemoteCommand=none", "PermitLocalCommand=no", "-p\n2222"} {
		if !strings.Contains(args, value) {
			t.Fatal("SSH channel protection missing", args)
		}
	}
	if cmd.Args[len(cmd.Args)-1] != "air" {
		t.Fatal("remote shell command added")
	}
}

func TestLiveSSHCallbackForward(t *testing.T) {
	target := os.Getenv("SHIOJI_AUTH_SSH_TARGET")
	if target == "" {
		t.Skip("explicit SSH callback probe only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ssh := SSH{Target: target}
	script := `import http.server,threading,sys
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(self.path.encode())
 def log_message(self,*args): pass
server=http.server.HTTPServer(('127.0.0.1',0),Handler)
print(server.server_port,flush=True)
threading.Thread(target=lambda:(sys.stdin.read(),server.shutdown()),daemon=True).start()
server.serve_forever()
`
	cmd, err := ssh.command(ctx, "/usr/bin/python3 -u -c "+shellQuote(script))
	if err != nil {
		t.Fatal(err)
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Wait() }()
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() {
		t.Fatal("remote callback fixture did not start")
	}
	port, err := strconv.Atoi(scanner.Text())
	if err != nil {
		t.Fatal(err)
	}
	forward, err := ssh.ForwardLoopback(ctx, domain.LoopbackEndpoint{Host: "localhost", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer forward.Close()
	if err = forward.Check(ctx); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 10 * time.Second}
	for _, host := range []string{"127.0.0.1", "::1"} {
		response, err := client.Get("http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/auth/callback?code=fixture-only&state=unchanged")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != "/auth/callback?code=fixture-only&state=unchanged" {
			t.Fatal("real SSH callback changed")
		}
	}
	forward.Close()
	t.Log("actual SSH callback forwarding verified on IPv4/IPv6; no account authorization or credential copy")
}
