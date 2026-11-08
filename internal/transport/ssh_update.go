package transport

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
)

// StageWorker uploads only an already inspected signed Shioji artifact. It does
// not start a worker or mutate the live database; the authenticated worker owns
// the subsequent update admission.
func (s SSH) StageWorker(ctx context.Context, binary string) (domain.WorkerBuild, error) {
	info, err := InspectWorker(ctx, binary)
	if err != nil {
		return info, err
	}
	script, err := sshWorkerStageScript(s.StateDirectory, info.BuildID)
	if err != nil {
		return info, err
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		return info, err
	}
	cmd, err := s.command(ctx, "/bin/sh -c "+shellQuote(script))
	if err != nil {
		return info, err
	}
	var output, diagnostic bytes.Buffer
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = &boundedWriter{w: &output, remaining: 256}
	cmd.Stderr = &boundedWriter{w: &diagnostic, remaining: 2048}
	if err = cmd.Run(); err != nil {
		return info, fmt.Errorf("上传执行服务失败：%s (%w)", strings.TrimSpace(diagnostic.String()), err)
	}
	if strings.TrimSpace(output.String()) != info.BuildID {
		return info, errors.New("远端执行服务摘要未确认")
	}
	return info, nil
}
func sshWorkerStageScript(directory, digest string) (string, error) {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest {
		return "", errors.New("无效的执行服务摘要")
	}
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, "\x00\r\n") {
		return "", errors.New("设备执行服务目录无效，请重新检查设备")
	}
	return `set -eu
umask 077
root=` + shellQuote(directory) + `
[ -d "$root" ] && [ ! -L "$root" ]
[ "$(/usr/bin/stat -f %u "$root")" = "$(/usr/bin/id -u)" ]
[ "$(/usr/bin/stat -f %Lp "$root")" = 700 ]
updates="$root/updates"
[ ! -L "$updates" ]
/bin/mkdir -p "$updates"
[ "$(/usr/bin/stat -f %Lp "$updates")" = 700 ]
stage=$(/usr/bin/mktemp "$updates/.worker.XXXXXX")
trap '/bin/rm -f "$stage"' EXIT
/bin/cat > "$stage"
actual=$(/usr/bin/shasum -a 256 "$stage")
[ "${actual%% *}" = ` + shellQuote(digest) + ` ]
/bin/chmod 700 "$stage"
/usr/bin/codesign --verify --strict "$stage"
dest="$updates/` + digest + `"
[ ! -L "$dest" ]
/bin/mkdir -p "$dest"
[ "$(/usr/bin/stat -f %Lp "$dest")" = 700 ]
[ ! -L "$dest/shioji-worker" ]
/bin/ln "$stage" "$dest/shioji-worker" 2>/dev/null || true
actual=$(/usr/bin/shasum -a 256 "$dest/shioji-worker")
[ "${actual%% *}" = ` + shellQuote(digest) + ` ]
/usr/bin/codesign --verify --strict "$dest/shioji-worker"
printf '%s\n' ` + shellQuote(digest) + "\n", nil
}
