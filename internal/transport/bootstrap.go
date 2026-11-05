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
	"strings"
)

// Bootstrap installs only Shioji's signed developer worker, never Codex or
// credentials. Existing owners and their authoritative database remain in place.
func (s SSH) Bootstrap(ctx context.Context, binary string) (WorkerInfo, error) {
	if err := s.Validate(); err != nil {
		return WorkerInfo{}, err
	}
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", binary).Run(); err != nil {
		return WorkerInfo{}, fmt.Errorf("worker 签名校验失败：%w", err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return WorkerInfo{}, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	script := `set -eu
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
exec "$dest/shioji-worker" --ensure
`
	script = strings.ReplaceAll(script, "DIGEST", digest)
	cmd, err := s.command(ctx, "/bin/sh -c "+shellQuote(script))
	if err != nil {
		return WorkerInfo{}, err
	}
	var output, diagnostic bytes.Buffer
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = &boundedWriter{w: &output, remaining: 16 << 10}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 8192}
	if err = cmd.Run(); err != nil {
		return WorkerInfo{}, fmt.Errorf("设备检查／开发 worker 部署失败：%s (%w)", strings.TrimSpace(diagnostic.String()), err)
	}
	var info WorkerInfo
	if err = json.Unmarshal(output.Bytes(), &info); err != nil {
		return info, errors.New("设备未返回有效的 worker 信息；请检查 SSH 启动输出")
	}
	if info.DeviceID == "" || !strings.HasPrefix(info.Worker, "/") || !strings.HasPrefix(info.Directory, "/") {
		return WorkerInfo{}, errors.New("设备 worker 信息不完整")
	}
	return info, nil
}
