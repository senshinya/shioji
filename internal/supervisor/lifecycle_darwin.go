package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
)

const lifecycleFDEnv = "SHIOJI_WORKER_LIFECYCLE_FD"
const lifecycleFrameLimit = 64 << 10
const PrivateControlVersion = 2 // Adds resume without ending the child channel.

type lifecycleReply struct {
	AttemptID string                    `json:"attemptId,omitempty"`
	Result    domain.WorkerUpdateResult `json:"result"`
	Error     string                    `json:"error,omitempty"`
}

type lifecycleClient struct {
	conn      net.Conn
	reader    *bufio.Reader
	mu        sync.Mutex
	calls     chan struct{}
	writes    chan []byte
	done      chan struct{}
	closeOnce sync.Once
	err       error
	pendingID string
	pending   chan lifecycleReply
}

func lifecyclePair() (*lifecycleClient, *os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}
	unix.CloseOnExec(fds[0])
	unix.CloseOnExec(fds[1])
	parent := os.NewFile(uintptr(fds[0]), "worker lifecycle parent")
	child := os.NewFile(uintptr(fds[1]), "worker lifecycle child")
	conn, err := net.FileConn(parent)
	parent.Close()
	if err != nil {
		child.Close()
		return nil, nil, err
	}
	return newLifecycleClient(conn), child, nil
}
func newLifecycleClient(conn net.Conn) *lifecycleClient {
	c := &lifecycleClient{conn: conn, reader: bufio.NewReaderSize(conn, lifecycleFrameLimit), calls: make(chan struct{}, 1), writes: make(chan []byte, 1), done: make(chan struct{})}
	go c.readReplies()
	go c.writeRequests()
	return c
}
func (c *lifecycleClient) Close() error { c.fail(net.ErrClosed); return nil }
func (c *lifecycleClient) fail(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		c.conn.Close()
		close(c.done)
	})
}
func (c *lifecycleClient) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Each child owns one reader and one writer. A canceled caller relinquishes
// only its reply slot: a partial frame must finish before another is written.
// The single-slot write queue bounds even a live child that stops reading.
func (c *lifecycleClient) writeRequests() {
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.writes:
			for len(frame) > 0 {
				n, err := c.conn.Write(frame)
				if err == nil && n == 0 {
					err = io.ErrNoProgress
				}
				if err != nil {
					c.fail(err)
					return
				}
				frame = frame[n:]
			}
		}
	}
}
func (c *lifecycleClient) readReplies() {
	for {
		raw, err := c.reader.ReadSlice('\n')
		if err != nil {
			c.fail(err)
			return
		}
		var reply lifecycleReply
		if err = json.Unmarshal(raw, &reply); err != nil {
			c.fail(err)
			return
		}
		c.mu.Lock()
		if c.pending != nil && reply.AttemptID == c.pendingID {
			select {
			case c.pending <- reply:
			default:
			}
		}
		c.mu.Unlock()
	}
}
func (c *lifecycleClient) request(ctx context.Context, in Control) (domain.WorkerUpdateResult, error) {
	select {
	case c.calls <- struct{}{}:
		defer func() { <-c.calls }()
	case <-ctx.Done():
		return domain.WorkerUpdateResult{}, ctx.Err()
	case <-c.done:
		return domain.WorkerUpdateResult{}, c.failure()
	}
	if err := ctx.Err(); err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	in.AttemptID = domain.NewID()
	raw, err := json.Marshal(in)
	if err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	raw = append(raw, '\n')
	if len(raw) > lifecycleFrameLimit {
		return domain.WorkerUpdateResult{}, errors.New("lifecycle request exceeds frame limit")
	}
	replies := make(chan lifecycleReply, 1)
	c.mu.Lock()
	c.pendingID, c.pending = in.AttemptID, replies
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.pendingID, c.pending = "", nil; c.mu.Unlock() }()
	select {
	case c.writes <- raw:
	case <-ctx.Done():
		return domain.WorkerUpdateResult{}, ctx.Err()
	case <-c.done:
		return domain.WorkerUpdateResult{}, c.failure()
	}
	var reply lifecycleReply
	select {
	case reply = <-replies:
	case <-ctx.Done():
		return domain.WorkerUpdateResult{}, ctx.Err()
	case <-c.done:
		// The accepted reply may immediately precede child EOF.
		select {
		case reply = <-replies:
		default:
			return domain.WorkerUpdateResult{}, c.failure()
		}
	}
	if reply.Error != "" {
		return reply.Result, errors.New(reply.Error)
	}
	return reply.Result, nil
}

// OpenChildLifecycle consumes only the private descriptor inherited from the
// exact supervisor spawn. It is removed from the environment before harnesses
// start, and FileConn's duplicated descriptor is close-on-exec.
func OpenChildLifecycle() (net.Conn, error) {
	value := os.Getenv(lifecycleFDEnv)
	os.Unsetenv(lifecycleFDEnv)
	if value == "" {
		return nil, nil
	}
	if value != "3" {
		return nil, errors.New("invalid worker lifecycle descriptor")
	}
	file := os.NewFile(3, "worker lifecycle")
	defer file.Close()
	return net.FileConn(file)
}

// ServeLifecycle acknowledges admission before signalling shutdown. An accepted
// request remains accepted if its reply is lost; the durable drain identity and
// final receipt decide the parent's outcome. Rejections leave this pipe usable.
func ServeLifecycle(conn net.Conn, handle func(Control) (domain.WorkerUpdateResult, error)) (bool, error) {
	reader := bufio.NewReaderSize(conn, lifecycleFrameLimit)
	for {
		raw, err := reader.ReadSlice('\n')
		if err != nil {
			return false, err
		}
		var in Control
		if err = json.Unmarshal(raw, &in); err != nil {
			return false, err
		}
		result, err := handle(in)
		reply := lifecycleReply{AttemptID: in.AttemptID, Result: result}
		if err != nil {
			reply.Error = err.Error()
		}
		accepted := err == nil && result.Ready && in.Action != "resume"
		writeErr := json.NewEncoder(conn).Encode(reply)
		if writeErr != nil || accepted {
			return accepted, writeErr
		}
	}
}
