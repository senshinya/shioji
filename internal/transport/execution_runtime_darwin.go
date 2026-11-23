package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/background"
	"shioji/internal/runtimebuild"
	"shioji/internal/supervisor"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Execution hosts are immutable signed bundles, separate from the registered
// login host and the network host. Preparing one changes neither registration
// nor a running process. All three preserve dev.shioji.runtime's signed identity.
func PrepareExecutionRuntime(ctx context.Context, directory, workerBinary string) error {
	remote := RemoteWorker{Directory: directory, Run: func(ctx context.Context, command string, input io.Reader, output, diagnostics io.Writer) (int, error) {
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, diagnostics
		err := cmd.Run()
		if cmd.ProcessState == nil {
			return -1, err
		}
		return cmd.ProcessState.ExitCode(), err
	}}
	return remote.PrepareExecutionRuntime(ctx, func(ctx context.Context) (io.ReadCloser, string, error) {
		return archiveRuntimeBundle(ctx, bundledRuntime(workerBinary))
	})
}

func executionRuntime(ctx context.Context, directory string) (string, error) {
	var selected struct{ Bundle string }
	raw, err := os.ReadFile(filepath.Join(directory, "execution-host.json"))
	if err != nil || json.Unmarshal(raw, &selected) != nil {
		return "", errors.New("缺少执行 Runtime，请先从新版 Shioji 准备设备执行服务")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", "Execution")
	digest := filepath.Base(filepath.Dir(selected.Bundle))
	if _, err := StagedWorkerPath("/", digest); err != nil || selected.Bundle != filepath.Join(root, digest, runtimeBundleName) {
		return "", errors.New("执行 Runtime 路径不匹配")
	}
	if err := verifyRuntimeBundle(ctx, selected.Bundle); err != nil {
		return "", err
	}
	output, err := exec.CommandContext(ctx, filepath.Join(selected.Bundle, "Contents", "MacOS", "shioji-runtime"), "--execution-describe").Output()
	var description struct {
		Version int
		Role    string
	}
	if err != nil || json.Unmarshal(output, &description) != nil || description.Version != 1 || description.Role != "execution" {
		return "", errors.New("执行 Runtime 不支持升级接管，请更新 Shioji")
	}
	return selected.Bundle, nil
}

// CheckInstallationRuntime runs before source drain, not after stopping it.
func CheckInstallationRuntime(ctx context.Context, directory string) error {
	if _, err := prepareExecutionRuntimeHost(ctx, directory); err != nil {
		return err
	}
	_, err := executionRuntimeManaged(ctx, directory)
	return err
}

// Replace only the signed bundle on disk, without unregistering its login item
// or stopping any execution/network process. launchd's next invocation uses the
// new entry point at the registered path and keeps the existing identity.
func prepareExecutionRuntimeHost(ctx context.Context, directory string) (string, error) {
	source, err := executionRuntime(ctx, directory)
	if err != nil {
		return "", err
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(source)))
	target := filepath.Join(root, runtimeBundleName)
	fd, err := unix.Open(filepath.Join(root, "execution-install.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return "", err
	}
	lock := os.NewFile(uintptr(fd), "execution Runtime installation")
	defer lock.Close()
	for {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			break
		} else if !errors.Is(err, unix.EWOULDBLOCK) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	existing, err := os.Lstat(target)
	if err == nil {
		if !existing.IsDir() {
			return "", errors.New("后台宿主路径不是普通应用目录")
		}
		if err := verifyRuntimeBundle(ctx, target); err != nil {
			return "", err
		}
		before, err := runtimebuild.Digest(filepath.Join(target, "Contents", "MacOS", "shioji-runtime"))
		if err != nil {
			return "", err
		}
		after, err := runtimebuild.Digest(filepath.Join(source, "Contents", "MacOS", "shioji-runtime"))
		if err != nil {
			return "", err
		}
		if before == after {
			return target, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	stage, err := os.MkdirTemp(root, ".execution-replace-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	copied := filepath.Join(stage, runtimeBundleName)
	if err := exec.CommandContext(ctx, "/usr/bin/ditto", source, copied).Run(); err != nil {
		return "", err
	}
	if err := verifyRuntimeBundle(ctx, copied); err != nil {
		return "", err
	}
	if existing == nil {
		err = os.Rename(copied, target)
	} else {
		err = unix.RenamexNp(copied, target, unix.RENAME_SWAP)
	}
	if err != nil {
		return "", err
	}
	return target, nil
}

func executionRuntimeManaged(ctx context.Context, directory string) (bool, error) {
	state, err := RuntimeService(ctx, directory, "", false)
	if err != nil {
		return false, err
	}
	switch state.State {
	case "enabled":
		return true, nil
	case "notInstalled", "notRegistered":
		return false, nil
	case "unavailable":
		home, err := os.UserHomeDir()
		if err == nil && directory != filepath.Join(home, "Library", "Application Support", "Shioji", "worker") {
			return false, nil // isolated development state has no login registration
		}
		return false, errors.New("执行设备的后台项目状态不可用，请先检查系统设置")
	default:
		return false, errors.New("请先在执行设备允许 Shioji 后台项目，再继续更新")
	}
}

func StartExecutionRuntime(ctx context.Context, directory string) error {
	return startExecutionRuntime(ctx, directory, false)
}

func startExecutionRuntime(ctx context.Context, directory string, replaceIdleHost bool) error {
	bundle, err := prepareExecutionRuntimeHost(ctx, directory)
	if err != nil {
		return err
	}
	// LaunchServices gives this process Runtime's responsibility identity. A
	// direct exec (including setsid/nohup) would retain the SSH initiator's TCC.
	managed, err := executionRuntimeManaged(ctx, directory)
	if err != nil {
		return err
	}
	if managed {
		if replaceIdleHost {
			// The installer still owns supervisor.lock and has confirmed the
			// source writer/fixed host exited. Replace an old registered host
			// that may only be waiting on that lease; never use -k on reconnect.
			label := strings.TrimSuffix(background.PlistName, ".plist")
			target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
			if err := exec.CommandContext(ctx, "/bin/launchctl", "kickstart", "-k", target).Run(); err != nil {
				return fmt.Errorf("无法接续执行 Runtime：%w", err)
			}
			return nil
		}
		return StartRuntimeService(ctx)
	}
	args := []string{"-n", "-g", "-a", bundle, "--args", "--serve", "--state-dir", directory}
	args = append(args, "--on-demand")
	if err := exec.CommandContext(ctx, "/usr/bin/open", args...).Run(); err != nil {
		return fmt.Errorf("无法启动执行 Runtime：%w", err)
	}
	return nil
}

func HandoffWorkerInstallation(ctx context.Context, directory string, install supervisor.LegacyInstall) error {
	if err := supervisor.InstallationHandoff(directory, install); err != nil {
		return err
	}
	// Start before releasing control: Runtime waits for the unchanged lease,
	// while a failed native launch leaves the installer holding it for recovery.
	launch, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := startExecutionRuntime(launch, directory, true); err != nil {
		return err
	}
	return install.Lease.Close()
}
