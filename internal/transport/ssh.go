// Package transport opens authenticated byte streams to device-owned workers.
package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type SSH struct {
	TailnetPeer    string `json:"tailnetPeer,omitempty"`
	ProxySocket    string `json:"-"`
	Target         string `json:"target"`
	Port           int    `json:"port,omitempty"`
	Worker         string `json:"worker"`
	StateDirectory string `json:"stateDirectory,omitempty"`
}

var sshTarget = regexp.MustCompile(`^[A-Za-z0-9_.@:%\[\]-]+$`)

func (s SSH) Validate() error {
	if s.Target == "" || len(s.Target) > 255 || strings.HasPrefix(s.Target, "-") || !sshTarget.MatchString(s.Target) {
		return errors.New("SSH 地址应为用户名@主机或 SSH 别名")
	}
	if s.Port < 0 || s.Port > 65535 {
		return errors.New("SSH 端口必须在 1–65535 之间")
	}
	return nil
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func (s SSH) command(ctx context.Context, remote string) (*exec.Cmd, error) {
	args, err := s.arguments()
	if err != nil {
		return nil, err
	}
	args = append(args, "--", s.Target, remote)
	return exec.CommandContext(ctx, "/usr/bin/ssh", args...), nil
}
func (s SSH) arguments() ([]string, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	route, err := s.proxyArguments()
	if err != nil {
		return nil, err
	}
	args := append(route, []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ConnectTimeout=8", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "-o", "LogLevel=ERROR"}...)
	if s.Port != 0 {
		args = append(args, "-p", strconv.Itoa(s.Port))
	}
	return args, nil
}
func (s SSH) Dial(ctx context.Context) (net.Conn, error) {
	if !strings.HasPrefix(s.Worker, "/") || strings.ContainsAny(s.Worker+s.StateDirectory, "\x00\r\n") {
		return nil, errors.New("远端 worker 路径无效，请重新检查设备")
	}
	remote := shellQuote(s.Worker) + " --bridge"
	if s.StateDirectory != "" {
		remote += " --state-dir " + shellQuote(s.StateDirectory)
	}
	cmd, err := s.command(ctx, remote)
	if err != nil {
		return nil, err
	}
	return dialProcess(cmd)
}

type processConn struct {
	input, output *os.File
	cmd           *exec.Cmd
	done          chan struct{}
	err           error
	stderr        bytes.Buffer
	once          sync.Once
}

func dialProcess(cmd *exec.Cmd) (net.Conn, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.WaitDelay = 2 * time.Second
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	input, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	reader, output, err := os.Pipe()
	if err != nil {
		input.Close()
		writer.Close()
		return nil, err
	}
	c := &processConn{input: writer, output: reader, cmd: cmd, done: make(chan struct{})}
	cmd.Stdin = input
	cmd.Stdout = output
	cmd.Stderr = &boundedWriter{w: &c.stderr, remaining: 8192}
	err = cmd.Start()
	input.Close()
	output.Close()
	if err != nil {
		writer.Close()
		reader.Close()
		return nil, err
	}
	go func() { c.err = cmd.Wait(); close(c.done) }()
	return c, nil
}
func (c *processConn) Read(p []byte) (int, error) {
	n, err := c.output.Read(p)
	if err != nil {
		select {
		case <-c.done:
			if c.err != nil {
				err = fmt.Errorf("SSH 连接失败：%s (%w)", strings.TrimSpace(c.stderr.String()), c.err)
			}
		default:
		}
	}
	return n, err
}
func (c *processConn) Write(p []byte) (int, error) { return c.input.Write(p) }
func (c *processConn) CloseWrite() error           { return c.input.Close() }
func (c *processConn) Close() error {
	c.once.Do(func() { c.input.Close(); c.output.Close(); syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL); <-c.done })
	return nil
}
func (c *processConn) LocalAddr() net.Addr  { return &net.UnixAddr{Name: "ssh-stdin", Net: "pipe"} }
func (c *processConn) RemoteAddr() net.Addr { return &net.UnixAddr{Name: "ssh-stdout", Net: "pipe"} }
func (c *processConn) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}
func (c *processConn) SetReadDeadline(t time.Time) error  { return c.output.SetReadDeadline(t) }
func (c *processConn) SetWriteDeadline(t time.Time) error { return c.input.SetWriteDeadline(t) }

type boundedWriter struct {
	w         io.Writer
	remaining int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > w.remaining {
		p = p[:w.remaining]
	}
	written, err := w.w.Write(p)
	w.remaining -= written
	return n, err
}

// Tailnet routes fail closed. Multiplexing and jump hosts must never bypass the
// private proxy, even when present in a user's OpenSSH configuration.
func (s SSH) proxyArguments() ([]string, error) {
	if s.TailnetPeer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(s.ProxySocket, "/") || strings.ContainsAny(s.ProxySocket, "\x00\r\n%") {
		return nil, errors.New("Tailnet 连接尚未准备，请重新连接网络")
	}
	port := s.Port
	if port == 0 {
		port = 22
	}
	return []string{"-o", "Port=" + strconv.Itoa(port), "-o", "ProxyCommand=/usr/bin/nc -U " + shellQuote(s.ProxySocket), "-o", "ProxyJump=none", "-o", "ControlMaster=no", "-o", "ControlPath=none"}, nil
}
