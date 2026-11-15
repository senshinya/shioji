package app

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"shioji/internal/protocol"
	"shioji/internal/transport"
)

type DevicePathPhase string

const (
	PathConnecting  DevicePathPhase = "connecting"
	PathReady       DevicePathPhase = "ready"
	PathActive      DevicePathPhase = "active"
	PathSwitching   DevicePathPhase = "switching"
	PathUnavailable DevicePathPhase = "unavailable"
)

type DevicePathStatus struct {
	ID       string
	Phase    DevicePathPhase
	Timing   *DeviceTiming
	Error    string
	Endpoint *transport.SSHEndpoint
}
type pathSample struct {
	at      time.Time
	elapsed time.Duration
}
type pathHealth struct {
	endpoint  *transport.SSHEndpoint
	samples   []pathSample
	err       string
	available bool
	checked   bool
	cancel    context.CancelFunc
}

// Only probes run concurrently. Business dispatch and handoff remain owned by
// the existing single device controller; neither probes nor routes own agents.
type devicePaths struct {
	mu                 sync.Mutex
	config             DeviceConnection
	activeID           string
	retiring           map[string]DeviceRoute
	health             map[string]*pathHealth
	open               func(context.Context, transport.SSH) (net.Conn, error)
	changed            chan struct{}
	ctx                context.Context
	wg                 sync.WaitGroup
	vote               string
	votes              int
	voteAt, switchedAt time.Time
	switching          string
}

func newDevicePaths(c DeviceConnection, open func(context.Context, transport.SSH) (net.Conn, error)) *devicePaths {
	p := &devicePaths{config: c.Normalized(), health: map[string]*pathHealth{}, open: open, changed: make(chan struct{}, 1), retiring: map[string]DeviceRoute{}}
	p.activeID = p.config.ActiveRouteID
	for _, r := range p.config.Routes {
		p.health[r.ID] = &pathHealth{}
	}
	return p
}
func (p *devicePaths) signal() {
	select {
	case p.changed <- struct{}{}:
	default:
	}
}
func (p *devicePaths) run(ctx context.Context) {
	p.mu.Lock()
	p.ctx = ctx
	for _, r := range p.config.Routes {
		p.startProbeLocked(r)
	}
	p.mu.Unlock()
	<-ctx.Done()
	p.mu.Lock()
	for _, h := range p.health {
		if h.cancel != nil {
			h.cancel()
		}
	}
	p.mu.Unlock()
	p.wg.Wait()
}
func (p *devicePaths) configure(c DeviceConnection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c = c.Normalized()
	c.ActiveRouteID = p.activeID
	retained := map[string]bool{}
	for _, r := range c.Routes {
		retained[r.ID] = true
		if p.health[r.ID] == nil {
			p.health[r.ID] = &pathHealth{}
			if p.ctx != nil && p.ctx.Err() == nil {
				p.startProbeLocked(r)
			}
		}
	}
	for id, h := range p.health {
		if !retained[id] {
			if id == p.activeID {
				for _, r := range p.config.Routes {
					if r.ID == id {
						p.retiring[id] = r
					}
				}
			}
			if h.cancel != nil {
				h.cancel()
			}
			delete(p.health, id)
		}
	}
	p.config = c
	p.signal()
}
func (p *devicePaths) startProbeLocked(r DeviceRoute) {
	ctx, cancel := context.WithCancel(p.ctx)
	p.health[r.ID].cancel = cancel
	p.wg.Add(1)
	go func() { defer p.wg.Done(); p.probe(ctx, r) }()
}
func (p *devicePaths) record(id string, elapsed time.Duration, now time.Time, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.health[id]
	if h == nil {
		return
	}
	h.checked = true
	h.available = err == nil
	if err != nil {
		h.err = err.Error()
		h.samples = nil
	} else {
		h.err = ""
		h.samples = append(h.samples, pathSample{now, elapsed})
		if len(h.samples) > 5 {
			h.samples = h.samples[len(h.samples)-5:]
		}
	}
	p.signal()
}
func pathMedian(h *pathHealth, now time.Time) (time.Duration, time.Time, int) {
	if h == nil || !h.available {
		return 0, time.Time{}, 0
	}
	var samples []time.Duration
	var at time.Time
	for _, s := range h.samples {
		if !s.at.After(now) && now.Sub(s.at) <= 15*time.Second {
			samples = append(samples, s.elapsed)
			at = s.at
		}
	}
	if len(samples) == 0 {
		return 0, at, 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[len(samples)/2], at, len(samples)
}
func (p *devicePaths) preferred(now time.Time) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	active := p.activeID
	current, _, count := pathMedian(p.health[active], now)
	best := ""
	var fastest time.Duration
	for _, r := range p.config.Routes {
		ms, _, n := pathMedian(p.health[r.ID], now)
		if n > 0 && (best == "" || ms < fastest) {
			best, fastest = r.ID, ms
		}
	}
	if best == "" {
		return ""
	}
	if count == 0 {
		return best
	}
	if best == active || now.Sub(p.switchedAt) < 30*time.Second || current-fastest < 5*time.Millisecond || float64(fastest) > float64(current)*.7 {
		p.vote = ""
		p.votes = 0
		p.voteAt = time.Time{}
		return active
	}
	_, observed, n := pathMedian(p.health[best], now)
	if count < 3 || n < 3 {
		return active
	}
	if best != p.vote {
		p.vote = best
		p.votes = 0
		p.voteAt = time.Time{}
	}
	if observed.After(p.voteAt) {
		p.votes++
		p.voteAt = observed
	}
	if p.votes >= 3 {
		return best
	}
	return active
}
func (p *devicePaths) activate(id string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeID != id {
		p.switchedAt = now
	}
	p.config.ActiveRouteID = id
	p.activeID = id
	clear(p.retiring)
	p.switching = ""
	p.vote = ""
	p.votes = 0
	p.voteAt = time.Time{}
	p.signal()
}
func (p *devicePaths) active() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activeID
}
func (p *devicePaths) ssh(id string) (transport.SSH, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.config.Routes {
		if r.ID == id {
			return r.SSH, true
		}
	}
	if r, ok := p.retiring[id]; ok {
		return r.SSH, true
	}
	return transport.SSH{}, false
}
func (p *devicePaths) states(now time.Time) []DevicePathStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []DevicePathStatus
	for _, r := range p.config.Routes {
		h := p.health[r.ID]
		s := DevicePathStatus{ID: r.ID, Phase: PathConnecting}
		if h != nil {
			s.Endpoint = h.endpoint
			s.Error = h.err
			ms, at, n := pathMedian(h, now)
			if n > 0 {
				s.Timing = &DeviceTiming{float64(ms) / float64(time.Millisecond), at}
				s.Phase = PathReady
				if r.ID == p.activeID {
					s.Phase = PathActive
				}
			}
			if h.checked && n == 0 {
				s.Phase = PathUnavailable
			}
		}
		if r.ID == p.switching {
			s.Phase = PathSwitching
		}
		out = append(out, s)
	}
	return out
}
func (p *devicePaths) preparing(id string) { p.mu.Lock(); p.switching = id; p.mu.Unlock(); p.signal() }
func (p *devicePaths) dialRoute(ctx context.Context, id string) (net.Conn, error) {
	s, ok := p.ssh(id)
	if !ok {
		return nil, errors.New("连接路径已移除")
	}
	return p.open(ctx, s)
}
func (p *devicePaths) dial(ctx context.Context) (net.Conn, error) {
	id := p.active()
	p.mu.Lock()
	_, _, n := pathMedian(p.health[id], time.Now())
	p.mu.Unlock()
	if n == 0 {
		id = p.preferred(time.Now())
	}
	if id == "" {
		return nil, errDevicePathsWaiting
	}
	conn, err := p.dialRoute(ctx, id)
	if err != nil {
		p.record(id, 0, time.Now(), err)
		return nil, err
	}
	if p.active() != id {
		p.activate(id, time.Now())
	}
	return conn, nil
}

// A separate byte stream may use a healthy fallback, but only the RPC actor
// may commit a business-path handoff and attribute command errors to it.
func (p *devicePaths) dialStream(ctx context.Context) (net.Conn, error) {
	now := time.Now()
	p.mu.Lock()
	id := p.activeID
	if _, _, n := pathMedian(p.health[id], now); n == 0 {
		id = ""
		var fastest time.Duration
		for _, route := range p.config.Routes {
			elapsed, _, count := pathMedian(p.health[route.ID], now)
			if count > 0 && (id == "" || elapsed < fastest) {
				id, fastest = route.ID, elapsed
			}
		}
	}
	p.mu.Unlock()
	if id == "" {
		return nil, errDevicePathsWaiting
	}
	return p.dialRoute(ctx, id)
}

var errDevicePathsWaiting = errors.New("正在检查连接路径")

func (p *devicePaths) probe(ctx context.Context, r DeviceRoute) {
	lookup, stopLookup := context.WithTimeout(ctx, 8*time.Second)
	endpoint, endpointErr := r.SSH.Endpoint(lookup)
	stopLookup()
	if endpointErr == nil && ctx.Err() == nil {
		p.mu.Lock()
		if h := p.health[r.ID]; h != nil {
			h.endpoint = &endpoint
		}
		p.mu.Unlock()
		p.signal()
	}
	p.mu.Lock()
	owner := p.config.DeviceID
	p.mu.Unlock()
	var client *protocol.Client
	defer func() {
		if client != nil {
			client.Close()
		}
	}()
	for ctx.Err() == nil {
		var err error
		// A failed periodic probe is immediately rechecked on a fresh connection.
		for attempt := 0; attempt < 2; attempt++ {
			call, cancel := context.WithTimeout(ctx, 8*time.Second)
			if client == nil {
				client, err = protocol.Dial(call, func(_ context.Context) (net.Conn, error) { return p.open(ctx, r.SSH) }, owner)
			}
			if err == nil {
				method := "device.ping"
				if client.Minor < 34 {
					method = "worker.info"
				}
				target := client
				stop := context.AfterFunc(ctx, func() { target.Close() })
				started := time.Now()
				var reply struct {
					DeviceID string `json:"deviceId"`
				}
				err = client.Call(call, method, nil, &reply)
				stop()
				if err == nil && method == "device.ping" && reply.DeviceID != client.DeviceID {
					err = errors.New("探测返回了不同设备")
				}
				if err == nil && ctx.Err() == nil {
					p.record(r.ID, time.Since(started), time.Now(), nil)
				}
			}
			cancel()
			if err == nil {
				break
			}
			if client != nil {
				client.Close()
				client = nil
			}
		}
		if err != nil && ctx.Err() == nil {
			p.record(r.ID, 0, time.Now(), err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
