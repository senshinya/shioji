package transport

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// SSHEndpoint is display metadata from OpenSSH's effective configuration. It
// does not replace the saved target/alias or grant access to another endpoint.
type SSHEndpoint struct {
	User string
	Host string
	Port int
}

func (s SSH) Endpoint(ctx context.Context) (SSHEndpoint, error) {
	// Config expansion does not connect. A paused Tailnet must not prevent
	// displaying its configured login, nor require opening a private bridge.
	if s.TailnetPeer != "" && s.Port == 0 {
		s.Port = 22
	}
	s.TailnetPeer, s.ProxySocket = "", ""
	args, err := s.arguments()
	if err != nil {
		return SSHEndpoint{}, err
	}
	args = append([]string{"-G"}, args...)
	args = append(args, "--", s.Target)
	out, err := exec.CommandContext(ctx, "/usr/bin/ssh", args...).Output()
	if err != nil {
		return SSHEndpoint{}, errors.New("无法读取 SSH 连接参数")
	}
	return parseSSHEndpoint(string(out))
}

func parseSSHEndpoint(output string) (SSHEndpoint, error) {
	var endpoint SSHEndpoint
	for _, line := range strings.Split(output, "\n") {
		key, value, _ := strings.Cut(line, " ")
		value = strings.TrimSpace(value)
		switch key {
		case "user":
			endpoint.User = value
		case "hostname":
			endpoint.Host = value
		case "port":
			endpoint.Port, _ = strconv.Atoi(value)
		}
	}
	if endpoint.User == "" || endpoint.Host == "" || endpoint.Port < 1 || endpoint.Port > 65535 {
		return SSHEndpoint{}, errors.New("SSH 连接参数不完整")
	}
	return endpoint, nil
}
