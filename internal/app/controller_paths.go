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

// Only observations can follow a successfully admitted same-generation path.
// Mutations (including approvals, configuration, interrupts and user input)
// are never replayed by the transport coordinator.
func readOnlyPathQuery(method string) bool {
	switch method {
	case "device.health", "device.directories", "file.search", "file.snapshot", "attachment.get", "git.status", "git.compare", "git.refs", "git.changes", "git.file", "session.search", "runtime.tools.inspect", "runtime.tools.list", "runtime.skills.list", "runtime.skills.read", "storage.usage", "session.work", "session.work.operation", "session.context.operation", "session.history-operation":
		return true
	case "runtime.hooks.list", "runtime.hooks.operation":
		return true
	}
	return false
}

func (c *Controller) currentSSH() *transport.SSH {
	if c.paths != nil {
		if s, ok := c.paths.ssh(c.paths.active()); ok {
			if c.resolveSSH != nil {
				if routed, err := c.resolveSSH(s); err == nil {
					s = routed
				}
			}
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
	if err = client.Call(call, "snapshot", nil, &snapshot); err != nil {
		return nil, snapshot, ready, nil, err
	}
	if snapshot.DeviceID != client.DeviceID || snapshot.Generation != generation || snapshot.Sequence < after {
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
