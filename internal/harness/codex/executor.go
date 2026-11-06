package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
	"shioji/internal/harness/codex/wire"
	"shioji/internal/managedexec"
)

type ExecutorOptions struct {
	Executable, Home string
	Scope            *managedexec.Scope
	OnStart          func(int) error
	OnLost           func() error
}
type executorBootstrap struct {
	id         string
	ack        managedexec.Acknowledgement
	prefix     []byte
	hidden     map[uint64]bool
	ready      chan struct{}
	once       sync.Once
	err        error
	registered bool
}

func (b *executorBootstrap) finish(err error) {
	b.once.Do(func() { b.err = err; b.ack = managedexec.Acknowledgement{}; close(b.ready) })
}

type executorRequest struct{ method, process string }
type Executor struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	writer         sync.Mutex
	mu             sync.Mutex
	pending        map[string]chan wire.Message
	requests       map[string]executorRequest
	boots          map[string]*executorBootstrap
	agent          *websocket.Conn
	initialization json.RawMessage
	token, url     string
	scope          *managedexec.Scope
	baseEnv        map[string]string
	listener       net.Listener
	server         *http.Server
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	admit          sync.Mutex
	fenced         bool
	closeOnce      sync.Once
	closeErr       error
	onLost         func() error
}

func randomExecutorID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func StartExecutor(ctx context.Context, opts ExecutorOptions) (*Executor, error) {
	if opts.Scope == nil {
		return nil, errors.New("executor requires a managed execution scope")
	}
	binary := opts.Executable
	if binary == "" {
		binary = "codex"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, err
	}
	env := os.Environ()
	if opts.Home != "" {
		env = append(env, "CODEX_HOME="+opts.Home)
	}
	ownerCtx, cancel := context.WithCancel(context.Background())
	e := &Executor{scope: opts.Scope, ctx: ownerCtx, cancel: cancel, done: make(chan struct{}), token: randomExecutorID() + randomExecutorID(), baseEnv: map[string]string{}, pending: map[string]chan wire.Message{}, requests: map[string]executorRequest{}, boots: map[string]*executorBootstrap{}}
	e.onLost = opts.OnLost
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			e.baseEnv[k] = v
		}
	}
	onStart := func(pid int) error {
		if err := opts.Scope.Bind("executor", pid); err != nil {
			return err
		}
		if opts.OnStart != nil {
			return opts.OnStart(pid)
		}
		return nil
	}
	var stdout io.Reader
	e.cmd, e.stdin, stdout, err = opts.Scope.StartOwned(ctx, "executor-runtime", []string{resolved, "exec-server", "--listen", "stdio", "--concurrent-requests", "32"}, env, onStart)
	if err != nil {
		cancel()
		return nil, err
	}
	go e.read(stdout)
	init, err := e.call(ctx, "initialize", map[string]string{"clientName": "shioji-worker"})
	if err == nil {
		e.initialization = init
		err = e.send(wire.Message{Method: "initialized", Params: json.RawMessage(`{}`)})
	}
	if err != nil {
		e.Close(context.Background())
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		e.Close(context.Background())
		return nil, err
	}
	e.listener = listener
	e.url = "ws://" + listener.Addr().String() + "/execution"
	ws := websocket.Server{Handshake: func(_ *websocket.Config, r *http.Request) error {
		if r.Header.Get("Authorization") != "Bearer "+e.token {
			return errors.New("unauthorized execution connection")
		}
		return nil
	}, Handler: e.serveAgent}
	mux := http.NewServeMux()
	mux.Handle("/execution", ws)
	e.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	go e.server.Serve(listener)
	return e, nil
}

func (e *Executor) URL() string           { return e.url }
func (e *Executor) Token() string         { return e.token }
func (e *Executor) Done() <-chan struct{} { return e.done }
func (e *Executor) Stopped() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}
func (e *Executor) send(msg wire.Message) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(b) > 16<<20 {
		return errors.New("execution frame too large")
	}
	e.writer.Lock()
	defer e.writer.Unlock()
	_, err = e.stdin.Write(append(b, '\n'))
	return err
}
func (e *Executor) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	key := "shioji-owned:" + randomExecutorID()
	id, _ := json.Marshal(key)
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	response := make(chan wire.Message, 1)
	e.mu.Lock()
	e.pending[string(id)] = response
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.pending, string(id)); e.mu.Unlock() }()
	if err = e.send(wire.Message{ID: id, Method: method, Params: b}); err != nil {
		return nil, err
	}
	select {
	case msg := <-response:
		if msg.Error != nil {
			return nil, errors.New("executor rejected owned operation: " + msg.Error.Message)
		}
		return msg.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, errors.New("owned executor stopped")
	}
}
func (e *Executor) read(reader io.Reader) {
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 64<<10), 16<<20)
	for scan.Scan() {
		var msg wire.Message
		if json.Unmarshal(scan.Bytes(), &msg) != nil {
			break
		}
		e.mu.Lock()
		response := e.pending[string(msg.ID)]
		request, fromAgent := e.requests[string(msg.ID)]
		if fromAgent && msg.Method == "" {
			delete(e.requests, string(msg.ID))
		}
		e.mu.Unlock()
		if response != nil && msg.Method == "" {
			response <- msg
			continue
		}
		var ownID string
		if msg.Method == "" && json.Unmarshal(msg.ID, &ownID) == nil && strings.HasPrefix(ownID, "shioji-owned:") {
			continue
		}
		if fromAgent && msg.Method == "" && request.method == "process/start" && msg.Error != nil {
			e.mu.Lock()
			if b := e.boots[request.process]; b != nil {
				b.finish(errors.New("owned process did not start"))
			}
			e.mu.Unlock()
		}
		if msg.Method == "process/output" {
			if !e.filterOutput(&msg) {
				continue
			}
		}
		if fromAgent && msg.Method == "" && request.method == "process/read" {
			e.filterRead(&msg, request.process)
		}
		e.forward(msg)
	}
	e.cmd.Process.Kill()
	e.cmd.Wait()
	e.cancel()
	e.admit.Lock()
	if !e.fenced && e.onLost != nil {
		// Persist quarantine before disconnection can produce a terminal event.
		if e.onLost() != nil {
			e.cancel()
		}
	}
	e.admit.Unlock()
	e.mu.Lock()
	for _, b := range e.boots {
		b.finish(errors.New("owned executor stopped"))
	}
	agent := e.agent
	e.mu.Unlock()
	if agent != nil {
		agent.Close()
	}
	close(e.done)
}
func (e *Executor) forward(msg wire.Message) {
	e.mu.Lock()
	agent := e.agent
	e.mu.Unlock()
	if agent == nil {
		return
	}
	b, err := json.Marshal(msg)
	if err == nil {
		websocket.Message.Send(agent, string(b))
	}
}
func (e *Executor) serveAgent(conn *websocket.Conn) {
	conn.MaxPayloadBytes = 16 << 20
	e.mu.Lock()
	if e.agent != nil {
		e.mu.Unlock()
		conn.Close()
		return
	}
	e.agent = conn
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if e.agent == conn {
			e.agent = nil
		}
		e.mu.Unlock()
		conn.Close()
	}()
	for {
		var raw []byte
		if websocket.Message.Receive(conn, &raw) != nil {
			return
		}
		var msg wire.Message
		if json.Unmarshal(raw, &msg) != nil {
			return
		}
		if msg.Method == "initialize" {
			result := wire.Message{ID: msg.ID, Result: e.initialization}
			b, _ := json.Marshal(result)
			if websocket.Message.Send(conn, string(b)) != nil {
				return
			}
			continue
		}
		if msg.Method == "initialized" {
			continue
		}
		process := ""
		if len(msg.Params) > 0 {
			var p struct{ ProcessID string }
			json.Unmarshal(msg.Params, &p)
			process = p.ProcessID
		}
		if msg.Method == "process/write" {
			e.mu.Lock()
			b := e.boots[process]
			e.mu.Unlock()
			if b != nil {
				select {
				case <-b.ready:
					if b.err != nil {
						e.reject(conn, msg.ID, "managed bootstrap did not start")
						continue
					}
				case <-e.ctx.Done():
					return
				}
			}
		}
		e.admit.Lock()
		if e.fenced || !e.scope.Admitting() {
			e.admit.Unlock()
			e.reject(conn, msg.ID, "execution is stopping")
			continue
		}
		if msg.Method == "process/start" {
			if err := e.prepare(&msg); err != nil {
				e.admit.Unlock()
				e.reject(conn, msg.ID, err.Error())
				continue
			}
		}
		e.mu.Lock()
		if len(msg.ID) > 0 && msg.Method != "" {
			e.requests[string(msg.ID)] = executorRequest{method: msg.Method, process: process}
		}
		e.mu.Unlock()
		err := e.send(msg)
		e.admit.Unlock()
		if err != nil {
			return
		}
	}
}
func (e *Executor) reject(conn *websocket.Conn, id json.RawMessage, message string) {
	if len(id) == 0 {
		return
	}
	b, _ := json.Marshal(wire.Message{ID: id, Error: &wire.RPCError{Code: -32603, Message: message}})
	websocket.Message.Send(conn, string(b))
}
func (e *Executor) prepare(msg *wire.Message) error {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return err
	}
	var process string
	json.Unmarshal(params["processId"], &process)
	if process == "" || len(process) > 512 {
		return errors.New("invalid owned process identity")
	}
	e.mu.Lock()
	_, exists := e.boots[process]
	e.mu.Unlock()
	if exists {
		return errors.New("owned process identity already exists")
	}
	// These configurations rewrite target credentials and proxy variables after
	// ingress. Replaying the original environment would discard that protection.
	var managedNetwork bool
	json.Unmarshal(params["enforceManagedNetwork"], &managedNetwork)
	for _, key := range []string{"networkProxy", "managedNetwork"} {
		if raw := params[key]; len(raw) > 0 && string(raw) != "null" {
			managedNetwork = true
		}
	}
	if managedNetwork {
		return errors.New("托管执行暂不支持此受管网络配置；未启动命令")
	}
	if snapshot := params["shellSnapshot"]; len(snapshot) > 0 && string(snapshot) != "null" {
		return errors.New("shell snapshots cannot bypass managed bootstrap")
	}
	var argv []string
	if json.Unmarshal(params["argv"], &argv) != nil || len(argv) == 0 {
		return errors.New("owned execution omitted command")
	}
	env, err := executorTargetEnv(e.baseEnv, params)
	if err != nil {
		return err
	}
	var tty, pipe bool
	json.Unmarshal(params["tty"], &tty)
	json.Unmarshal(params["pipeStdin"], &pipe)
	ticket, err := e.scope.Prepare("executor", process)
	if err != nil {
		return err
	}
	program := argv[0]
	var arg0 *string
	json.Unmarshal(params["arg0"], &arg0)
	if arg0 != nil {
		argv[0] = *arg0
	}
	b := &executorBootstrap{id: ticket.ID, ack: managedexec.Acknowledgement{ID: ticket.ID, Program: program, Argv: argv, Env: env, PipeStdin: pipe || tty}, hidden: map[uint64]bool{}, ready: make(chan struct{})}
	e.mu.Lock()
	e.boots[process] = b
	e.mu.Unlock()
	params["argv"], _ = json.Marshal([]string{e.scope.Executable(), "--managed-exec", ticket.ID})
	params["env"], _ = json.Marshal(map[string]string{"PATH": "/usr/bin:/bin"})
	params["envPolicy"] = json.RawMessage(`null`)
	params["arg0"] = json.RawMessage(`null`)
	params["pipeStdin"] = json.RawMessage(`true`)
	msg.Params, err = json.Marshal(params)
	return err
}
func (e *Executor) filterOutput(msg *wire.Message) bool {
	var output struct {
		ProcessID string `json:"processId"`
		Seq       uint64 `json:"seq"`
		Stream    string `json:"stream"`
		Chunk     []byte `json:"chunk"`
	}
	if json.Unmarshal(msg.Params, &output) != nil {
		return false
	}
	e.mu.Lock()
	b := e.boots[output.ProcessID]
	if b == nil || (output.Stream != "stdout" && output.Stream != "pty") {
		e.mu.Unlock()
		return true
	}
	if b.hidden[output.Seq] {
		e.mu.Unlock()
		output.Chunk = []byte{}
		msg.Params, _ = json.Marshal(output)
		return true
	}
	if b.registered {
		e.mu.Unlock()
		return true
	}
	b.hidden[output.Seq] = true
	b.prefix = append(b.prefix, output.Chunk...)
	if len(b.prefix) > 4096 {
		b.finish(errors.New("invalid managed registration"))
		e.mu.Unlock()
		return false
	}
	end := strings.IndexByte(string(b.prefix), '\n')
	if end < 0 {
		e.mu.Unlock()
		output.Chunk = []byte{}
		msg.Params, _ = json.Marshal(output)
		return true
	}
	packet := append([]byte(nil), b.prefix[:end+1]...)
	b.prefix = nil
	b.registered = true
	e.mu.Unlock()
	go e.acknowledge(output.ProcessID, b, packet)
	output.Chunk = []byte{}
	msg.Params, _ = json.Marshal(output)
	return true
}
func (e *Executor) acknowledge(process string, b *executorBootstrap, packet []byte) {
	e.admit.Lock()
	var registration managedexec.Registration
	err := error(nil)
	if e.fenced || !strings.HasPrefix(string(packet), managedexec.RegistrationPrefix) || json.Unmarshal(packet[len(managedexec.RegistrationPrefix):], &registration) != nil || registration.ID != b.id {
		err = errors.New("managed registration differs from reserved launch")
	}
	if err == nil {
		err = e.scope.Register(registration)
	}
	if err == nil {
		e.mu.Lock()
		ack := b.ack
		e.mu.Unlock()
		payload, _ := json.Marshal(ack)
		payload = append(payload, '\n')
		ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
		_, err = e.call(ctx, "process/write", map[string]any{"processId": process, "writeId": b.id, "chunk": base64.StdEncoding.EncodeToString(payload)})
		cancel()
	}
	e.admit.Unlock()
	e.mu.Lock()
	b.finish(err)
	e.mu.Unlock()
	if err != nil {
		ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
		e.call(ctx, "process/terminate", map[string]string{"processId": process})
		cancel()
	}
}
func (e *Executor) filterRead(msg *wire.Message, process string) {
	if msg.Error != nil {
		return
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(msg.Result, &result) != nil {
		return
	}
	var chunks []json.RawMessage
	if json.Unmarshal(result["chunks"], &chunks) != nil {
		return
	}
	var filtered []json.RawMessage
	for _, chunk := range chunks {
		var row map[string]json.RawMessage
		if json.Unmarshal(chunk, &row) != nil {
			continue
		}
		row["processId"], _ = json.Marshal(process)
		params, _ := json.Marshal(row)
		proxy := wire.Message{Method: "process/output", Params: params}
		if e.filterOutput(&proxy) {
			var clean map[string]json.RawMessage
			json.Unmarshal(proxy.Params, &clean)
			delete(clean, "processId")
			data, _ := json.Marshal(clean)
			filtered = append(filtered, data)
		}
	}
	if filtered == nil {
		filtered = []json.RawMessage{}
	}
	result["chunks"], _ = json.Marshal(filtered)
	msg.Result, _ = json.Marshal(result)
}
func (e *Executor) Fence() error {
	e.admit.Lock()
	defer e.admit.Unlock()
	e.fenced = true
	return e.scope.Fence()
}
func (e *Executor) Close(ctx context.Context) error {
	e.closeOnce.Do(func() {
		e.closeErr = e.Fence()
		e.mu.Lock()
		agent := e.agent
		e.mu.Unlock()
		if agent != nil {
			agent.Close()
		}
		// Admitted filesystem operations finish before the executor is retired.
		deadline := time.Now().Add(3 * time.Second)
		for {
			e.mu.Lock()
			pending := len(e.requests)
			e.mu.Unlock()
			if pending == 0 {
				break
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				e.closeErr = errors.New("owned executor requests did not settle")
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		e.cancel()
		e.stdin.Close()
		if e.server != nil {
			e.server.Close()
		}
		select {
		case <-e.done:
		case <-time.After(3 * time.Second):
			e.cmd.Process.Kill()
			<-e.done
		}
	})
	return e.closeErr
}
