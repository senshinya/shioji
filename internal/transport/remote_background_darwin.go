package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

// RuntimeRegistration uses only the execution service's login item. Reading
// status or enabling an existing installation never opens an upload artifact.
func (s RemoteWorker) RuntimeRegistration(ctx context.Context, action string, artifact WorkerArtifact) (domain.BackgroundService, error) {
	if action != "status" && action != "register" && action != "unregister" {
		return domain.BackgroundService{}, errors.New("未知后台服务操作")
	}
	if s.Run == nil {
		return domain.BackgroundService{}, errors.New("远程后台服务路径不可用")
	}
	directory, err := remoteWorkerRoot(s.Directory)
	if err != nil {
		return domain.BackgroundService{}, err
	}
	prefix := `set -eu
umask 077
expected="$HOME/Library/Application Support/Shioji/worker"
[ ` + directory + ` = "$expected" ] || { printf '%s\n' '{"format":1,"state":"unavailable","message":"隔离开发目录不注册系统后台服务"}'; exit 0; }
root="$HOME/Library/Application Support/Shioji/Runtime"
target="$root/Shioji Runtime.app"
[ ! -L "$root" ] && [ ! -L "$target" ]
`
	verify := `/usr/bin/codesign --verify --deep --strict "$target"
[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$target/Contents/Info.plist")" = dev.shioji.runtime ]
exec "$target/Contents/MacOS/shioji-runtime" --action ` + shellQuote(action) + "\n"
	status, err := s.runBackgroundHelper(ctx, prefix+`if [ ! -d "$target" ]; then printf '%s\n' '{"format":1,"state":"notInstalled","notStarted":true}'; exit 0; fi
`+verify, nil)
	if err != nil || action != "register" || status.State != "notInstalled" || !status.NotStarted {
		return status, err
	}
	if artifact == nil {
		return status, errors.New("此客户端缺少签名后台宿主，请更新 Shioji")
	}
	content, digest, err := artifact(ctx)
	if err != nil {
		return status, err
	}
	defer content.Close()
	if _, err = StagedWorkerPath("/", digest); err != nil {
		return status, errors.New("无效的后台宿主摘要")
	}
	install := `/bin/mkdir -p "$root"
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
	return s.runBackgroundHelper(ctx, prefix+install+verify, content)
}

func (s RemoteWorker) runBackgroundHelper(ctx context.Context, script string, content io.Reader) (domain.BackgroundService, error) {
	var output, diagnostic bytes.Buffer
	_, err := s.Run(ctx, "/bin/sh -c "+shellQuote(script), content,
		&boundedWriter{w: &output, remaining: 8192}, &boundedWriter{w: &diagnostic, remaining: 2048})
	var result domain.BackgroundService
	if decode := json.Unmarshal(output.Bytes(), &result); decode != nil {
		if err == nil {
			err = decode
		}
		return result, fmt.Errorf("设备后台服务检查失败：%s (%w)", strings.TrimSpace(diagnostic.String()), err)
	}
	if result.Format != 1 {
		return result, errors.New("设备后台宿主格式不兼容")
	}
	if err != nil {
		return result, fmt.Errorf("设备后台服务操作失败：%s (%w)", result.Message, err)
	}
	return result, nil
}

func (s RemoteWorker) BackgroundStopStatus(ctx context.Context, id, device string) (domain.BackgroundService, error) {
	root, err := remoteWorkerRoot(s.Directory)
	if err != nil {
		return domain.BackgroundService{}, err
	}
	if _, err := sshWorkerProbeScript(s.Worker, s.Directory); err != nil || s.Worker == "" || s.Run == nil {
		return domain.BackgroundService{}, errors.New("远程执行服务路径无效")
	}
	command := "set -eu\nroot=" + root + "\nworker=" + shellQuote(s.Worker) + "\n" + `[ -d "$root" ] && [ ! -L "$root" ]
[ "$( /usr/bin/stat -f %u "$root" )" = "$( /usr/bin/id -u )" ]
[ "$( /usr/bin/stat -f %Lp "$root" )" = 700 ]
/usr/bin/codesign --verify --strict "$worker"
exec "$worker" --state-dir "$root" --service-control background-status --service-id ` + shellQuote(id) + " --service-device " + shellQuote(device)
	var output, diagnostics bytes.Buffer
	_, err = s.Run(ctx, "/bin/sh -c "+shellQuote(command), nil,
		&boundedWriter{w: &output, remaining: 8192}, &boundedWriter{w: &diagnostics, remaining: 2048})
	if err != nil {
		return domain.BackgroundService{}, fmt.Errorf("后台关闭回执暂不可用：%s (%w)", strings.TrimSpace(diagnostics.String()), err)
	}
	var reply struct {
		Background domain.BackgroundService
		Error      string
	}
	if err = json.Unmarshal(output.Bytes(), &reply); err != nil {
		return domain.BackgroundService{}, err
	}
	if reply.Error != "" {
		return reply.Background, errors.New(reply.Error)
	}
	if reply.Background.Format != 1 {
		return domain.BackgroundService{}, errors.New("无效的后台关闭回执")
	}
	return reply.Background, nil
}

func (s RemoteWorker) EnsureDetached(ctx context.Context, device string) error {
	for {
		info, err := BootstrapWorker(ctx, s.Worker, s.Directory, s.Run, nil)
		var legacy *protocol.LegacyWorkerError
		if (err == nil && info.DeviceID != device) || (errors.As(err, &legacy) && legacy.DeviceID != device) {
			return errors.New("后台服务关闭后设备身份改变，请重新核对")
		}
		if err == nil && info.Build != nil && !info.Build.Managed {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), err)
		case <-time.After(150 * time.Millisecond):
		}
	}
}
