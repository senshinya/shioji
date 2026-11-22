package app

import (
	"context"
	"errors"
	"net"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"time"
)

// AuthenticatedDevicePath dials an independently verified SSH host without
// relying on an OpenSSH executable. The controller verifies the worker ID.
type AuthenticatedDevicePath struct {
	ID       string
	Endpoint transport.SSHEndpoint
	Dial     func(context.Context) (net.Conn, error)
	Worker   transport.RemoteWorker
}

func (c *Controller) authenticatedWorker() transport.RemoteWorker {
	p := c.paths
	if p == nil || p.authenticated == nil {
		return transport.RemoteWorker{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authenticated[p.activeID].Worker
}

func NewAuthenticatedDeviceController(directory, device, preferred string, paths []AuthenticatedDevicePath) *Controller {
	c := NewController(directory)
	c.ExpectedDeviceID = device
	c.workerControlJobs = make(chan Job, 4)
	p := newDevicePaths(DeviceConnection{DeviceID: device}, nil)
	p.authenticated = make(map[string]AuthenticatedDevicePath)
	p.activeID = preferred
	c.paths, c.Dial = p, p.dial
	c.UpdateAuthenticatedDevicePaths(paths)
	return c
}

// UpdateAuthenticatedDevicePaths admits verified routes without restarting
// the logical connection. Only the controller commits a business handoff.
func (c *Controller) UpdateAuthenticatedDevicePaths(paths []AuthenticatedDevicePath) bool {
	p := c.paths
	if p == nil || p.authenticated == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next := p.config
	next.Routes = make([]DeviceRoute, 0, len(paths))
	for _, route := range paths {
		next.Routes = append(next.Routes, DeviceRoute{ID: route.ID})
		p.authenticated[route.ID] = route
	}
	p.configureLocked(next)
	for id := range p.authenticated {
		if p.health[id] == nil && id != p.activeID {
			delete(p.authenticated, id)
		}
	}
	return true
}

func (c *Controller) CanRemoveAuthenticatedDevicePath(id string) bool {
	p := c.paths
	if p == nil || p.authenticated == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.removalReplacementLocked(id)
	return err == nil
}

// RemoveAuthenticatedDevicePath persists the removal before changing the path
// set. Save writes the local registry and must not call back into the controller.
// The current RPC connection is kept until the normal handoff admits a backup.
func (c *Controller) RemoveAuthenticatedDevicePath(id string, save func(replacement string) error) error {
	p := c.paths
	if p == nil || p.authenticated == nil {
		return errors.New("设备的连接路径尚未就绪")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	replacement, err := p.removalReplacementLocked(id)
	if err != nil {
		return err
	}
	if err := save(replacement); err != nil {
		return err
	}
	next := p.config
	next.Routes = make([]DeviceRoute, 0, len(p.config.Routes)-1)
	for _, route := range p.config.Routes {
		if route.ID != id {
			next.Routes = append(next.Routes, route)
		}
	}
	p.configureLocked(next)
	if id != p.activeID {
		delete(p.authenticated, id)
	}
	return nil
}

func (p *devicePaths) removalReplacementLocked(id string) (string, error) {
	if p.health[id] == nil {
		return "", errors.New("连接路径已变化，请重新选择")
	}
	if len(p.config.Routes) < 2 {
		return "", errors.New("设备至少需要一条连接路径")
	}
	if p.switching == id {
		return "", errors.New("这条连接正在切换，请稍后再试")
	}
	if id != p.activeID && p.health[p.activeID] != nil {
		return p.activeID, nil
	}
	now := time.Now()
	for _, route := range p.config.Routes {
		if route.ID != id {
			if _, _, samples := pathMedian(p.health[route.ID], now); samples > 0 {
				return route.ID, nil
			}
		}
	}
	return "", errors.New("请先确认另一条可用的连接路径")
}

// DialDeviceStream opens a dedicated stream for attachments and port forwards.
func (c *Controller) DialDeviceStream(ctx context.Context) (net.Conn, error) {
	if c.paths != nil {
		return c.paths.dialStream(ctx)
	}
	return c.dial(ctx)
}

// Only observations can follow a successfully admitted same-generation path.
// Mutations (including approvals, configuration, interrupts and user input)
// are never replayed by the transport coordinator.
func readOnlyPathQuery(method string) bool {
	switch method {
	case "device.health", "device.directories", "file.search", "file.snapshot", "attachment.get", "git.status", "git.compare", "git.refs", "git.changes", "git.file", "session.search", "runtime.tools.inspect", "runtime.tools.list", "runtime.skills.list", "runtime.skills.read", "storage.usage", "session.work", "session.work.operation", "session.context.operation", "session.history-operation":
		return true
	case "runtime.hooks.list", "runtime.hooks.operation", "runtime.signin.status":
		return true
	}
	return false
}

func (c *Controller) currentSSH() *transport.SSH {
	s := c.configuredSSH()
	if s != nil && c.resolveSSH != nil {
		if routed, err := c.resolveSSH(*s); err == nil {
			return &routed
		}
	}
	return s
}

func (c *Controller) configuredSSH() *transport.SSH {
	if c.paths != nil && c.paths.authenticated == nil {
		if s, ok := c.paths.ssh(c.paths.active()); ok {
			return &s
		}
	}
	return c.WorkerSSH
}
func (c *Controller) publishPaths() {
	if c.paths == nil {
		return
	}
	states := c.paths.states(time.Now())
	var timing *DeviceTiming
	for _, s := range states {
		if s.Phase == PathActive {
			timing = s.Timing
		}
	}
	c.post(func(m *Model) { m.DevicePaths = states; m.ConnectionTiming = timing })
}

// Prepare a replacement without touching the old command/event connections.
// Collect journal gaps before committing a new business path, never jump its
// cursor past content which has not been delivered to the model.
func (c *Controller) preparePath(ctx context.Context, id, generation string, after int64, q protocol.ReadinessQuery) (*protocol.Client, domain.Snapshot, domain.Readiness, []domain.Event, error) {
	var snapshot domain.Snapshot
	var ready domain.Readiness
	var events []domain.Event
	call, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client, err := protocol.Dial(call, func(_ context.Context) (net.Conn, error) { return c.paths.dialRoute(ctx, id) }, c.ExpectedDeviceID)
	if err != nil {
		return nil, snapshot, ready, nil, err
	}
	failed := true
	defer func() {
		if failed {
			client.Close()
		}
	}()
	if client.Generation != "" && client.Minor >= protocol.CatalogMinor {
		var page protocol.EventPage
		if err = client.Call(call, "events", protocol.EventsQuery{After: after, Generation: generation}, &page); err != nil {
			return nil, snapshot, ready, nil, err
		}
		snapshot = domain.Snapshot{DeviceID: client.DeviceID, Generation: page.Generation, Sequence: after}
		for _, event := range page.Events {
			if event.Sequence != snapshot.Sequence+1 {
				return nil, snapshot, ready, nil, errors.New("连接交接事件不连续")
			}
			events = append(events, event)
			snapshot.Sequence = event.Sequence
		}
	} else if snapshot, err = client.ReadSnapshot(call); err != nil {
		return nil, snapshot, ready, nil, err
	}
	if snapshot.DeviceID != client.DeviceID || client.Generation != "" && client.Generation != generation || snapshot.Generation != generation || snapshot.Sequence < after {
		return nil, snapshot, ready, nil, errors.New("执行服务身份或运行代次已改变")
	}
	if err = client.Call(call, "readiness", q, &ready); err != nil {
		return nil, snapshot, ready, nil, err
	}
	for after < snapshot.Sequence {
		var page protocol.EventPage
		if err = client.Call(call, "events", protocol.EventsQuery{After: after, Generation: generation}, &page); err != nil {
			return nil, snapshot, ready, nil, err
		}
		if page.Generation != generation || len(page.Events) == 0 {
			return nil, snapshot, ready, nil, errors.New("连接交接缺少事件")
		}
		for _, e := range page.Events {
			if e.Sequence > snapshot.Sequence {
				break
			}
			if e.Sequence != after+1 {
				return nil, snapshot, ready, nil, errors.New("连接交接事件不连续")
			}
			events = append(events, e)
			after = e.Sequence
		}
	}
	failed = false
	return client, snapshot, ready, events, nil
}
