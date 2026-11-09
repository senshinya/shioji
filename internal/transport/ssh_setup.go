package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type SSHHostKey struct {
	Target       string   `json:"target"`
	Port         int      `json:"port"`
	Name         string   `json:"name"`
	File         string   `json:"file"`
	Files        []string `json:"files"`
	Entries      string   `json:"entries"`
	Fingerprints string   `json:"fingerprints"`
}
type sshHostConfig struct {
	Name         string
	User, Global []string
}

var hostTrustMu sync.Mutex

func parseSSHHostConfig(output string) (sshHostConfig, error) {
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		k, v, ok := strings.Cut(line, " ")
		if ok {
			values[k] = strings.TrimSpace(v)
		}
	}
	paths := func(value string) ([]string, error) {
		var result []string
		if value == "none" {
			return nil, nil
		}
		for _, p := range strings.Fields(value) {
			if !filepath.IsAbs(p) || strings.ContainsAny(p, "\"'\x00\r\n") {
				return nil, errors.New("SSH 主机记录路径无法明确解析，请检查 SSH 配置")
			}
			result = append(result, p)
		}
		return result, nil
	}
	user, err := paths(values["userknownhostsfile"])
	if err != nil || len(user) == 0 {
		if err == nil {
			err = errors.New("SSH 未配置可保存的主机记录文件")
		}
		return sshHostConfig{}, err
	}
	global, err := paths(values["globalknownhostsfile"])
	if err != nil {
		return sshHostConfig{}, err
	}
	name := values["hostkeyalias"]
	if name == "" || name == "none" {
		name = strings.ToLower(values["hostname"])
	}
	port, err := strconv.Atoi(values["port"])
	if err != nil || port < 1 || port > 65535 || name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\r\n\t ") {
		return sshHostConfig{}, errors.New("SSH 主机配置无效")
	}
	if port != 22 {
		name = fmt.Sprintf("[%s]:%d", name, port)
	}
	return sshHostConfig{Name: name, User: user, Global: global}, nil
}
func (s SSH) hostConfig(ctx context.Context) (sshHostConfig, error) {
	args, err := s.arguments()
	if err != nil {
		return sshHostConfig{}, err
	}
	args = append([]string{"-G"}, args...)
	args = append(args, "--", s.Target)
	out, err := exec.CommandContext(ctx, "/usr/bin/ssh", args...).Output()
	if err != nil {
		return sshHostConfig{}, errors.New("无法读取 SSH 主机配置")
	}
	return parseSSHHostConfig(string(out))
}
func hostListed(ctx context.Context, name string, files []string) (bool, error) {
	for _, file := range files {
		st, err := os.Stat(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !st.Mode().IsRegular() {
			return false, errors.New("SSH 主机记录不是普通文件")
		}
		err = exec.CommandContext(ctx, "/usr/bin/ssh-keygen", "-F", name, "-f", file).Run()
		if err == nil {
			return true, nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return false, errors.New("无法核对 SSH 主机记录")
		}
	}
	return false, nil
}
func (s SSH) InspectHostKey(ctx context.Context) (*SSHHostKey, error) {
	cfg, err := s.hostConfig(ctx)
	if err != nil {
		return nil, err
	}
	return s.inspectHostKey(ctx, cfg)
}
func (s SSH) inspectHostKey(ctx context.Context, cfg sshHostConfig) (*SSHHostKey, error) {
	root, err := os.MkdirTemp("", "shioji-host-key-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	capture := filepath.Join(root, "known_hosts")
	if err = os.WriteFile(capture, nil, 0600); err != nil {
		return nil, err
	}
	files := append(append([]string{}, cfg.User...), cfg.Global...)
	user := append([]string{capture}, cfg.User...)
	quoted := make([]string, len(user))
	for i, p := range user {
		quoted[i] = "\"" + p + "\""
	}
	args := []string{}
	for _, option := range []string{"UserKnownHostsFile=" + strings.Join(quoted, " "), "StrictHostKeyChecking=accept-new", "UpdateHostKeys=no", "PubkeyAuthentication=no", "PasswordAuthentication=no", "KbdInteractiveAuthentication=no", "GSSAPIAuthentication=no", "HostbasedAuthentication=no", "ControlPath=none", "ForwardX11=no", "PermitLocalCommand=no", "RemoteCommand=none", "Tunnel=no"} {
		args = append(args, "-o", option)
	}
	base, err := s.arguments()
	if err != nil {
		return nil, err
	}
	args = append(args, base...)
	args = append(args, "--", s.Target, "/usr/bin/true")
	cmd := exec.CommandContext(ctx, "/usr/bin/ssh", args...)
	var diagnostic bytes.Buffer
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 4096}
	runErr := cmd.Run()
	entries, err := os.ReadFile(capture)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		exists, err := hostListed(ctx, cfg.Name, files)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.New("SSH 主机密钥发生变化，请核实设备身份后修正主机记录")
		}
		fingerprints, err := exec.CommandContext(ctx, "/usr/bin/ssh-keygen", "-l", "-f", capture, "-E", "sha256").Output()
		if err != nil {
			return nil, errors.New("无法读取 SSH 主机指纹")
		}
		return &SSHHostKey{Target: s.Target, Port: s.Port, Name: cfg.Name, File: cfg.User[0], Files: files, Entries: string(entries), Fingerprints: strings.TrimSpace(string(fingerprints))}, nil
	}
	if runErr != nil {
		message := diagnostic.String()
		if strings.Contains(message, "Permission denied (") || strings.Contains(message, "Authentication failed") {
			return nil, nil
		}
		return nil, fmt.Errorf("无法验证 SSH 主机：%s", strings.TrimSpace(message))
	}
	return nil, nil
}

// Trust appends the exact entries shown for review, never rescans to trust an
// unseen replacement. Recheck every configured file before modifying any one.
func (k SSHHostKey) Trust(ctx context.Context) error {
	hostTrustMu.Lock()
	defer hostTrustMu.Unlock()
	if len(k.Files) == 0 || k.Files[0] != k.File || !filepath.IsAbs(k.File) || k.Name == "" || len(k.Entries) > 16384 || k.Entries == "" {
		return errors.New("主机指纹确认已失效，请重新检查")
	}
	root, err := os.MkdirTemp("", "shioji-reviewed-key-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	reviewed := filepath.Join(root, "known_hosts")
	if err = os.WriteFile(reviewed, []byte(k.Entries), 0600); err != nil {
		return err
	}
	match, err := hostListed(ctx, k.Name, []string{reviewed})
	if err != nil || !match {
		return errors.New("主机记录与已确认的指纹不一致")
	}
	exists, err := hostListed(ctx, k.Name, k.Files)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("主机记录已改变，请重新核对指纹")
	}
	if err = os.MkdirAll(filepath.Dir(k.File), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(k.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	previous, err := os.ReadFile(k.File)
	if err != nil {
		return err
	}
	if len(previous) > 0 && previous[len(previous)-1] != '\n' {
		prefix = "\n"
	}
	if _, err = f.WriteString(prefix + k.Entries); err != nil {
		return err
	}
	return f.Sync()
}

type SSHHostVerificationRequired struct{}

func (*SSHHostVerificationRequired) Error() string { return "需要核对 SSH 主机指纹" }

type SSHAuthenticationRequired struct{}

func (*SSHAuthenticationRequired) Error() string { return "需要设置 SSH 密钥" }
func (s SSH) CheckAuthentication(ctx context.Context) error {
	cmd, err := s.command(ctx, "/usr/bin/true")
	if err != nil {
		return err
	}
	var diagnostic bytes.Buffer
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 4096}
	if err = cmd.Run(); err == nil {
		return nil
	}
	text := diagnostic.String()
	if strings.Contains(text, "Host key verification failed") || strings.Contains(text, "REMOTE HOST IDENTIFICATION HAS CHANGED") {
		return &SSHHostVerificationRequired{}
	}
	if strings.Contains(text, "Permission denied (") || strings.Contains(text, "Authentication failed") {
		return &SSHAuthenticationRequired{}
	}
	return fmt.Errorf("SSH 连接失败：%s", strings.TrimSpace(text))
}
func (s SSH) KeySetupCommand() ([]string, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	args := []string{"/usr/bin/ssh-copy-id", "-o", "StrictHostKeyChecking=yes", "-o", "BatchMode=no", "-o", "ForwardAgent=no", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "RemoteCommand=none"}
	if s.Port != 0 {
		args = append(args, "-p", strconv.Itoa(s.Port))
	}
	return append(args, s.Target), nil
}
