package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/protocol"
	"strings"
	"time"
)

// Bootstrap installs only Shioji's signed developer worker, never Codex or
// credentials. Existing owners and their authoritative database remain in place.
func (s SSH) Bootstrap(ctx context.Context, binary string) (WorkerInfo, error) {
	return s.bootstrap(ctx, binary, s.command)
}

func (s SSH) bootstrap(ctx context.Context, binary string, command func(context.Context, string) (*exec.Cmd, error)) (WorkerInfo, error) {
	if err := s.Validate(); err != nil {
		return WorkerInfo{}, err
	}
	script, err := sshWorkerProbeScript(s.Worker, s.StateDirectory)
	if err != nil {
		return WorkerInfo{}, err
	}
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	cmd, err := command(probe, "/bin/sh -c "+shellQuote(script))
	if err != nil {
		cancel()
		return WorkerInfo{}, err
	}
	var output, diagnostic bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 16 << 10}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 8192}
	err = cmd.Run()
	probeErr := probe.Err()
	cancel()
	if err == nil {
		return decodeBootstrapInfo(output.Bytes())
	}
	if probeErr != nil {
		return WorkerInfo{}, probeErr
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 44 || output.String() != "shioji-worker-missing-v1\n" {
		return WorkerInfo{}, fmt.Errorf("检查已有执行服务失败：%s (%w)", strings.TrimSpace(diagnostic.String()), err)
	}
	// Only an explicit missing-service result may enter deployment. A failed
	// handshake, invalid signature, incompatible protocol or timeout may not.
	deploy, finish := context.WithTimeout(ctx, 2*time.Minute)
	defer finish()
	ctx = deploy
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", binary).Run(); err != nil {
		return WorkerInfo{}, fmt.Errorf("worker 签名校验失败：%w", err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return WorkerInfo{}, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	script = `set -eu
umask 077
[ "$(/usr/bin/uname -s)" = Darwin ] && [ "$(/usr/bin/uname -m)" = arm64 ] || { echo '需要 Apple Silicon macOS' >&2; exit 1; }
root="$HOME/Library/Application Support/Shioji/Runtime/development"
[ ! -L "$root" ] || exit 1
/bin/mkdir -p "$root"
/bin/chmod 700 "$root"
stage=$(/usr/bin/mktemp -d "$root/.stage.XXXXXX")
trap '/bin/rm -rf "$stage"' EXIT
/bin/cat > "$stage/shioji-worker"
actual=$(/usr/bin/shasum -a 256 "$stage/shioji-worker")
[ "${actual%% *}" = DIGEST ] || { echo 'worker 摘要不匹配' >&2; exit 1; }
/bin/chmod 700 "$stage/shioji-worker"
/usr/bin/codesign --verify --strict "$stage/shioji-worker"
dest="$root/DIGEST"
[ ! -L "$dest" ] || exit 1
/bin/mkdir -p "$dest"
[ ! -L "$dest/shioji-worker" ] || exit 1
/bin/ln "$stage/shioji-worker" "$dest/shioji-worker" 2>/dev/null || true
actual=$(/usr/bin/shasum -a 256 "$dest/shioji-worker")
[ "${actual%% *}" = DIGEST ] || { echo '已有 worker 制品不同，请检查设备' >&2; exit 1; }
/usr/bin/codesign --verify --strict "$dest/shioji-worker"
/bin/rm -rf "$stage"
trap - EXIT
exec "$dest/shioji-worker" --ensure STATE_ARGUMENTS
`
	script = strings.ReplaceAll(script, "DIGEST", digest)
	stateArgs := ""
	if s.StateDirectory != "" {
		stateArgs = "--state-dir " + shellQuote(s.StateDirectory)
	}
	script = strings.ReplaceAll(script, "STATE_ARGUMENTS", stateArgs)
	cmd, err = command(ctx, "/bin/sh -c "+shellQuote(script))
	if err != nil {
		return WorkerInfo{}, err
	}
	output.Reset()
	diagnostic.Reset()
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = &boundedWriter{w: &output, remaining: 16 << 10}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 8192}
	if err = cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return WorkerInfo{}, errors.New("部署执行服务超时（2 分钟），请检查连接后重试")
		}
		if ctx.Err() != nil {
			return WorkerInfo{}, ctx.Err()
		}
		return WorkerInfo{}, fmt.Errorf("设备检查／开发 worker 部署失败：%s (%w)", strings.TrimSpace(diagnostic.String()), err)
	}
	return decodeBootstrapInfo(output.Bytes())
}

func decodeBootstrapInfo(data []byte) (WorkerInfo, error) {
	var info WorkerInfo
	var status protocol.Frame
	if json.Unmarshal(data, &status) == nil && status.Type == "worker-legacy" && status.Major == 1 && status.DeviceID != "" {
		return info, &protocol.LegacyWorkerError{Major: status.Major, Minor: status.Minor, DeviceID: status.DeviceID}
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, errors.New("设备未返回有效的 worker 信息；请检查 SSH 启动输出")
	}
	if info.DeviceID == "" || !strings.HasPrefix(info.Worker, "/") || !strings.HasPrefix(info.Directory, "/") {
		return WorkerInfo{}, errors.New("设备 worker 信息不完整")
	}
	return info, nil
}

func sshWorkerProbeScript(worker, directory string) (string, error) {
	for _, path := range []string{worker, directory} {
		if path != "" && (!filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n")) {
			return "", errors.New("设备执行服务路径无效")
		}
	}
	state := `"$HOME/Library/Application Support/Shioji/worker"`
	if directory != "" {
		state = shellQuote(directory)
	}
	return `set -eu
[ "$(/usr/bin/uname -s)" = Darwin ] && [ "$(/usr/bin/uname -m)" = arm64 ] || { echo '需要 Apple Silicon macOS' >&2; exit 1; }
directory=` + state + `
root="$HOME/Library/Application Support/Shioji/Runtime/development"
if [ -e "$directory" ] || [ -L "$directory" ]; then
  [ -d "$directory" ] && [ ! -L "$directory" ]
  [ "$(/usr/bin/stat -f %u "$directory")" = "$(/usr/bin/id -u)" ]
  [ "$(/usr/bin/stat -f %Lp "$directory")" = 700 ]
fi
use_worker() {
  [ -f "$1" ] && [ -x "$1" ]
  /usr/bin/codesign --verify --strict "$1"
  exec "$1" --ensure --state-dir "$directory"
}
# Prefer the actual owner, even if an older App or development artifact remains.
if [ -S "$directory/worker.sock" ]; then
  for pid in $(/usr/sbin/lsof -t "$directory/worker.sock" 2>/dev/null); do
    candidate=$(/bin/ps -p "$pid" -o comm=)
    case "$candidate" in */shioji-worker) use_worker "$candidate";; esac
  done
fi
candidate=` + shellQuote(worker) + `
if [ -e "$candidate" ] || [ -L "$candidate" ]; then use_worker "$candidate"; fi
if [ -e "$directory/active-worker.json" ] || [ -L "$directory/active-worker.json" ]; then
  [ ! -L "$directory/active-worker.json" ]
  build=$(/usr/bin/plutil -extract buildId raw -o - "$directory/active-worker.json")
  [ "${#build}" = 64 ]
  case "$build" in *[!a-f0-9]*) exit 1;; esac
  use_worker "$directory/updates/$build/shioji-worker"
fi
for candidate in /Applications/Shioji.app/Contents/MacOS/shioji-worker "$HOME/Applications/Shioji.app/Contents/MacOS/shioji-worker"; do
  if [ -e "$candidate" ] || [ -L "$candidate" ]; then use_worker "$candidate"; fi
done
# Standalone developer installs have no App or selected update marker. Pick the
# newest signed build by its build metadata, never the digest's lexical order.
newest=''
newest_time=-1
for candidate in "$root"/*/shioji-worker "$directory"/updates/*/shioji-worker; do
  [ -e "$candidate" ] || continue
  /usr/bin/codesign --verify --strict "$candidate"
  metadata=$("$candidate" --describe)
  kind=$(printf '%s' "$metadata" | /usr/bin/plutil -extract type raw -o - -)
  [ "$kind" = worker-build ]
  built=$(printf '%s' "$metadata" | /usr/bin/plutil -extract worker.builtAt raw -o - - 2>/dev/null || printf '0')
  case "$built" in ''|*[!0-9]*) exit 1;; esac
  if [ "$built" -gt "$newest_time" ]; then newest="$candidate"; newest_time="$built"; fi
done
if [ -n "$newest" ]; then use_worker "$newest"; fi
if [ -e "$directory/worker.sock" ] || [ -L "$directory/worker.sock" ]; then
  echo '已有执行服务端点，但无法确认所属程序，请在设备上检查服务' >&2
  exit 1
fi
printf 'shioji-worker-missing-v1\n'
exit 44
`, nil
}
