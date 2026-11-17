package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
)

const lifecycleFDEnv = "SHIOJI_WORKER_LIFECYCLE_FD"
const lifecycleFrameLimit = 64 << 10

type lifecycleReply struct {
	Result domain.WorkerUpdateResult `json:"result"`
	Error  string                    `json:"error,omitempty"`
}

type lifecycleClient struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
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
	return &lifecycleClient{conn: conn, reader: bufio.NewReaderSize(conn, lifecycleFrameLimit)}, child, nil
}
func (c *lifecycleClient) Close() error { return c.conn.Close() }
func (c *lifecycleClient) request(ctx context.Context, in Control) (domain.WorkerUpdateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.WorkerUpdateResult{}, err
	}
	deadline, _ := ctx.Deadline()
	c.conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { c.conn.Close() })
	defer stop()
	if err := json.NewEncoder(c.conn).Encode(in); err != nil {
		c.conn.Close()
		return domain.WorkerUpdateResult{}, err
	}
	raw, err := c.reader.ReadSlice('\n')
	if err != nil {
		c.conn.Close()
		return domain.WorkerUpdateResult{}, err
	}
	var reply lifecycleReply
	if err = json.Unmarshal(raw, &reply); err != nil {
		c.conn.Close()
		return reply.Result, err
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
		reply := lifecycleReply{Result: result}
		if err != nil {
			reply.Error = err.Error()
		}
		accepted := err == nil && result.Ready
		writeErr := json.NewEncoder(conn).Encode(reply)
		if writeErr != nil || accepted {
			return accepted, writeErr
		}
	}
}
