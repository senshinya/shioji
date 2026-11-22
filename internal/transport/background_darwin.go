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
	"shioji/internal/domain"
	"shioji/internal/runtimebuild"
	"strings"
	"syscall"
	"time"
)

const runtimeBundleName = "Shioji Runtime.app"

func bundledRuntime(workerBinary string) string {
	return filepath.Clean(filepath.Join(filepath.Dir(workerBinary), "..", "Resources", runtimeBundleName))
}
func verifyRuntimeBundle(ctx context.Context, path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errors.New("后台宿主必须是普通应用目录")
	}
	if err = exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", path).Run(); err != nil {
		return errors.New("后台宿主签名校验失败")
	}
	id, err := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", filepath.Join(path, "Contents", "Info.plist")).Output()
	if err != nil || strings.TrimSpace(string(id)) != "dev.shioji.runtime" {
		return errors.New("后台宿主身份不匹配")
	}
	return nil
}
func RuntimeService(ctx context.Context, directory, workerBinary string, enable bool) (domain.BackgroundService, error) {
	action := "status"
	if enable {
		action = "register"
	}
	return manageRuntimeService(ctx, directory, workerBinary, action)
}
func DisableRuntimeService(ctx context.Context, directory, workerBinary string) (domain.BackgroundService, error) {
	return manageRuntimeService(ctx, directory, workerBinary, "unregister")
}

// StartRuntimeService wakes the already-approved login agent without replacing
// a running instance. The caller has checked its SMAppService status.
func StartRuntimeService(ctx context.Context) error {
	label := strings.TrimSuffix(background.PlistName, ".plist")
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	if err := exec.CommandContext(ctx, "/bin/launchctl", "kickstart", target).Run(); err != nil {
		return fmt.Errorf("无法启动系统后台执行服务：%w", err)
	}
	return nil
}

func manageRuntimeService(ctx context.Context, directory, workerBinary, action string) (domain.BackgroundService, error) {
	enable := action == "register"
	none := domain.BackgroundService{Format: 1, State: "notInstalled", NotStarted: true}
	home, err := os.UserHomeDir()
	if err != nil {
		return none, err
	}
	expected := filepath.Join(home, "Library", "Application Support", "Shioji", "worker")
	if filepath.Clean(directory) != expected {
		return domain.BackgroundService{Format: 1, State: "unavailable", Message: "隔离开发目录不注册系统后台服务"}, nil
	}
	root := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime")
	target := filepath.Join(root, runtimeBundleName)
	if _, err = os.Lstat(target); os.IsNotExist(err) {
		if !enable {
			return none, nil
		}
		source := bundledRuntime(workerBinary)
		if err = verifyRuntimeBundle(ctx, source); err != nil {
			return none, err
		}
		if err = os.MkdirAll(root, 0700); err != nil {
			return none, err
		}
		st, err := os.Lstat(root)
		if err != nil || !st.IsDir() {
			return none, errors.New("后台宿主目录不安全")
		}
		stage, err := os.MkdirTemp(root, ".runtime-*")
		if err != nil {
			return none, err
		}
		defer os.RemoveAll(stage)
		copied := filepath.Join(stage, runtimeBundleName)
		if err = exec.CommandContext(ctx, "/usr/bin/ditto", source, copied).Run(); err != nil {
			return none, err
		}
		if err = verifyRuntimeBundle(ctx, copied); err != nil {
			return none, err
		}
		if _, e := os.Lstat(target); !os.IsNotExist(e) {
			return none, errors.New("后台宿主已变化，请重新检测")
		}
		if err = os.Rename(copied, target); err != nil {
			return none, err
		}
	} else if err != nil {
		return none, err
	}
	if err = verifyRuntimeBundle(ctx, target); err != nil {
		return none, err
	}
	return runRuntimeHelper(ctx, filepath.Join(target, "Contents", "MacOS", "shioji-runtime"), action)
}
func runRuntimeHelper(ctx context.Context, path, action string) (domain.BackgroundService, error) {
	cmd := exec.CommandContext(ctx, path, "--action", action)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 8192}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 2048}
	if err := cmd.Start(); err != nil {
		return domain.BackgroundService{Format: 1, NotStarted: true}, err
	}
	err := cmd.Wait()
	var result domain.BackgroundService
	if decodeErr := json.Unmarshal(output.Bytes(), &result); decodeErr != nil {
		return result, fmt.Errorf("后台宿主未返回有效状态：%s", strings.TrimSpace(diagnostic.String()))
	}
	if result.Format != 1 {
		return result, errors.New("后台宿主格式不兼容，请先更新 Shioji")
	}
	if err != nil {
		return result, fmt.Errorf("后台服务操作失败：%s", result.Message)
	}
	return result, nil
}
func (s SSH) RuntimeService(ctx context.Context, workerBinary string, enable bool) (domain.BackgroundService, error) {
	action := "status"
	if enable {
		action = "register"
	}
	return s.manageRuntimeService(ctx, workerBinary, action)
}
func (s SSH) DisableRuntimeService(ctx context.Context, workerBinary string) (domain.BackgroundService, error) {
	return s.manageRuntimeService(ctx, workerBinary, "unregister")
}
func (s SSH) manageRuntimeService(ctx context.Context, workerBinary, action string) (domain.BackgroundService, error) {
	if !filepath.IsAbs(s.StateDirectory) || strings.ContainsAny(s.StateDirectory, "\x00\r\n") {
		return domain.BackgroundService{}, errors.New("请重新检查设备执行服务目录")
	}
	remote := RemoteWorker{Directory: s.StateDirectory, Run: func(ctx context.Context, command string, input io.Reader, output, diagnostics io.Writer) (int, error) {
		cmd, err := s.command(ctx, command)
		if err != nil {
			return -1, err
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, diagnostics
		err = cmd.Run()
		return cmd.ProcessState.ExitCode(), err
	}}
	return remote.RuntimeRegistration(ctx, action, func(ctx context.Context) (io.ReadCloser, string, error) {
		return archiveRuntimeBundle(ctx, bundledRuntime(workerBinary))
	})
}

func archiveRuntimeBundle(ctx context.Context, path string) (io.ReadCloser, string, error) {
	if err := verifyRuntimeBundle(ctx, path); err != nil {
		return nil, "", err
	}
	if err := filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type()&os.ModeSymlink != 0 {
			return errors.New("后台宿主含不支持的符号链接")
		}
		return err
	}); err != nil {
		return nil, "", err
	}
	temp, err := os.CreateTemp("", "shioji-runtime-*.tar")
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(temp.Name())
	temp.Close()
	if err = exec.CommandContext(ctx, "/usr/bin/tar", "-cf", temp.Name(), "-C", path, ".").Run(); err != nil {
		return nil, "", err
	}
	digest, err := runtimebuild.Digest(temp.Name())
	if err != nil {
		return nil, "", err
	}
	file, err := os.Open(temp.Name())
	return file, digest, err
}

// Called only after explicit background disable; this is a new idle owner, not a
// replay of user input. Wait for launchd's old owner to finish before returning.
func EnsureDetachedWorker(ctx context.Context, directory, binary, deviceID string, remote *SSH) error {
	for {
		var info WorkerInfo
		var err error
		if remote == nil {
			info, err = EnsureLocalWorker(ctx, directory, binary, ExistingCodex())
		} else {
			cmd, e := remote.command(ctx, "/usr/bin/codesign --verify --strict "+shellQuote(remote.Worker)+" && "+shellQuote(remote.Worker)+" --ensure --state-dir "+shellQuote(remote.StateDirectory))
			if e != nil {
				return e
			}
			data, e := cmd.Output()
			err = e
			if err == nil {
				err = json.Unmarshal(data, &info)
			}
		}
		if err == nil && info.DeviceID != deviceID {
			return errors.New("后台服务关闭后设备身份改变，请重新核对")
		}
		if err == nil && info.Build != nil && !info.Build.Managed {
			return nil
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
