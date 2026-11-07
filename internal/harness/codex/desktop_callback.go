package codex

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/harness"
)

// Codex desktop host callback protocol. Skills and plugins using this transport
// share the same host capability; there is no per-plugin dispatch.
// This endpoint is not an app-server proxy: it only accepts a message for a
// thread bound to this runtime, then asks the worker to accept it durably.
type desktopBridge struct {
	listener        net.Listener
	directory, path string
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	conns           map[net.Conn]bool
	closed          bool
	slots           chan struct{}
	receive         func(context.Context, string, string, string) error
}

func newDesktopBridge(receive func(context.Context, string, string, string) error) (*desktopBridge, error) {
	// macOS Unix socket paths have a small limit; worker state paths can be long.
	directory, err := os.MkdirTemp("/tmp", "shioji-callback-")
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(directory, "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.RemoveAll(directory)
		return nil, err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		os.RemoveAll(directory)
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &desktopBridge{listener: listener, directory: directory, path: socket, ctx: ctx, cancel: cancel, conns: map[net.Conn]bool{}, slots: make(chan struct{}, 8), receive: receive}
	go b.accept()
	return b, nil
}
func (b *desktopBridge) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.cancel()
	b.listener.Close()
	for conn := range b.conns {
		conn.Close()
	}
	os.RemoveAll(b.directory)
}
func (b *desktopBridge) accept() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		select {
		case b.slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			conn.Close()
			<-b.slots
			return
		}
		b.conns[conn] = true
		b.mu.Unlock()
		go func() {
			defer func() { conn.Close(); b.mu.Lock(); delete(b.conns, conn); b.mu.Unlock(); <-b.slots }()
			if !callbackPeerIsOwner(conn) {
				return
			}
			b.serve(conn)
		}()
	}
}
func callbackPeerIsOwner(conn net.Conn) bool {
	u, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Xucred
	var inner error
	err = raw.Control(func(fd uintptr) { cred, inner = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) })
	return err == nil && inner == nil && cred != nil && cred.Uid == uint32(os.Getuid())
}
func (b *desktopBridge) serve(conn net.Conn) {
	for {
		conn.SetDeadline(time.Now().Add(15 * time.Second))
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		length := binary.LittleEndian.Uint32(header[:])
		if length == 0 || length > 1<<20 {
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(payload, &request) != nil || request.JSONRPC != "2.0" || len(request.ID) == 0 {
			return
		}
		result, err := b.call(request.Method, request.Params)
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		if err != nil {
			response["error"] = map[string]any{"code": -32602, "message": err.Error()}
		} else {
			response["result"] = result
		}
		payload, _ = json.Marshal(response)
		binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))
		if _, err = conn.Write(header[:]); err != nil {
			return
		}
		if _, err = conn.Write(payload); err != nil {
			return
		}
	}
}
func (b *desktopBridge) call(method string, raw json.RawMessage) (any, error) {
	switch method {
	case "tools/list":
		return map[string]any{"tools": []any{map[string]any{"name": "send_message_to_thread", "namespace": "shioji"}}}, nil
	case "tools/call":
		var p struct {
			CallerSource string `json:"callerSource"`
			HostID       string `json:"hostId"`
			Namespace    string `json:"namespace"`
			Tool         string `json:"tool"`
			ThreadID     string `json:"threadId"`
			CallID       string `json:"callId"`
			Arguments    struct {
				ThreadID string `json:"threadId"`
				HostID   string `json:"hostId"`
				Prompt   string `json:"prompt"`
			} `json:"arguments"`
		}
		if json.Unmarshal(raw, &p) != nil || p.CallerSource != "codex" || p.HostID != "local" || p.Namespace != "shioji" || p.Tool != "send_message_to_thread" || p.CallID == "" || len(p.CallID) > 256 || p.ThreadID == "" || len(p.ThreadID) > 256 || p.ThreadID != p.Arguments.ThreadID || p.Arguments.HostID != "local" {
			return nil, errors.New("invalid local thread callback")
		}
		if err := (domain.Submission{Text: p.Arguments.Prompt}).ValidateContent(); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
		defer cancel()
		if err := b.receive(ctx, p.ThreadID, p.CallID, p.Arguments.Prompt); err != nil {
			return nil, err
		}
		receipt, _ := json.Marshal(map[string]string{"threadId": p.ThreadID})
		return map[string]any{"success": true, "contentItems": []any{map[string]string{"type": "inputText", "text": string(receipt)}}}, nil
	default:
		return nil, errors.New("unsupported host callback method")
	}
}

func (r *Runtime) receiveDesktopMessage(ctx context.Context, thread, callID, text string, onMessage func(context.Context, harness.IncomingMessage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.normalizer.mu.Lock()
	session := r.normalizer.sessions[thread]
	r.normalizer.mu.Unlock()
	if session == "" {
		return errors.New("callback thread is not bound to this runtime")
	}
	digest := sha256.Sum256([]byte(thread + "\x00" + callID))
	return onMessage(ctx, harness.IncomingMessage{OperationID: "callback:" + hex.EncodeToString(digest[:]), SessionID: session, SourceRef: thread, Text: text})
}

// Only the owned child process sees these values. Never reuse a parent host's
// callback pipe or expose this socket through SSH, TCP or the app UI.
func desktopEnvironment(env []string, b *desktopBridge, node string) []string {
	out := make([]string, 0, len(env)+3)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "CODEX_APP_TOOLS_PIPE_PATH", "CODEX_APP_TOOLS_CALLER_HOST_ID", "CODEX_MCP_NODE_PATH":
			continue
		}
		out = append(out, entry)
	}
	if b != nil {
		out = append(out, "CODEX_APP_TOOLS_PIPE_PATH="+b.path, "CODEX_APP_TOOLS_CALLER_HOST_ID=local", "CODEX_MCP_NODE_PATH="+node)
	}
	return out
}
