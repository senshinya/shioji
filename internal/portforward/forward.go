package portforward

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"shioji/internal/domain"
)

type Request struct {
	ID                           string
	DeviceID, ConnectionID, Name string
	Target                       Target
	LocalPort                    int
	AutoPort                     bool
}
type Mapping struct {
	ID, DeviceID, ConnectionID, Name string
	Target                           Target
	LocalPort                        int
	State, Error, LastURL            string
}

func (m Mapping) LocalAddress() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(m.LocalPort))
}

type Dial func(context.Context, Target) (net.Conn, error)
type entry struct {
	closeStarted bool
	mapping      Mapping
	ctx          context.Context
	cancel       context.CancelFunc
	dial         Dial
	listener     net.Listener
	connections  map[net.Conn]bool
	ready        chan struct{}
	err          error
	closing      bool
	wg           sync.WaitGroup
	order        uint64
	streams      int
}
type Manager struct {
	closedDone chan struct{}
	notifyMu   sync.Mutex
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	entries    map[string]*entry
	changed    func([]Mapping)
	closed     bool
	order      uint64
}

func New(ctx context.Context, changed func([]Mapping)) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, entries: map[string]*entry{}, changed: changed, closedDone: make(chan struct{})}
	go func() { <-ctx.Done(); m.Close() }()
	return m
}
func (m *Manager) Snapshot() []Mapping {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].order < entries[j].order })
	out := make([]Mapping, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.mapping)
	}
	return out
}
func (m *Manager) notify() {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	if m.changed != nil {
		m.changed(m.Snapshot())
	}
}
func (m *Manager) Create(ctx context.Context, r Request, dial Dial) (Mapping, error) {
	if ctx.Err() != nil {
		return Mapping{}, ctx.Err()
	}
	var err error
	r.Target, err = r.Target.Normalize()
	if err != nil {
		return Mapping{}, err
	}
	if r.DeviceID == "" || r.LocalPort < 0 || r.LocalPort > 65535 || dial == nil {
		return Mapping{}, errors.New("端口转发设备或端口无效")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Mapping{}, net.ErrClosed
	}
	if r.AutoPort {
		for _, e := range m.entries {
			if !e.closing && e.mapping.DeviceID == r.DeviceID && e.mapping.Target == r.Target {
				ready := e.ready
				m.mu.Unlock()
				select {
				case <-ready:
				case <-ctx.Done():
					return Mapping{}, ctx.Err()
				}
				m.mu.Lock()
				out, err := e.mapping, e.err
				if e.closing {
					err = net.ErrClosed
				}
				m.mu.Unlock()
				return out, err
			}
		}
	}
	id := r.ID
	if id == "" {
		id = domain.NewID()
	}
	if m.entries[id] != nil {
		m.mu.Unlock()
		return Mapping{}, errors.New("转发创建操作已存在")
	}
	life, cancel := context.WithCancel(m.ctx)
	m.order++
	e := &entry{ctx: life, cancel: cancel, dial: dial, ready: make(chan struct{}), connections: map[net.Conn]bool{}, order: m.order, mapping: Mapping{ID: id, DeviceID: r.DeviceID, ConnectionID: r.ConnectionID, Name: r.Name, Target: r.Target, State: "connecting"}}
	e.wg.Add(1)
	m.entries[e.mapping.ID] = e
	m.mu.Unlock()
	m.notify()
	defer e.wg.Done()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.LocalPort)))
	if err != nil && r.AutoPort && (errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES)) {
		listener, err = net.Listen("tcp4", "127.0.0.1:0")
	}
	if err == nil {
		m.mu.Lock()
		e.listener = listener
		e.mapping.LocalPort = listener.Addr().(*net.TCPAddr).Port
		m.mu.Unlock()
		var conn net.Conn
		call, done := context.WithTimeout(life, 10*time.Second)
		conn, err = dial(call, r.Target)
		done()
		if conn != nil {
			conn.Close()
		}
	}
	m.mu.Lock()
	if life.Err() != nil {
		err = life.Err()
	}
	e.err = err
	if err != nil {
		cancel()
		if listener != nil {
			listener.Close()
		}
		delete(m.entries, e.mapping.ID)
	} else {
		e.mapping.State = "available"
		e.wg.Add(1)
		go m.accept(e)
	}
	close(e.ready)
	out := e.mapping
	m.mu.Unlock()
	m.notify()
	return out, err
}
func (m *Manager) accept(e *entry) {
	defer e.wg.Done()
	for {
		local, err := e.listener.Accept()
		if err != nil {
			return
		}
		m.mu.Lock()
		if e.closing || e.streams >= 32 {
			m.mu.Unlock()
			local.Close()
			continue
		}
		e.connections[local] = true
		e.streams++
		e.wg.Add(1)
		m.mu.Unlock()
		go m.stream(e, local)
	}
}
func (m *Manager) stream(e *entry, local net.Conn) {
	defer e.wg.Done()
	defer local.Close()
	defer func() {
		m.mu.Lock()
		delete(e.connections, local)
		e.streams--
		m.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	remote, err := e.dial(ctx, e.mapping.Target)
	cancel()
	m.mu.Lock()
	if err == nil && e.closing {
		remote.Close()
		err = net.ErrClosed
	}
	if !e.closing {
		if err != nil {
			e.mapping.State = "unavailable"
			e.mapping.Error = err.Error()
		} else {
			e.mapping.State = "available"
			e.mapping.Error = ""
			e.connections[remote] = true
		}
	}
	m.mu.Unlock()
	m.notify()
	if err != nil {
		return
	}
	defer remote.Close()
	defer func() { m.mu.Lock(); delete(e.connections, remote); m.mu.Unlock() }()
	Relay(e.ctx, local, remote)
}

// CancelID marks a close synchronously before its asynchronous cleanup, so
// no later browser action can pass the availability check.
func (m *Manager) CancelID(id string) {
	m.mu.Lock()
	if e := m.entries[id]; e != nil {
		e.closing = true
		e.mapping.State = "closing"
		e.cancel()
	}
	m.mu.Unlock()
	m.notify()
}
func (m *Manager) CloseID(id string) error {
	m.mu.Lock()
	e := m.entries[id]
	if e == nil {
		m.mu.Unlock()
		return nil
	}
	if e.closeStarted {
		m.mu.Unlock()
		e.wg.Wait()
		return nil
	}
	e.closeStarted = true
	e.closing = true
	e.mapping.State = "closing"
	e.cancel()
	if e.listener != nil {
		e.listener.Close()
	}
	for c := range e.connections {
		c.Close()
	}
	m.mu.Unlock()
	m.notify()
	e.wg.Wait()
	m.mu.Lock()
	delete(m.entries, id)
	m.mu.Unlock()
	m.notify()
	return nil
}
func (m *Manager) CloseDevice(device string) {
	for _, e := range m.Snapshot() {
		if e.DeviceID == device {
			m.CloseID(e.ID)
		}
	}
}
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		<-m.closedDone
		return nil
	}
	m.closed = true
	defer close(m.closedDone)
	m.cancel()
	m.mu.Unlock()
	for _, e := range m.Snapshot() {
		m.CloseID(e.ID)
	}
	return nil
}

// Open serializes a confirmed browser action with close. URL stays in memory only.
func (m *Manager) Open(id, raw string, open func(string) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil || e.closing || e.mapping.State != "available" {
		return errors.New("转发尚未可用")
	}
	address := "http://" + e.mapping.LocalAddress() + "/"
	var err error
	if raw != "" {
		address, err = LocalURL(raw, e.mapping.LocalPort)
		if err != nil {
			return err
		}
	} else if e.mapping.LastURL != "" {
		address = e.mapping.LastURL
	}
	e.mapping.LastURL = address
	return open(address)
}
func (m *Manager) Retry(ctx context.Context, id string) error {
	m.mu.Lock()
	e := m.entries[id]
	if e == nil || e.closing {
		m.mu.Unlock()
		return net.ErrClosed
	}
	e.wg.Add(1)
	m.mu.Unlock()
	defer e.wg.Done()
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	defer cancel()
	conn, err := e.dial(call, e.mapping.Target)
	if conn != nil {
		conn.Close()
	}
	m.mu.Lock()
	if e.closing {
		m.mu.Unlock()
		return net.ErrClosed
	}
	if err != nil {
		e.mapping.State = "unavailable"
		e.mapping.Error = err.Error()
	} else {
		e.mapping.State = "available"
		e.mapping.Error = ""
	}
	m.mu.Unlock()
	m.notify()
	return err
}
