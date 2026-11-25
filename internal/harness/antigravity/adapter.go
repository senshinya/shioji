// Package antigravity adapts the device's installed Antigravity CLI to Shioji.
package antigravity

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/systemexec"
)

type Adapter struct {
	Executable                               string
	profileID, profileName, profileDirectory string
}

func (a Adapter) Descriptor() domain.HarnessDescriptor {
	if a.profileID != "" {
		return domain.HarnessDescriptor{ID: a.profileID, Name: a.profileName, Icon: "antigravity"}
	}
	return domain.HarnessDescriptor{ID: "antigravity", Name: "Antigravity", Icon: "antigravity"}
}

func (a Adapter) WithExecutable(path string) (harness.Adapter, error) {
	if a.profileID != "" {
		if path != "" {
			return nil, harness.Unsupported("profile-executable")
		}
		return a, nil
	}
	a.Executable = path
	return a, nil
}

func (a Adapter) Installation() domain.RuntimeInstallation {
	ctx, cancel := context.WithTimeout(context.Background(), systemexec.ShellEnvironmentTimeout+time.Second)
	defer cancel()
	env, _ := a.environment(ctx)
	path, _ := a.executable(env)
	if path == "" {
		path = a.Executable
	}
	if path == "" {
		path = "agy"
	}
	if a.profileID != "" {
		installation := domain.RuntimeInstallation{Executable: path, Documentation: "https://antigravity.google/docs/cli"}
		if a.accountAlreadyBound() {
			installation.Guide = "此 Profile 已绑定账号。使用其他账号时请新建 Profile。"
		} else {
			installation.SignInMethods = []domain.SignInMethod{domain.SignInBrowser}
		}
		return installation
	}
	return domain.RuntimeInstallation{
		Executable: path, Override: a.Executable, Configurable: true,
		Guide:         "在该设备的终端安装并登录 Antigravity CLI，然后重新检测",
		Documentation: "https://antigravity.google/docs/cli",
		SignInCommand: []string{path, "acp", "--login"},
		SignInMethods: []domain.SignInMethod{domain.SignInBrowser},
	}
}

func (a Adapter) CatalogVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), systemexec.ShellEnvironmentTimeout+time.Second)
	defer cancel()
	env, _ := a.environment(ctx)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err := a.executable(env)
	if err != nil {
		return "", err
	}
	return executableVersion(path)
}

func (a Adapter) executable(env []string) (string, error) {
	name := a.Executable
	if name == "" {
		name = "agy"
	}
	path, err := systemexec.LookPath(name, env)
	if err == nil {
		return path, nil
	}
	if a.Executable == "" && err != exec.ErrDot {
		candidates := []string{"/opt/homebrew/bin/agy", "/usr/local/bin/agy"}
		if home := systemexec.EnvironmentValue(env, "HOME"); filepath.IsAbs(home) {
			candidates = append(candidates, filepath.Join(home, ".local/bin/agy"))
		}
		for _, candidate := range candidates {
			if path, err := systemexec.LookPath(candidate, env); err == nil {
				return path, nil
			}
		}
	}
	return "", &harness.SetupError{Issue: domain.RuntimeMissing, Message: "未找到 agy，请配置执行设备上的可执行文件或检查该设备的 Shell PATH"}
}

func executableVersion(path string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	stat := info.Sys().(*syscall.Stat_t)
	key := fmt.Sprintf("%s\x00%s\x00%d:%d:%d:%d:%d", path, real, stat.Dev, stat.Ino, info.Size(), info.ModTime().UnixNano(), info.Mode())
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key))), nil
}

// The cache is useful only inside the same device-owned native profile.
func profileKey(env []string) string {
	key := systemexec.EnvironmentValue(env, "HOME") + "\x00" + systemexec.EnvironmentValue(env, "GEMINI_HOME")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
}
