package transport

import (
	"bytes"
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
func CheckInstallationRuntime(ctx context.Context, directory, registrationBuild string) error {
	if _, err := prepareExecutionRuntimeHost(ctx, directory, registrationBuild); err != nil {
		return err
	}
	_, err := executionRuntimeManaged(ctx, directory)
	return err
}

func executionRuntimePlist(bundle string) string {
	return filepath.Join(bundle, "Contents", "Library", "LaunchAgents", background.PlistName)
}

func verifyInteractiveExecutionRuntime(ctx context.Context, bundle, build string) error {
	actual, err := runtimebuild.Digest(filepath.Join(bundle, "Contents", "MacOS", "shioji-worker"))
	if err != nil || actual != build {
		return errors.New("签名执行 Runtime 与接管候选构建不一致")
	}
	raw, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", executionRuntimePlist(bundle)).Output()
	var config struct{ Label, ProcessType string }
	if err != nil || json.Unmarshal(raw, &config) != nil || config.Label != strings.TrimSuffix(background.PlistName, ".plist") || config.ProcessType != "Interactive" {
		return errors.New("签名执行 Runtime 的服务标识或调度配置无效")
	}
	return nil
}

// Replace only the signed bundle on disk, without unregistering its login item
// or stopping any execution/network process. launchd's next invocation uses the
// new entry point at the registered path and keeps the existing identity.
func prepareExecutionRuntimeHost(ctx context.Context, directory, registrationBuild string) (string, error) {
	source, err := executionRuntime(ctx, directory)
	if err != nil {
		return "", err
	}
	if registrationBuild != "" {
		if err := verifyInteractiveExecutionRuntime(ctx, source, registrationBuild); err != nil {
			return "", err
		}
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
		oldPlist, oldErr := os.ReadFile(executionRuntimePlist(target))
		newPlist, newErr := os.ReadFile(executionRuntimePlist(source))
		if before == after && oldErr == nil && newErr == nil && bytes.Equal(oldPlist, newPlist) {
			if registrationBuild == "" || verifyInteractiveExecutionRuntime(ctx, target, registrationBuild) == nil {
				return target, nil
			}
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
		expected, err := runtimeServiceDirectory()
		if err == nil && filepath.Clean(directory) != expected {
			return false, nil // isolated development state has no login registration
		}
		return false, errors.New("执行设备的后台项目状态不可用，请先检查系统设置")
	default:
		return false, errors.New("请先在执行设备允许 Shioji 后台项目，再继续更新")
	}
}

func StartExecutionRuntime(ctx context.Context, directory string) error {
	op, pending, err := supervisor.PendingInstallation(directory)
	if err != nil {
		return err
	}
	if !pending || op.ExecutionRegistration == "" {
		return startExecutionRuntime(ctx, directory, false, nil)
	}
	lease, err := supervisor.AcquireLease(directory)
	if errors.Is(err, supervisor.ErrOwned) {
		return nil // The installer or the newly registered Runtime owns the handoff.
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	latest, pending, err := supervisor.PendingInstallation(directory)
	if err != nil || !pending || latest.Input != op.Input {
		return err
	}
	return startExecutionRuntime(ctx, directory, true, &latest)
}

func startExecutionRuntime(ctx context.Context, directory string, replaceIdleHost bool, registration *supervisor.Operation) error {
	registrationBuild := ""
	if registration != nil {
		expected, err := runtimeServiceDirectory()
		if err != nil {
			return err
		}
		if filepath.Clean(directory) != expected {
			return errors.New("隔离开发目录不能刷新系统执行服务注册")
		}
		registrationBuild = registration.Input.BuildID
	}
	bundle, err := prepareExecutionRuntimeHost(ctx, directory, registrationBuild)
	if err != nil {
		return err
	}
	if registration != nil {
		if err = completeExecutionRegistration(ctx, directory, bundle, registration); err != nil {
			registration.Error = err.Error()
			return errors.Join(err, supervisor.SaveLegacy(directory, *registration))
		}
		return StartRuntimeService(ctx)
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

// The caller holds the handoff lease and has verified the signed Runtime.
// Each phase is recorded before the next SMAppService action can run.
func completeExecutionRegistration(ctx context.Context, directory, bundle string, op *supervisor.Operation) error {
	binary := filepath.Join(bundle, "Contents", "MacOS", "shioji-runtime")
	state, err := runRuntimeHelper(ctx, binary, "status")
	if err != nil {
		return err
	}
	if state.State == "requiresApproval" {
		return errors.New("请在系统设置的登录项与扩展中允许 Shioji 执行服务")
	}
	if op.ExecutionRegistration == supervisor.RefreshExecutionRegistration {
		if state.State == "enabled" {
			state, err = runRuntimeHelper(ctx, binary, "unregister")
			if err != nil {
				return err
			}
		}
		if state.State != "notRegistered" && state.State != "notFound" {
			return errors.New("旧执行服务注册尚未确认移除")
		}
		op.ExecutionRegistration = supervisor.RestoreExecutionRegistration
		if err = supervisor.SaveLegacy(directory, *op); err != nil {
			return err
		}
	}
	if op.ExecutionRegistration == supervisor.RestoreExecutionRegistration {
		if state.State == "notRegistered" || state.State == "notFound" {
			state, err = runRuntimeHelper(ctx, binary, "register")
			if err != nil {
				return err
			}
		}
		if state.State == "requiresApproval" {
			return errors.New("请在系统设置的登录项与扩展中允许 Shioji 执行服务")
		}
		if state.State != "enabled" {
			return errors.New("执行服务注册尚未确认启用")
		}
		op.ExecutionRegistration = supervisor.ExecutionRegistrationEnabled
		op.Error = ""
		return supervisor.SaveLegacy(directory, *op)
	}
	if op.ExecutionRegistration != supervisor.ExecutionRegistrationEnabled || state.State != "enabled" {
		return errors.New("执行服务注册状态已变化")
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
	var registration *supervisor.Operation
	if install.Operation.ExecutionRegistration != "" {
		op, pending, err := supervisor.PendingInstallation(directory)
		if err != nil {
			return err
		}
		if !pending || op.Input != install.Operation.Input {
			return errors.New("执行 Runtime 接管记录已变化")
		}
		registration = &op
	}
	if err := startExecutionRuntime(launch, directory, true, registration); err != nil {
		return err
	}
	return install.Lease.Close()
}
