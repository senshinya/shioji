package tailnet

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

type bridge struct {
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	generation uint64
	lastUse    time.Time
	streams    map[net.Conn]bool
	slots      chan struct{}
}

func newBridge(path string, dial func(context.Context) (net.Conn, error)) (*bridge, error) {
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return serveBridge(l, dial), nil
}
func serveBridge(l net.Listener, dial func(context.Context) (net.Conn, error)) *bridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &bridge{listener: l, ctx: ctx, cancel: cancel, streams: map[net.Conn]bool{}, slots: make(chan struct{}, 32), lastUse: time.Now()}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			select {
			case b.slots <- struct{}{}:
				go b.serve(c, dial)
			default:
				c.Close()
			}
		}
	}()
	return b
}
func (b *bridge) track(c net.Conn, generation uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || generation != b.generation {
		c.Close()
		return false
	}
	b.streams[c] = true
	b.lastUse = time.Now()
	return true
}
func (b *bridge) serve(c net.Conn, dial func(context.Context) (net.Conn, error)) {
	defer func() { <-b.slots }()
	b.mu.Lock()
	generation := b.generation
	b.mu.Unlock()
	if !b.track(c, generation) {
		return
	}
	defer b.release(c)
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	remote, err := dial(ctx)
	cancel()
	if err != nil {
		return
	}
	if !b.track(remote, generation) {
		return
	}
	defer b.release(remote)
	done := make(chan struct{})
	go func() {
		io.Copy(remote, c)
		if half, ok := remote.(interface{ CloseWrite() error }); ok {
			half.CloseWrite()
		}
		close(done)
	}()
	io.Copy(c, remote)
	if half, ok := c.(interface{ CloseWrite() error }); ok {
		half.CloseWrite()
	} else {
		c.Close()
	}
	<-done
}
func (b *bridge) release(c net.Conn) {
	c.Close()
	b.mu.Lock()
	delete(b.streams, c)
	b.lastUse = time.Now()
	b.mu.Unlock()
}

func (b *bridge) idleBefore(cutoff time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed || len(b.streams) == 0 && len(b.slots) == 0 && b.lastUse.Before(cutoff)
}
func (b *bridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.cancel()
	if b.listener != nil {
		b.listener.Close()
	}
	for c := range b.streams {
		c.Close()
	}
}

// Drop closes current streams while retaining the private socket for reconnect.
func (b *bridge) Drop() {
	b.mu.Lock()
	b.generation++
	defer b.mu.Unlock()
	for c := range b.streams {
		c.Close()
	}
}
