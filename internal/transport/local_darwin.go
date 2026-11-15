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
	Migration *domain.WorkerMigrationStatus `json:"migration,omitempty"`
	Legacy    *domain.LegacyWorkerInfo      `json:"legacy,omitempty"`
	Build     *domain.WorkerBuild           `json:"build,omitempty"`
	Home      string                        `json:"home"`
	Directory string                        `json:"directory"`
	Worker    string                        `json:"worker"`
	DeviceID  string                        `json:"deviceId"`
	Minor     int                           `json:"minor"`
	Started   bool                          `json:"started"`
}

// Resolve after the worker has prepared its owning user's launch environment.
// A GUI PATH must not pin a different installation before that discovery.
func ExistingCodex() string { return "codex" }

// EnsureLocalWorker is the developer bootstrap shared by local launch and SSH.
// It reuses the same authoritative state and never replaces a live worker.
func EnsureLocalWorker(ctx context.Context, directory, binary, codex string) (WorkerInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return WorkerInfo{}, err
	}
	info := WorkerInfo{Home: home, Directory: directory, Worker: binary}
	if pending, e := PendingWorkerMigration(directory); e != nil {
		return info, e
	} else if pending != nil {
		info.DeviceID = pending.DeviceID
		info.Migration = pending
		return info, nil
	}
	probe := func() (bool, error) {
		reachable := false
		client, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "unix", filepath.Join(directory, "worker.sock"))
			if err == nil {
				reachable = true
			}
			return conn, err
		}, "")
		if err != nil {
			return reachable, err
		}
		defer client.Close()
		info.DeviceID, info.Minor = client.DeviceID, client.Minor
		info.Build = client.Worker
		return true, nil
	}
	if exists, err := probe(); exists {
		return info, err
	}
	selected, err := ActiveWorkerBuild(directory)
	if err != nil {
		return info, err
	}
	if selected != "" {
		verify, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if _, err = VerifyStagedWorker(verify, directory, selected); err != nil {
			return info, err
		}
		binary, err = StagedWorkerPath(directory, selected)
		if err != nil {
			return info, err
		}
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

// WaitLocalWorker observes an approved launchd service without spawning a second
// detached owner or bypassing a revoked background authorization.
func WaitLocalWorker(ctx context.Context, directory string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "unix", filepath.Join(directory, "worker.sock"))
		if err == nil {
			client, e := protocol.Connect(ctx, conn, "")
			if e == nil {
				client.Close()
				return nil
			}
			conn.Close()
		}
		select {
		case <-ctx.Done():
			return errors.New("后台服务尚未启动，请检查系统后台项目设置")
		case <-ticker.C:
		}
	}
}
