package transport

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type WorkerInfo struct {
	Build     *domain.WorkerBuild `json:"build,omitempty"`
	Home      string              `json:"home"`
	Directory string              `json:"directory"`
	Worker    string              `json:"worker"`
	DeviceID  string              `json:"deviceId"`
	Minor     int                 `json:"minor"`
	Started   bool                `json:"started"`
}

func ExistingCodex() string {
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/opt/homebrew/bin/codex", "/usr/local/bin/codex", filepath.Join(home, ".npm-global/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex"), "/Applications/Codex.app/Contents/Resources/codex"} {
		if s, err := os.Stat(p); err == nil && s.Mode().IsRegular() && s.Mode().Perm()&0111 != 0 {
			return p
		}
	}
	return "codex"
}

// EnsureLocalWorker is the developer bootstrap shared by local launch and SSH.
// It reuses the same authoritative state and never replaces a live worker.
func EnsureLocalWorker(ctx context.Context, directory, binary, codex string) (WorkerInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return WorkerInfo{}, err
	}
	info := WorkerInfo{Home: home, Directory: directory, Worker: binary}
	probe := func() (bool, error) {
		conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "unix", filepath.Join(directory, "worker.sock"))
		if err != nil {
			return false, nil
		}
		defer conn.Close()
		client, err := protocol.Connect(ctx, conn, "")
		if err != nil {
			return true, err
		}
		info.DeviceID, info.Minor = client.DeviceID, client.Minor
		info.Build = client.Worker
		return true, nil
	}
	if exists, err := probe(); exists {
		return info, err
	}
	if binary == "" {
		return info, errors.New("本机执行服务未启动")
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return info, err
	}
	st, err := os.Lstat(directory)
	if err != nil {
		return info, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || !ok || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm() != 0700 {
		return info, errors.New("worker 目录必须属于当前用户，权限为 0700")
	}
	log, err := os.OpenFile(filepath.Join(directory, "worker.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return info, err
	}
	defer log.Close()
	if codex == "" {
		codex = ExistingCodex()
	}
	cmd := exec.Command(binary, "--state-dir", directory, "--codex", codex)
	cmd.Env = append(os.Environ(), "PATH=/opt/homebrew/bin:/usr/local/bin:"+os.Getenv("PATH"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		return info, err
	}
	cmd.Process.Release()
	info.Started = true
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(25 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return info, ctx.Err()
		case <-timeout.C:
			return info, errors.New("worker 未就绪，请检查设备执行服务")
		case <-ticker.C:
			if exists, err := probe(); exists {
				return info, err
			}
		}
	}
}
