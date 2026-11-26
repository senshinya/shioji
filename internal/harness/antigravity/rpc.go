package antigravity

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"shioji/internal/harness"
)

type packet struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

type rpc struct {
	in       io.WriteCloser
	out      io.Reader
	cmd      *exec.Cmd
	done     chan struct{}
	waitDone chan struct{}
	handler  func(packet) error
	onLost   func() error
	closing  atomic.Bool
	ready    atomic.Bool

	writeMu sync.Mutex
	pending sync.Map // numeric client request ID -> func(packet) error
	next    atomic.Uint64
}

func startRPC(ctx context.Context, argv, env []string, opts harness.StartOptions, handler func(packet) error, directory ...string) (*rpc, error) {
	var cmd *exec.Cmd
	var in io.WriteCloser
	var out io.Reader
	var err error
	if opts.Scope != nil {
		cmd, in, out, err = opts.Scope.StartOwned(ctx, "agent", argv, env, opts.OnStart)
	} else {
		cmd, in, out, err = startProcess(argv, env, opts.OnStart, directory...)
	}
	if err != nil {
		return nil, err
	}
	c := &rpc{in: in, out: out, cmd: cmd, done: make(chan struct{}), waitDone: make(chan struct{}), handler: handler, onLost: opts.OnLost}
	return c, nil
}

func startProcess(argv, env []string, onStart func(int) error, directory ...string) (*exec.Cmd, io.WriteCloser, io.Reader, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	if len(directory) > 0 {
		cmd.Dir = directory[0]
	}
	cmd.Env = env
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, nil, nil, err
	}
	err = cmd.Start()
	if err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, nil, nil, err
	}
	if onStart != nil {
		if err := onStart(cmd.Process.Pid); err != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			return nil, nil, nil, err
		}
	}
	return cmd, in, out, nil
}

func (c *rpc) read() {
	scan := bufio.NewScanner(c.out)
	scan.Buffer(make([]byte, 64<<10), 16<<20)
	for scan.Scan() {
		var msg packet
		if json.Unmarshal(scan.Bytes(), &msg) != nil || msg.Version != "2.0" {
			continue
		}
		var err error
		switch {
		case msg.Method != "":
			err = c.handler(msg)
		case len(msg.ID) > 0:
			var id uint64
			if json.Unmarshal(msg.ID, &id) == nil {
				if callback, exists := c.pending.LoadAndDelete(id); exists {
					err = callback.(func(packet) error)(msg)
				}
			}
		}
		if err != nil {
			break
		}
	}
	// Terminate a process that stopped speaking the dedicated transport.
	_ = c.in.Close()
	_ = c.cmd.Process.Signal(syscall.SIGTERM)
	close(c.done)
	go func() {
		if c.ready.Load() && !c.closing.Load() && c.onLost != nil {
			_ = c.onLost()
		}
	}()
	_ = c.cmd.Wait()
	close(c.waitDone)
}

func (c *rpc) unavailable() bool {
	select {
	case <-c.done:
		return true
	default:
		return c.closing.Load()
	}
}

func (c *rpc) request(ctx context.Context, method string, params any, callback func(packet) error) (uint64, error) {
	id := c.next.Add(1)
	c.pending.Store(id, callback)
	if err := c.send(ctx, id, method, params); err != nil {
		c.pending.Delete(id)
		return 0, err
	}
	return id, nil
}

func (c *rpc) send(ctx context.Context, id uint64, method string, params any) error {
	return c.write(ctx, struct {
		Version string `json:"jsonrpc"`
		ID      uint64 `json:"id,omitempty"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params})
}

type writeError struct{ error }

func (c *rpc) write(ctx context.Context, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.unavailable() {
		return os.ErrClosed
	}
	if pipe, ok := c.in.(interface{ SetWriteDeadline(time.Time) error }); ok {
		deadline := time.Now().Add(5 * time.Second)
		if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
			deadline = end
		}
		_ = pipe.SetWriteDeadline(deadline)
	}
	n, err := c.in.Write(data)
	if err != nil || n != len(data) {
		_ = c.in.Close() // A partial line cannot be reused for a later request.
		return &writeError{errors.Join(err, io.ErrShortWrite)}
	}
	return nil
}

func (c *rpc) call(ctx context.Context, method string, params, target any) error {
	reply := make(chan packet, 1)
	id, err := c.request(ctx, method, params, func(p packet) error { reply <- p; return nil })
	if err != nil {
		return err
	}
	defer c.pending.Delete(id)
	select {
	case r := <-reply:
		if r.Error != nil {
			return r.Error
		}
		if target != nil {
			return json.Unmarshal(r.Result, target)
		}
		return nil
	case <-c.done:
		return os.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}
