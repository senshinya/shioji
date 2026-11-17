package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	enable := action == "register"
	var result domain.BackgroundService
	if !filepath.IsAbs(s.StateDirectory) || strings.ContainsAny(s.StateDirectory, "\x00\r\n") {
		return result, errors.New("请重新检查设备执行服务目录")
	}
	prefix := `set -eu
umask 077
expected="$HOME/Library/Application Support/Shioji/worker"
[ ` + shellQuote(s.StateDirectory) + ` = "$expected" ] || { printf '%s\n' '{"format":1,"state":"unavailable","message":"隔离开发目录不注册系统后台服务"}'; exit 0; }
root="$HOME/Library/Application Support/Shioji/Runtime"
target="$root/Shioji Runtime.app"
[ ! -L "$root" ] && [ ! -L "$target" ]
`
	var archive *os.File
	if enable {
		action = "register"
		source := bundledRuntime(workerBinary)
		if err := verifyRuntimeBundle(ctx, source); err != nil {
			return result, err
		}
		// The archive is made from our signature-verified, regular-files-only bundle.
		if err := filepath.WalkDir(source, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("后台宿主含不支持的符号链接")
			}
			return nil
		}); err != nil {
			return result, err
		}
		temp, err := os.CreateTemp("", "shioji-runtime-*.tar")
		if err != nil {
			return result, err
		}
		defer os.Remove(temp.Name())
		temp.Close()
		if err = exec.CommandContext(ctx, "/usr/bin/tar", "-cf", temp.Name(), "-C", source, ".").Run(); err != nil {
			return result, err
		}
		digest, err := runtimebuild.Digest(temp.Name())
		if err != nil {
			return result, err
		}
		archive, err = os.Open(temp.Name())
		if err != nil {
			return result, err
		}
		defer archive.Close()
		prefix += `/bin/mkdir -p "$root"
stage=$(/usr/bin/mktemp -d "$root/.runtime.XXXXXX")
trap '/bin/rm -rf "$stage"' EXIT
/bin/cat > "$stage/runtime.tar"
actual=$(/usr/bin/shasum -a 256 "$stage/runtime.tar")
[ "${actual%% *}" = ` + shellQuote(digest) + ` ]
if [ ! -e "$target" ]; then
/bin/mkdir "$stage/Shioji Runtime.app"
/usr/bin/tar -xf "$stage/runtime.tar" -C "$stage/Shioji Runtime.app"
/usr/bin/codesign --verify --deep --strict "$stage/Shioji Runtime.app"
[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$stage/Shioji Runtime.app/Contents/Info.plist")" = dev.shioji.runtime ]
/bin/mv -n "$stage/Shioji Runtime.app" "$target"
fi
`
	}
	prefix += `if [ ! -d "$target" ]; then printf '%s\n' '{"format":1,"state":"notInstalled"}'; exit 0; fi
/usr/bin/codesign --verify --deep --strict "$target"
[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$target/Contents/Info.plist")" = dev.shioji.runtime ]
exec "$target/Contents/MacOS/shioji-runtime" --action ` + shellQuote(action) + "\n"
	cmd, err := s.command(ctx, "/bin/sh -c "+shellQuote(prefix))
	if err != nil {
		return result, err
	}
	if archive != nil {
		cmd.Stdin = archive
	}
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 8192}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 2048}
	err = cmd.Run()
	if json.Unmarshal(output.Bytes(), &result) != nil {
		return result, fmt.Errorf("设备后台服务检查失败：%s", strings.TrimSpace(diagnostic.String()))
	}
	if result.Format != 1 {
		return result, errors.New("设备后台宿主格式不兼容")
	}
	if err != nil {
		return result, fmt.Errorf("设备后台服务操作失败：%s", result.Message)
	}
	return result, nil
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
