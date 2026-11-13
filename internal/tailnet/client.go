package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// Connection is the small local-network boundary used by presentation and
// device routing. The GUI's implementation is Client, never the node owner.
type Connection interface {
	Run(context.Context, func(Snapshot))
	Do(Action) bool
	Route(string, string, int) (string, error)
}

type Client struct {
	directory string
	ensure    func(context.Context, bool) error
	verify    func(int) error
	actions   chan Action
	mu        sync.Mutex
	session   serviceReply
	interrupt context.CancelFunc
}

type HostInfo struct {
	Version           int
	BuildID, Instance string
	Compatible        bool
}

// InspectService is read-only and does not allocate a GUI authorization session.
// A trusted older protocol can still be identified for an explicit handoff.
func InspectService(ctx context.Context, directory string, verify func(int) error) (HostInfo, error) {
	c := NewClient(directory, nil, verify)
	r, err := c.call(ctx, serviceRequest{Kind: "describe"})
	if r.Version == 0 {
		return HostInfo{}, err
	}
	return HostInfo{Version: r.Version, BuildID: r.BuildID, Instance: r.Instance, Compatible: err == nil}, nil
}

func NewClient(directory string, ensure func(context.Context, bool) error, verify func(int) error) *Client {
	return &Client{directory: directory, ensure: ensure, verify: verify, actions: make(chan Action, 16)}
}

func (c *Client) Do(a Action) bool {
	select {
	case c.actions <- a:
	default:
		return false
	}
	c.mu.Lock()
	if c.interrupt != nil {
		c.interrupt()
	}
	c.mu.Unlock()
	return true
}

func (c *Client) call(ctx context.Context, r serviceRequest) (serviceReply, error) {
	var reply serviceReply
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", ServiceSocket(c.directory))
	if err != nil {
		return reply, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(6 * time.Second))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	pid, err := peerProcess(conn.(*net.UnixConn))
	if err != nil {
		return reply, err
	}
	if c.verify == nil {
		return reply, errors.New("无法核对网络服务身份")
	}
	if err = c.verify(pid); err != nil {
		return reply, err
	}
	r.Version = serviceVersion
	if err = json.NewEncoder(conn).Encode(r); err != nil {
		return reply, err
	}
	if err = json.NewDecoder(io.LimitReader(conn, maxServiceMessage)).Decode(&reply); err != nil {
		return reply, err
	}
	if reply.Version != serviceVersion {
		return reply, errors.New("网络服务版本不兼容，请更新应用")
	}
	if reply.Error != "" {
		return reply, errors.New(reply.Error)
	}
	return reply, nil
}

func (c *Client) sessionRequest(kind string) serviceRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return serviceRequest{Kind: kind, Client: c.session.Client, Instance: c.session.Instance}
}

func (c *Client) Route(peer, address string, port int) (string, error) {
	r := c.sessionRequest("route")
	if r.Client == "" {
		return "", errors.New("Tailnet 网络服务尚未连接")
	}
	r.Peer, r.Address, r.Port = peer, address, port
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reply, err := c.call(ctx, r)
	if err != nil {
		return "", err
	}
	return reply.Socket, nil
}

// Run only owns the GUI subscription. Cancellation never sends pause/logout,
// never closes bridges and never terminates the network service process.
func (c *Client) Run(ctx context.Context, changed func(Snapshot)) {
	intent, err := readIntent(c.directory)
	if err != nil {
		changed(Snapshot{State: "Offline", Error: "无法读取网络连接设置"})
		return
	}
	idle := Snapshot{State: "NotStarted", Hostname: intent.Hostname, InboundEnabled: intent.forwardingEnabled()}
	if intent.Mode == "paused" {
		idle.State = "Stopped"
	}
	var pending *Action
	var revision, sequence uint64
	connected := false
	var operationError, unknownOperation, prepared string
	var unknownSequence uint64
	last := idle
	restoreAllowed := intent.Mode == "connected"
	var restoreError error
	wait := func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case a := <-c.actions:
			pending = &a
		case <-timer.C:
		}
	}
	for ctx.Err() == nil {
		if pending == nil {
			select {
			case a := <-c.actions:
				pending = &a
			default:
			}
		}
		if !connected {
			hello, e := c.call(ctx, c.sessionRequest("hello"))
			explicitConnection := pending != nil && (pending.Kind == "join" || pending.Kind == "resume")
			if pending != nil && (pending.Kind == "pause" || pending.Kind == "logout") {
				restoreAllowed = false
			}
			if e != nil && c.ensure != nil && (pending == nil && restoreAllowed || explicitConnection) {
				startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				e = c.ensure(startCtx, explicitConnection)
				cancel()
				if errors.Is(e, ErrUserAction) {
					restoreAllowed = false
					restoreError = e
				}
				if e == nil {
					if pending != nil {
						prepared = pending.ID
					}
					hello, e = c.call(ctx, c.sessionRequest("hello"))
				}
			}
			if e != nil {
				s := last
				if intent.Mode == "connected" || pending != nil {
					s.State = "Offline"
					s.Error = e.Error()
					if restoreError != nil && pending == nil {
						s.Error = restoreError.Error()
					}
				}
				s.AuthURL = ""
				s.Incoming = false
				if pending != nil {
					s.OperationID = pending.ID
					pending = nil
				}
				if ctx.Err() == nil {
					changed(s)
				}
				wait()
				continue
			}
			c.mu.Lock()
			c.session = hello
			c.mu.Unlock()
			revision = 0
			sequence = hello.AcceptedSequence
			connected = true
		}
		if pending != nil {
			if c.ensure != nil && prepared != pending.ID && (pending.Kind == "join" || pending.Kind == "resume") {
				call, cancel := context.WithTimeout(ctx, 15*time.Second)
				e := c.ensure(call, true)
				cancel()
				if e != nil {
					s := last
					s.Error = e.Error()
					s.OperationID = pending.ID
					s.Authorizing = false
					s.AuthURL = ""
					pending = nil
					changed(s)
					wait()
					continue
				}
				hello, e := c.call(ctx, c.sessionRequest("hello"))
				if e != nil {
					connected = false
					continue
				}
				c.mu.Lock()
				c.session = hello
				c.mu.Unlock()
				sequence = hello.AcceptedSequence
				revision = 0
			}
			sequence++
			r := c.sessionRequest("action")
			r.Sequence = sequence
			r.Action = *pending
			_, e := c.call(ctx, r)
			if e != nil {
				operationError = "网络操作结果待核对，请检查连接状态"
				unknownOperation = pending.ID
				unknownSequence = sequence
			} else {
				operationError = ""
				unknownOperation = ""
			}
			// Even an interrupted response is query-only from here. A new GUI
			// must not replay an unknown enrollment, pause or logout operation.
			pending = nil
		}
		watch, cancel := context.WithCancel(ctx)
		c.mu.Lock()
		c.interrupt = cancel
		c.mu.Unlock()
		r := c.sessionRequest("snapshot")
		r.After = revision
		reply, e := c.call(watch, r)
		c.mu.Lock()
		c.interrupt = nil
		c.mu.Unlock()
		cancel()
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			select {
			case a := <-c.actions:
				pending = &a
				continue
			default:
			}
			connected = false
			s := last
			s.State = "Offline"
			s.Error = "无法连接 Tailnet 网络服务"
			s.AuthURL = ""
			s.Incoming = false
			changed(s)
			wait()
			continue
		}
		revision = reply.Revision
		s := reply.Snapshot
		s.AuthURL = reply.AuthorizationURL
		if s.State == "" {
			s.State = "Starting"
		}
		// A current authoritative snapshot resolves a lost action reply.
		if operationError != "" && s.OperationID != unknownOperation && !(reply.AcceptedSequence == unknownSequence && reply.AcceptedID == unknownOperation) {
			s.Error = operationError
		} else {
			operationError = ""
			unknownOperation = ""
		}
		last = s
		changed(s)
	}
}
