// Package transport opens authenticated byte streams to device-owned workers.
package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type SSH struct {
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
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ConnectTimeout=8", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "-o", "LogLevel=ERROR"}
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
	net.Conn
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	stderr bytes.Buffer
	once   sync.Once
}

func dialProcess(cmd *exec.Cmd) (net.Conn, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	local, peer := net.Pipe()
	c := &processConn{Conn: local, cmd: cmd, done: make(chan struct{})}
	cmd.Stderr = &boundedWriter{w: &c.stderr, remaining: 8192}
	if err = cmd.Start(); err != nil {
		input.Close()
		output.Close()
		local.Close()
		peer.Close()
		return nil, err
	}
	go func() { io.Copy(input, peer); input.Close() }()
	go func() { io.Copy(peer, output); c.err = cmd.Wait(); close(c.done); peer.Close() }()
	return c, nil
}
func (c *processConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
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
func (c *processConn) Close() error {
	c.once.Do(func() { c.Conn.Close(); c.cmd.Process.Kill() })
	return nil
}

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
