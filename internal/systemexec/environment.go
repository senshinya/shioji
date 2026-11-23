package systemexec

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const shellEnvironmentMarker = "__SHIOJI_LOGIN_ENV__"
const ShellEnvironmentTimeout = 3 * time.Second
const shellEnvironmentTTL = 30 * time.Second

// The owning worker captures its own user's environment, never the GUI's or a
// different device's. Secrets remain in memory; no shell output is logged.
type shellEnvironmentRead struct {
	key           string
	done          chan struct{}
	env           []string
	err           error
	expires       time.Time
	reusableUntil time.Time
}
type shellEnvironmentCache struct {
	mu   sync.Mutex
	read *shellEnvironmentRead
}

var loginEnvironment shellEnvironmentCache

func shellEnvironmentKey(base []string) string {
	var key strings.Builder
	for _, name := range []string{"HOME", "SHELL", "ZDOTDIR", "PATH"} {
		fmt.Fprintf(&key, "%s=%s\x00", name, EnvironmentValue(base, name))
	}
	dir := EnvironmentValue(base, "ZDOTDIR")
	if dir == "" {
		dir = EnvironmentValue(base, "HOME")
	}
	for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".bash_profile", ".bashrc"} {
		if st, e := os.Stat(filepath.Join(dir, name)); e == nil {
			fmt.Fprintf(&key, "%s:%d:%d\x00", name, st.Size(), st.ModTime().UnixNano())
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key.String())))
}

func (c *shellEnvironmentCache) get(ctx context.Context, base []string) ([]string, error) {
	key := shellEnvironmentKey(base)
	c.mu.Lock()
	read := c.read
	if read == nil || read.key != key || !read.expires.IsZero() && time.Now().After(read.expires) {
		previous := read
		read = &shellEnvironmentRead{key: key, done: make(chan struct{})}
		c.read = read
		snapshot := slices.Clone(base)
		go func() {
			probe, cancel := context.WithTimeout(context.Background(), ShellEnvironmentTimeout)
			defer cancel()
			env, err := readLoginEnvironment(probe, snapshot)
			c.mu.Lock()
			read.env, read.err = env, err
			now := time.Now()
			if err == nil {
				read.expires = now.Add(shellEnvironmentTTL)
				read.reusableUntil = read.expires.Add(shellEnvironmentTTL)
			} else {
				read.expires = now.Add(2 * time.Second)
				// A brief shell failure can hide paths known to the same login
				// configuration. Keep that observation briefly and retry soon.
				if previous != nil && previous.key == key && now.Before(previous.reusableUntil) {
					read.env, read.err = previous.env, nil
					read.reusableUntil = previous.reusableUntil
					if read.expires.After(read.reusableUntil) {
						read.expires = read.reusableUntil
					}
				}
			}
			close(read.done)
			c.mu.Unlock()
		}()
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-read.done:
		return slices.Clone(read.env), read.err
	}
}

// A bounded sink still drains stdout, including noisy shell rc files.
type BoundedOutput struct {
	data     []byte
	overflow bool
}

func (b *BoundedOutput) Overflow() bool { return b.overflow }

func (b *BoundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	room := (1 << 20) - len(b.data)
	if room < len(p) {
		b.overflow = true
		p = p[:max(0, room)]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func readLoginEnvironment(ctx context.Context, base []string) ([]string, error) {
	shell := owningUserShell(ctx, base)
	shells := []string{shell}
	for _, candidate := range []string{"/bin/zsh", "/bin/bash"} {
		if !slices.Contains(shells, candidate) {
			shells = append(shells, candidate)
		}
	}
	for _, candidate := range shells {
		script := ""
		if filepath.Base(candidate) == "bash" {
			script = "[ ! -f \"$HOME/.bashrc\" ] || . \"$HOME/.bashrc\" >/dev/null 2>&1; "
		}
		script += "printf '%s' '" + shellEnvironmentMarker + "'; /usr/bin/env -0 2>/dev/null || /usr/bin/env; printf '%s' '" + shellEnvironmentMarker + "'"
		cmd := exec.CommandContext(ctx, candidate, "-ilc", script)
		cmd.Env = append(slices.Clone(base), "DISABLE_AUTO_UPDATE=true", "ZSH_TMUX_AUTOSTART=false", "ZSH_TMUX_AUTOSTARTED=true")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 200 * time.Millisecond
		var output BoundedOutput
		cmd.Stdout = &output
		cmd.Stderr = io.Discard
		err := cmd.Run()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || output.overflow {
			continue
		}
		env := ParseLoginEnvironment(string(output.data))
		if len(env) > 0 {
			return env, nil
		}
	}
	return nil, errors.New("无法读取登录 Shell 环境")
}

func ParseLoginEnvironment(output string) []string {
	parts := strings.Split(output, shellEnvironmentMarker)
	if len(parts) < 3 {
		return nil
	}
	separator := "\x00"
	if !strings.Contains(parts[1], separator) {
		separator = "\n"
	}
	var out []string
	for _, entry := range strings.Split(parts[1], separator) {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		// Preserve worker execution identity and context; rc files cannot inject
		// another session's tool bridge or replace execution-control endpoints.
		if name == "PWD" || name == "OLDPWD" || name == "SHLVL" || name == "_" || strings.HasPrefix(name, "SHIOJI_") || strings.HasPrefix(name, "HERDR_") || name == "DISABLE_AUTO_UPDATE" || strings.HasPrefix(name, "ZSH_TMUX_") {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func MergeEnvironment(base, shell []string) []string {
	values := map[string]string{}
	for _, list := range [][]string{shell, base} {
		for _, entry := range list {
			name, value, ok := strings.Cut(entry, "=")
			if ok {
				values[name] = value
			}
		}
	}
	seen := map[string]bool{}
	var paths []string
	for _, path := range []string{EnvironmentValue(shell, "PATH"), EnvironmentValue(base, "PATH")} {
		for _, entry := range filepath.SplitList(path) {
			if entry != "" && !seen[entry] {
				seen[entry] = true
				paths = append(paths, entry)
			}
		}
	}
	if len(paths) > 0 {
		values["PATH"] = strings.Join(paths, string(os.PathListSeparator))
	}
	out := make([]string, 0, len(values))
	for name, value := range values {
		out = append(out, name+"="+value)
	}
	slices.Sort(out)
	return out
}

// Environment reads the owning user's login PATH and runtime defaults.
func Environment(ctx context.Context, discard func(string) bool) ([]string, error) {
	base := os.Environ()
	shell, err := loginEnvironment.get(ctx, base)
	if discard != nil {
		shell = slices.DeleteFunc(shell, discard)
	}
	return MergeEnvironment(base, shell), err
}

func EnvironmentValue(env []string, name string) string {
	for _, value := range env {
		if strings.HasPrefix(value, name+"=") {
			return strings.TrimPrefix(value, name+"=")
		}
	}
	return ""
}

func LookPath(name string, env []string) (string, error) {
	check := func(path string) (string, error) {
		st, e := os.Stat(path)
		if e != nil {
			return "", e
		}
		if !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
			return "", os.ErrPermission
		}
		return filepath.Abs(path)
	}
	if strings.ContainsRune(name, '/') {
		return check(name)
	}
	for _, dir := range filepath.SplitList(EnvironmentValue(env, "PATH")) {
		if dir == "" {
			dir = "."
		}
		if path, e := check(filepath.Join(dir, name)); e == nil {
			if !filepath.IsAbs(dir) {
				return "", exec.ErrDot
			}
			return path, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func owningUserShell(ctx context.Context, base []string) string {
	if shell := EnvironmentValue(base, "SHELL"); filepath.IsAbs(shell) {
		return shell
	}
	if owner, err := user.Current(); err == nil {
		probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		cmd := exec.CommandContext(probe, "/usr/bin/dscl", ".", "-read", "/Users/"+owner.Username, "UserShell")
		cmd.Env = base
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 200 * time.Millisecond
		cmd.Stderr = io.Discard
		var out BoundedOutput
		cmd.Stdout = &out
		if cmd.Run() == nil {
			fields := strings.Fields(string(out.data))
			if len(fields) == 2 && fields[0] == "UserShell:" && filepath.IsAbs(fields[1]) {
				return fields[1]
			}
		}
	}
	return "/bin/zsh"
}
