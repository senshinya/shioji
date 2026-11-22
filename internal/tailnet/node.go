// Package tailnet owns the application's userspace node, never the system client.
package tailnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

type Measurement struct {
	Milliseconds             float64
	ObservedAt               time.Time
	Path, Region, RegionName string
}
type Peer struct {
	ID, Name, DNSName, Address, OS string
	Online                         bool
	Measurement                    *Measurement
}
type Snapshot struct {
	State, Hostname, Network, Address, Error string
	OperationID                              string
	Authorizing                              bool
	AuthURL                                  string `json:"-"`
	Peers                                    []Peer
	Incoming                                 bool
	InboundEnabled                           bool
}
type Action struct{ Kind, Hostname, ID string }
type routeKey struct {
	peer, address string
	port          int
}
type Node struct {
	inbound      *inboundForwarder
	forwarding   bool
	outboundOnly bool
	staging      bool
	directory    string
	secret       func() ([]byte, error)
	actions      chan Action
	mu           sync.Mutex
	server       *tsnet.Server
	status       Snapshot
	bridges      map[routeKey]*bridge
	outbound     map[*outboundStream]routeKey
	bridgeDir    string
}

func New(directory string, secret func() ([]byte, error)) *Node {
	return &Node{directory: directory, secret: secret, actions: make(chan Action, 16), forwarding: true, bridges: map[routeKey]*bridge{}}
}

// NewOutboundNode is for control clients that have no inbound execution service.
func NewOutboundNode(directory string, secret func() ([]byte, error)) *Node {
	n := New(directory, secret)
	n.outboundOnly = true
	n.forwarding = false
	return n
}

// DialPeer opens an authorized SSH transport directly through the embedded node.
func (n *Node) DialPeer(ctx context.Context, peerID, address string) (net.Conn, error) {
	dst, err := netip.ParseAddrPort(address)
	if err != nil || dst.Port() == 0 || dst.Addr().Zone() != "" {
		return nil, errors.New("Tailnet SSH 地址无效")
	}
	host := dst.Addr().String()
	n.mu.Lock()
	server, allowed := n.server, n.canDialPeerLocked(peerID, host)
	n.mu.Unlock()
	if server == nil || !allowed {
		return nil, errors.New("Tailnet 设备不可达或地址已改变")
	}
	sys := server.Sys()
	if sys == nil {
		return nil, errors.New("内嵌网络尚未就绪")
	}
	dialer, _ := sys.Dialer.GetOK()
	conn, err := dialNetstack(ctx, dialer, dst)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	allowed = n.server == server && n.canDialPeerLocked(peerID, host)
	if allowed {
		conn = n.trackOutboundLocked(conn, routeKey{peer: peerID, address: host, port: int(dst.Port())})
	}
	n.mu.Unlock()
	if !allowed {
		conn.Close()
		return nil, errors.New("Tailnet 设备不可达或地址已改变")
	}
	return conn, nil
}

type outboundStream struct {
	net.Conn
	node *Node
}

func (s *outboundStream) Close() error {
	s.node.mu.Lock()
	delete(s.node.outbound, s)
	s.node.mu.Unlock()
	return s.Conn.Close()
}

func (n *Node) trackOutboundLocked(conn net.Conn, route routeKey) *outboundStream {
	if n.outbound == nil {
		n.outbound = make(map[*outboundStream]routeKey)
	}
	stream := &outboundStream{Conn: conn, node: n}
	n.outbound[stream] = route
	return stream
}

// Detach revoked connections under the same lock that publishes peer
// visibility. Close the underlying streams after releasing that lock.
func (n *Node) detachOutboundLocked() []net.Conn {
	var retiring []net.Conn
	for stream, route := range n.outbound {
		if n.server != nil && n.canDialPeerLocked(route.peer, route.address) {
			continue
		}
		delete(n.outbound, stream)
		retiring = append(retiring, stream.Conn)
	}
	return retiring
}

func closeOutboundStreams(streams []net.Conn) {
	for _, stream := range streams {
		stream.Close()
	}
}

func (n *Node) canDialPeerLocked(id, address string) bool {
	if id == "" || n.status.State != "Running" {
		return false
	}
	for _, peer := range n.status.Peers {
		if peer.ID == id && peer.Address == address && peer.Online {
			return true
		}
	}
	return false
}

func (n *Node) Do(a Action) bool {
	select {
	case n.actions <- a:
		return true
	default:
		return false
	}
}
func (n *Node) Snapshot() Snapshot {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.status
	s.Peers = append([]Peer(nil), s.Peers...)
	return s
}

type peerMeasurement struct {
	address string
	sample  *Measurement
}

func snapshotFromStatus(st *ipnstate.Status, measurements map[string]peerMeasurement) Snapshot {
	s := Snapshot{State: st.BackendState}
	if st.CurrentTailnet != nil {
		s.Network = st.CurrentTailnet.Name
	}
	if st.Self != nil {
		s.Hostname = st.Self.HostName
	}
	if len(st.TailscaleIPs) > 0 {
		s.Address = st.TailscaleIPs[0].String()
	}
	for _, p := range st.Peer {
		if p.ID == "" || p.ShareeNode || !p.InNetworkMap || len(p.TailscaleIPs) == 0 {
			continue
		}
		peer := Peer{ID: string(p.ID), Name: p.HostName, DNSName: strings.TrimSuffix(p.DNSName, "."), Address: p.TailscaleIPs[0].String(), OS: p.OS, Online: p.Online}
		if observed := measurements[peer.ID]; observed.address == peer.Address {
			peer.Measurement = observed.sample
		}
		s.Peers = append(s.Peers, peer)
	}
	sort.Slice(s.Peers, func(i, j int) bool {
		if s.Peers[i].Online != s.Peers[j].Online {
			return s.Peers[i].Online
		}
		return s.Peers[i].Name < s.Peers[j].Name
	})
	return s
}
func measurementFromPing(p *ipnstate.PingResult, now time.Time) *Measurement {
	if p == nil || p.Err != "" || p.IsLocalIP || p.LatencySeconds <= 0 {
		return nil
	}
	m := &Measurement{Milliseconds: p.LatencySeconds * 1000, ObservedAt: now, Path: "unknown"}
	switch {
	case p.PeerRelay != "":
		m.Path = "peer-relay"
	case p.Endpoint != "":
		m.Path = "direct"
	case p.DERPRegionID != 0:
		m.Path = "derp"
		m.Region = p.DERPRegionCode
	}
	return m
}
func (n *Node) start(hostname string, staging bool) (*local.Client, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.server != nil {
		return n.server.LocalClient()
	}
	if err := os.MkdirAll(n.directory, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(n.directory, 0700); err != nil {
		return nil, err
	}
	if !staging {
		if _, err := os.Stat(filepath.Join(n.directory, "identity")); err != nil {
			return nil, fmt.Errorf("网络身份不可用：%w", ErrUserAction)
		}
	}
	key, err := n.secret()
	if err != nil {
		return nil, fmt.Errorf("无法读取 Tailnet 身份密钥：%w", ErrUserAction)
	}
	root := n.directory
	if staging {
		root = filepath.Join(root, "pending")
		if err = os.MkdirAll(root, 0700); err != nil {
			clear(key)
			return nil, err
		}
	}
	store, err := openStore(filepath.Join(root, "identity"), key)
	clear(key)
	if err != nil {
		return nil, fmt.Errorf("无法读取 Tailnet 身份：%w", ErrUserAction)
	}
	configurePrivacy()
	s := &tsnet.Server{Dir: root, Store: store, Hostname: hostname, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
	if err = s.Start(); err != nil {
		s.Close()
		return nil, errors.New("无法启动内嵌 Tailnet 节点")
	}
	n.server = s
	return s.LocalClient()
}
func (n *Node) publish(s Snapshot, changed func(Snapshot)) {
	n.mu.Lock()
	if (s.State != "Running" || !n.forwarding) && n.inbound != nil {
		n.inbound.Close()
		n.inbound = nil
	}
	if s.State == "Running" && n.forwarding && n.inbound == nil && n.server != nil {
		n.inbound = newInbound(n.server.RegisterFallbackTCPHandler, n.server.TailscaleIPs)
	}
	s.Incoming = n.inbound != nil
	s.InboundEnabled = n.forwarding
	n.status = s
	for key, b := range n.bridges {
		if !n.canDialPeerLocked(key.peer, key.address) {
			b.Drop()
		}
	}
	retiring := n.detachOutboundLocked()
	n.mu.Unlock()
	closeOutboundStreams(retiring)
	changed(s)
}
func (n *Node) Run(ctx context.Context, changed func(Snapshot)) {
	owner, err := lockIdentity(n.directory)
	if err != nil {
		n.publish(Snapshot{State: "Offline", Error: err.Error()}, changed)
		return
	}
	defer owner.Close()
	n.run(ctx, changed)
}

func (n *Node) run(ctx context.Context, changed func(Snapshot)) {
	defer n.Close()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	intent, err := readIntent(n.directory)
	if err != nil {
		n.publish(Snapshot{State: "Offline", Error: "无法读取网络连接设置"}, changed)
		return
	}
	n.mu.Lock()
	n.forwarding = !n.outboundOnly && intent.forwardingEnabled()
	n.mu.Unlock()
	hostname := intent.Hostname
	if hostname == "" {
		hostname = "shioji-mac"
	}
	var lc *local.Client
	var last Snapshot
	operation := ""
	operationError := ""
	login := false
	measurements := map[string]peerMeasurement{}
	var retry startupRetry
	stopped := func() Snapshot {
		state := "NotStarted"
		if intent.Mode == "paused" {
			state = "Stopped"
		}
		return Snapshot{State: state, Hostname: hostname, OperationID: operation}
	}
	fail := func(message string) {
		operationError = message
		if n.staging {
			last.State = "NeedsLogin"
		} else if lc == nil && intent.Mode == "connected" {
			last.State = "Offline"
		}
		last.Error = message
		last.AuthURL = ""
		last.Authorizing = false
		n.publish(last, changed)
	}
	poll := func() {
		if lc == nil {
			return
		}
		call, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		st, e := lc.Status(call)
		if e != nil {
			last.State = "Offline"
			last.AuthURL = ""
			last.Error = "无法读取网络状态"
			n.publish(last, changed)
			return
		}
		s := snapshotFromStatus(st, measurements)
		s.OperationID = operation
		s.Error = operationError
		s.Authorizing = login
		if login {
			s.AuthURL = st.AuthURL
		}
		if s.State == "Running" && login {
			if n.staging {
				n.mu.Lock()
				store := n.server.Store.(*encryptedStore)
				n.mu.Unlock()
				if e = store.promote(filepath.Join(n.directory, "identity")); e != nil {
					fail("无法保存网络身份，请重试")
					return
				}
				n.staging = false
			}
			intent = nodeIntent{Mode: "connected", Hostname: s.Hostname, Forwarding: intent.Forwarding}
			if e = writeIntent(n.directory, intent); e != nil {
				fail("无法保存网络连接设置")
				return
			}
			login = false
			s.Authorizing = false
			s.AuthURL = ""
		}
		last = s
		n.publish(s, changed)
	}
	if intent.Mode == "connected" {
		lc, err = n.start(hostname, false)
		if err != nil {
			retry.fail(err, time.Now())
			last = Snapshot{State: "Offline", Error: err.Error()}
			n.publish(last, changed)
		} else {
			poll()
		}
	} else {
		last = stopped()
		if intent.Mode == "" {
			if _, e := os.Stat(filepath.Join(n.directory, "identity")); e == nil {
				last.State = "Stopped"
			}
		}
		n.publish(last, changed)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-n.actions:
			if a.ID != "" && a.ID == operation {
				continue
			}
			if a.ID != "" {
				operation = a.ID
			}
			last.OperationID = operation
			operationError = ""
			call, cancel := context.WithTimeout(ctx, 8*time.Second)
			err = nil
			switch a.Kind {
			case "refresh":
				// Refresh the existing owner below, without starting or reenrolling it.
				if lc == nil {
					n.publish(last, changed)
				}
			case "join":
				// A new browser enrollment gets its own provisional keys. A canceled URL
				// can never authorize a later attempt or overwrite the old enrolled node.
				if err = n.retirePending(); err == nil {
					clear(measurements)
					lc = nil
					hostname = a.Hostname
					if hostname == "" {
						hostname = "shioji-mac"
					}
					last = Snapshot{State: "Starting", OperationID: operation, Authorizing: true}
					n.publish(last, changed)
					n.staging = true
					lc, err = n.start(hostname, true)
				}
				if err == nil {
					login = true
					err = lc.StartLoginInteractive(call)
				}
			case "resume":
				retry.clear()
				if n.staging {
					err = n.retirePending()
					lc = nil
				}
				if err == nil {
					hostname = intent.Hostname
					if hostname == "" {
						hostname = "shioji-mac"
					}
					intent = nodeIntent{Mode: "connected", Hostname: hostname, Forwarding: intent.Forwarding}
					err = writeIntent(n.directory, intent)
					if err == nil {
						lc, err = n.start(hostname, false)
					}
				}
				if err == nil {
					_, err = lc.EditPrefs(call, &ipn.MaskedPrefs{WantRunningSet: true, Prefs: ipn.Prefs{WantRunning: true}})
					if err == nil {
						intent = nodeIntent{Mode: "connected", Hostname: hostname, Forwarding: intent.Forwarding}
						err = writeIntent(n.directory, intent)
					}
				}
			case "cancel":
				retry.clear()
				login = false
				if n.staging {
					err = n.retirePending()
					lc = nil
				}
				if err == nil && intent.Mode == "connected" {
					lc, err = n.start(intent.Hostname, false)
				}
				last = stopped()
				n.publish(last, changed)
			case "pause":
				retry.clear()
				login = false
				if n.staging {
					err = n.retirePending()
				} else {
					n.closeServer()
				}
				lc = nil
				intent = nodeIntent{Mode: "paused", Hostname: hostname, Forwarding: intent.Forwarding}
				if err == nil {
					err = writeIntent(n.directory, intent)
				}
				last = stopped()
				n.publish(last, changed)
			case "logout":
				retry.clear()
				login = false
				clear(measurements)
				if lc != nil {
					err = lc.Logout(call)
				}
				if n.staging {
					e := n.retirePending()
					if err == nil {
						err = e
					}
				} else {
					n.closeServer()
				}
				lc = nil
				intent = nodeIntent{Mode: "signed-out", Hostname: hostname, Forwarding: intent.Forwarding}
				if err == nil {
					err = writeIntent(n.directory, intent)
				}
				last = stopped()
				n.publish(last, changed)
			case "incoming-on", "incoming-off":
				if n.outboundOnly {
					continue
				}
				enabled := a.Kind == "incoming-on"
				next := intent
				next.Forwarding = &enabled
				err = writeIntent(n.directory, next)
				if err == nil {
					intent = next
					n.mu.Lock()
					n.forwarding = enabled
					n.mu.Unlock()
					n.publish(last, changed)
				}

			}
			cancel()
			if err != nil {
				if lc == nil && intent.Mode == "connected" {
					retry.fail(err, time.Now())
				}
				message := "网络操作失败，请重试"
				if a.Kind == "incoming-on" {
					message = "无法保存入站转发设置，请重试"
				}
				fail(message)
			} else if lc != nil {
				poll()
			}
		case <-ticker.C:
			n.mu.Lock()
			n.pruneBridgesLocked(time.Now())
			n.mu.Unlock()
			if lc == nil && intent.Mode == "connected" && retry.due(time.Now()) {
				lc, err = n.start(hostname, false)
				if err != nil {
					retry.fail(err, time.Now())
					last.State, last.Error = "Offline", err.Error()
					n.publish(last, changed)
				} else {
					retry.clear()
					operationError = ""
				}
			}
			poll()
			if lc != nil && last.State == "Running" {
				for _, p := range last.Peers {
					if !p.Online {
						continue
					}
					m := measurements[p.ID]
					if m.address == p.Address && m.sample != nil && time.Since(m.sample.ObservedAt) < 15*time.Second {
						continue
					}
					call, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
					ip, e := netip.ParseAddr(p.Address)
					if e == nil {
						res, e := lc.Ping(call, ip, tailcfg.PingDisco)
						if e == nil {
							if sample := measurementFromPing(res, time.Now()); sample != nil {
								if sample.Path == "derp" {
									if dm, e := lc.CurrentDERPMap(call); e == nil {
										if r := dm.Regions[res.DERPRegionID]; r != nil {
											sample.RegionName = r.RegionName
										}
									}
								}
								measurements[p.ID] = peerMeasurement{address: p.Address, sample: sample}
							}
						}
					}
					cancel()
					break
				}
			}
		}
	}
}

// Route exposes only one visible peer and SSH port, never a general TCP proxy.
func (n *Node) Route(peer, address string, port int) (string, error) {
	if peer == "" || address == "" || port < 0 || port > 65535 {
		return "", errors.New("Tailnet 连接无效")
	}
	if port == 0 {
		port = 22
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.bridgeDir == "" {
		var err error
		n.bridgeDir, err = os.MkdirTemp("/tmp", "shioji-tailnet-")
		if err != nil {
			return "", err
		}
	}
	key := routeKey{peer, address, port}
	n.pruneBridgesLocked(time.Now())
	if b := n.bridges[key]; b != nil {
		b.mu.Lock()
		b.lastUse = time.Now()
		b.mu.Unlock()
		return b.listener.Addr().String(), nil
	}
	if len(n.bridges) >= 128 {
		return "", errors.New("Tailnet 连接路径过多，请稍后重试")
	}
	path := filepath.Join(n.bridgeDir, "ssh-"+randomID()+".sock")
	b, err := newBridge(path, func(ctx context.Context) (net.Conn, error) {
		n.mu.Lock()
		s, st := n.server, n.status
		allowed := false
		for _, p := range st.Peers {
			if p.ID == peer && p.Address == address && p.Online {
				allowed = true
				break
			}
		}
		n.mu.Unlock()
		if s == nil || st.State != "Running" || !allowed {
			return nil, errors.New("Tailnet 设备不可达")
		}
		ip, err := netip.ParseAddr(address)
		if err != nil {
			return nil, err
		}
		return dialNetstack(ctx, s.Sys().Dialer.Get(), netip.AddrPortFrom(ip, uint16(port)))
	})
	if err != nil {
		return "", err
	}
	n.bridges[key] = b
	return path, nil
}

func (n *Node) pruneBridgesLocked(now time.Time) {
	for key, b := range n.bridges {
		if b.idleBefore(now.Add(-2 * time.Minute)) {
			b.Close()
			delete(n.bridges, key)
		}
	}
}
func (n *Node) Close() {
	n.mu.Lock()
	s := n.server
	if n.inbound != nil {
		n.inbound.Close()
		n.inbound = nil
	}
	n.server = nil
	retiring := n.detachOutboundLocked()
	for _, b := range n.bridges {
		b.Close()
	}
	n.bridges = map[routeKey]*bridge{}
	dir := n.bridgeDir
	n.bridgeDir = ""
	n.mu.Unlock()
	closeOutboundStreams(retiring)
	if s != nil {
		s.Close()
	}
	if dir != "" {
		os.RemoveAll(dir)
	}
}
