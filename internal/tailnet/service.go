package tailnet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/runtimebuild"
	"sync"
	"syscall"
	"time"
)

const serviceVersion = 1
const maxServiceMessage = 1 << 20

type serviceRequest struct {
	Version                int
	Kind, Client, Instance string
	After, Sequence        uint64
	Action                 Action
	Peer, Address          string
	Port                   int
}
type serviceReply struct {
	Version                 int
	BuildID                 string
	Client, Instance, Error string
	Revision                uint64
	AcceptedSequence        uint64
	AcceptedID              string
	Snapshot                Snapshot
	AuthorizationURL        string `json:",omitempty"`
	Socket                  string `json:",omitempty"`
}
type serviceClient struct {
	seen       time.Time
	sequence   uint64
	lastAction Action
}
type networkService struct {
	node                      *Node
	instance                  string
	buildID                   string
	mu                        sync.Mutex
	clients                   map[string]*serviceClient
	authClient, authOperation string
	revision                  uint64
	changed                   chan struct{}
	nodeDone                  <-chan struct{}
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Keep the Unix address short even when a development profile has a long path.
// Only its hash is used here; the original directory still identifies the
// unchanged Keychain account and encrypted node state.
func ServiceSocket(directory string) string {
	abs, _ := filepath.Abs(directory)
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join("/tmp", fmt.Sprintf("shioji-network-%d-%x", os.Getuid(), sum[:12]), "control.sock")
}

func privateServiceListener(directory string) (*net.UnixListener, error) {
	path := ServiceSocket(directory)
	root := filepath.Dir(path)
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || !ok || stat.Uid != uint32(os.Getuid()) || st.Mode().Perm() != 0700 {
		return nil, errors.New("网络控制目录不安全")
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("网络控制路径已被占用")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: path})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Serve owns the node independently of every GUI/client connection. Only this
// process's service context, or explicit pause/logout actions, stops networking.
func Serve(ctx context.Context, node *Node) error {
	owner, err := lockIdentity(node.directory)
	if err != nil {
		return err
	}
	defer owner.Close()
	if _, err := readIntent(node.directory); err != nil {
		return errors.New("无法读取网络连接设置，请修复后重新连接")
	}
	l, err := privateServiceListener(node.directory)
	if err != nil {
		return err
	}
	defer func() { l.Close(); os.Remove(filepath.Dir(ServiceSocket(node.directory))) }()
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &networkService{node: node, instance: randomID(), buildID: runtimebuild.Current().BuildID, clients: map[string]*serviceClient{}, changed: make(chan struct{})}
	done := make(chan struct{})
	s.nodeDone = done
	go func() {
		defer close(done)
		node.run(ctx, func(Snapshot) {
			s.mu.Lock()
			s.revision++
			close(s.changed)
			s.changed = make(chan struct{})
			s.mu.Unlock()
		})
	}()
	go func() { <-done; cancel() }()
	defer func() { cancel(); <-done }()
	var wg sync.WaitGroup
	var conns sync.Map
	defer func() { conns.Range(func(k, _ any) bool { k.(net.Conn).Close(); return true }); wg.Wait() }()
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	slots := make(chan struct{}, 64)
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			if parent.Err() != nil {
				return nil
			}
			if ctx.Err() != nil {
				return errors.New("Tailnet 网络节点已停止，请重新连接")
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			c.Close()
			continue
		}
		conns.Store(c, true)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			defer conns.Delete(c)
			defer c.Close()
			c.SetDeadline(time.Now().Add(6 * time.Second))
			if _, err := peerProcess(c); err != nil {
				return
			}
			var req serviceRequest
			if json.NewDecoder(io.LimitReader(c, maxServiceMessage)).Decode(&req) != nil {
				return
			}
			reply := s.handle(ctx, req)
			json.NewEncoder(c).Encode(reply)
		}()
	}
}

func (s *networkService) handle(ctx context.Context, r serviceRequest) serviceReply {
	out := serviceReply{Version: serviceVersion, Instance: s.instance, BuildID: s.buildID}
	if r.Kind == "describe" {
		return out
	}
	if r.Version != serviceVersion {
		out.Error = "网络服务版本不兼容，请更新应用"
		return out
	}
	s.mu.Lock()
	if r.Kind == "hello" {
		for id, c := range s.clients {
			if time.Since(c.seen) > 5*time.Minute && id != s.authClient {
				delete(s.clients, id)
			}
		}
		if (r.Instance != s.instance || s.clients[r.Client] == nil) && len(s.clients) >= 128 {
			s.mu.Unlock()
			out.Error = "网络客户端过多，请稍后重试"
			return out
		}
		if r.Instance != s.instance || s.clients[r.Client] == nil {
			r.Client = randomID()
			s.clients[r.Client] = &serviceClient{seen: time.Now()}
		}
	} else if r.Instance != s.instance || s.clients[r.Client] == nil {
		s.mu.Unlock()
		out.Error = "网络服务连接已改变，请重试"
		return out
	}
	c := s.clients[r.Client]
	c.seen = time.Now()
	out.Client = r.Client
	switch r.Kind {
	case "hello", "snapshot":
		if r.Kind == "snapshot" && r.After >= s.revision {
			changed := s.changed
			s.mu.Unlock()
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
			case <-changed:
			case <-timer.C:
			}
			timer.Stop()
			s.mu.Lock()
		}
	case "action":
		select {
		case <-s.nodeDone:
			out.Error = "网络节点已停止，操作未接纳"
			s.mu.Unlock()
			return out
		default:
		}
		a := r.Action
		valid := a.Kind == "join" || a.Kind == "resume" || a.Kind == "pause" || a.Kind == "cancel" || a.Kind == "logout" || a.Kind == "incoming-on" || a.Kind == "incoming-off" || a.Kind == "refresh"
		if !valid || a.ID == "" || len(a.ID) > 128 || len(a.Hostname) > 63 || r.Sequence == 0 {
			out.Error = "网络操作无效"
		} else if r.Sequence <= c.sequence {
			if r.Sequence == c.sequence && a != c.lastAction {
				out.Error = "网络操作标识已被使用"
			}
		} else if !s.node.Do(a) {
			out.Error = "网络服务忙，请稍后重试"
		} else {
			c.sequence = r.Sequence
			c.lastAction = a
			if a.Kind == "join" {
				s.authClient = r.Client
				s.authOperation = a.ID
			} else if a.Kind == "cancel" || a.Kind == "pause" || a.Kind == "logout" {
				s.authClient = ""
				s.authOperation = ""
			}
		}
	case "route":
		out.Socket = ""
		var err error
		out.Socket, err = s.node.Route(r.Peer, r.Address, r.Port)
		if err != nil {
			out.Error = err.Error()
		}
	default:
		out.Error = "未知网络操作"
	}
	out.Revision = s.revision
	out.AcceptedSequence = c.sequence
	out.AcceptedID = c.lastAction.ID
	out.Snapshot = s.node.Snapshot()
	if s.authClient == r.Client && out.Snapshot.OperationID == s.authOperation {
		out.AuthorizationURL = out.Snapshot.AuthURL
	}
	out.Snapshot.AuthURL = ""
	if !out.Snapshot.Authorizing && out.Snapshot.OperationID == s.authOperation {
		s.authClient = ""
		s.authOperation = ""
	}
	s.mu.Unlock()
	return out
}
