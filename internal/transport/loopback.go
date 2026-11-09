package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os/exec"
	"shioji/internal/domain"
	"strconv"
	"sync"
	"time"
)

// LoopbackForward owns only the callback listeners and streams it creates.
// Payload bytes pass unchanged; no HTTP query, token or body is inspected/logged.
type LoopbackForward struct {
	listeners   []net.Listener
	dial        func(context.Context) (net.Conn, error)
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	connections map[net.Conn]bool
	closed      bool
	wg          sync.WaitGroup
}

func (s SSH) loopbackCommand(ctx context.Context, e domain.LoopbackEndpoint) (*exec.Cmd, error) {
	if !e.Valid() {
		return nil, errors.New("登录回调必须位于执行设备回环地址")
	}
	args, err := s.arguments()
	if err != nil {
		return nil, err
	}
	args = append(args, "-o", "RemoteCommand=none", "-o", "PermitLocalCommand=no", "-W", net.JoinHostPort(e.Host, strconv.Itoa(e.Port)), "--", s.Target)
	return exec.CommandContext(ctx, "/usr/bin/ssh", args...), nil
}
func (s SSH) ForwardLoopback(ctx context.Context, e domain.LoopbackEndpoint) (*LoopbackForward, error) {
	return forwardLoopback(ctx, e, func(ctx context.Context) (net.Conn, error) {
		cmd, err := s.loopbackCommand(ctx, e)
		if err != nil {
			return nil, err
		}
		return dialProcess(cmd)
	})
}
func forwardLoopback(ctx context.Context, e domain.LoopbackEndpoint, dial func(context.Context) (net.Conn, error)) (*LoopbackForward, error) {
	if !e.Valid() {
		return nil, errors.New("登录回调必须位于回环地址")
	}
	lifetime, cancel := context.WithCancel(ctx)
	f := &LoopbackForward{ctx: lifetime, cancel: cancel, dial: dial, connections: map[net.Conn]bool{}}
	hosts := []string{e.Host}
	if e.Host == "localhost" {
		hosts = []string{"127.0.0.1", "::1"}
	}
	for _, host := range hosts {
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(e.Port)))
		if err != nil {
			for _, l := range f.listeners {
				l.Close()
			}
			cancel()
			return nil, errors.New("浏览器登录回调端口已被占用，可使用验证码登录")
		}
		f.listeners = append(f.listeners, listener)
	}
	for _, listener := range f.listeners {
		f.wg.Add(1)
		go f.accept(listener)
	}
	go func() { <-lifetime.Done(); f.Close() }()
	return f, nil
}
func (f *LoopbackForward) accept(listener net.Listener) {
	defer f.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			conn.Close()
			return
		}
		f.connections[conn] = true
		f.wg.Add(1)
		f.mu.Unlock()
		go f.relay(conn)
	}
}
func (f *LoopbackForward) relay(local net.Conn) {
	defer f.wg.Done()
	defer func() { local.Close(); f.mu.Lock(); delete(f.connections, local); f.mu.Unlock() }()
	if f.dial == nil {
		return
	}
	remote, err := f.dial(f.ctx)
	if err != nil {
		return
	}
	defer remote.Close()
	sent := make(chan struct{})
	go func() { io.Copy(remote, local); remote.Close(); local.Close(); close(sent) }()
	io.Copy(local, remote)
	remote.Close()
	local.Close()
	<-sent
}
func (f *LoopbackForward) Check(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+f.listeners[0].Addr().String()+"/__shioji_callback_probe__", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("无法通过 SSH 连接登录回调，请检查设备连接或改用验证码登录")
	}
	response.Body.Close()
	return nil
}
func (f *LoopbackForward) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.cancel()
	for _, listener := range f.listeners {
		listener.Close()
	}
	for conn := range f.connections {
		conn.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
	return nil
}
