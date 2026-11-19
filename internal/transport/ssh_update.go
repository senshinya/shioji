package transport

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"shioji/internal/domain"
)

type WorkerUploadPhase string

const (
	WorkerUploadSending      WorkerUploadPhase = "uploading"
	WorkerUploadVerifying    WorkerUploadPhase = "verifying-upload"
	WorkerUploadReconnecting WorkerUploadPhase = "reconnecting-upload"
)

// StageWorker uploads an inspected artifact without starting a worker or
// modifying its database. A later call can reuse the received bytes.
func (s SSH) StageWorker(ctx context.Context, binary string) (domain.WorkerBuild, error) {
	return stageRemoteWorker(ctx, binary, func() (SSH, error) { return s, nil }, nil, false)
}

// StageRemoteWorker resumes a user-requested upload across transient failures
// within one minute. resolve must keep the same authenticated device and state
// directory, and refresh its route for each connection.
func StageRemoteWorker(ctx context.Context, binary string, resolve func() (SSH, error), report func(WorkerUploadPhase)) (domain.WorkerBuild, error) {
	call, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	info, err := stageRemoteWorker(call, binary, resolve, report, true)
	if errors.Is(call.Err(), context.DeadlineExceeded) {
		return info, fmt.Errorf("上传执行服务超过 1 分钟，可重试续传：%w", call.Err())
	}
	return info, err
}

func stageRemoteWorker(ctx context.Context, binary string, resolve func() (SSH, error), report func(WorkerUploadPhase), retry bool) (domain.WorkerBuild, error) {
	info, err := InspectWorker(ctx, binary)
	if err != nil {
		return info, err
	}
	file, err := os.Open(binary)
	if err != nil {
		return info, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return info, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return info, err
		}
		ssh, err := resolve()
		if err == nil {
			err = ssh.uploadWorker(ctx, info.BuildID, file, stat.Size(), report)
			if err == nil {
				return info, nil
			}
			var temporary *interruptedWorkerUpload
			if !errors.As(err, &temporary) {
				return info, err
			}
		}
		if !retry {
			return info, err
		}
		if report != nil {
			report(WorkerUploadReconnecting)
		}
		select {
		case <-ctx.Done():
			return info, errors.Join(ctx.Err(), err)
		case <-time.After(time.Second):
		}
	}
}

type interruptedWorkerUpload struct{ error }

func (s SSH) uploadWorker(ctx context.Context, digest string, file io.ReaderAt, size int64, report func(WorkerUploadPhase)) error {
	script, err := sshWorkerStageScript(s.StateDirectory, digest)
	if err != nil {
		return err
	}
	cmd, err := s.command(ctx, "/bin/sh -c "+shellQuote(script)+" stage "+strconv.FormatInt(size, 10))
	if err != nil {
		return err
	}
	return transferWorker(ctx, cmd, digest, file, size, report)
}

func transferWorker(ctx context.Context, cmd *exec.Cmd, digest string, file io.ReaderAt, size int64, report func(WorkerUploadPhase)) error {
	conn, err := dialProcess(cmd)
	if err != nil {
		return err
	}
	defer conn.Close()
	reply := bufio.NewReaderSize(conn, 256)
	line, err := reply.ReadSlice('\n')
	if err != nil {
		return workerUploadResult(ctx, conn, err)
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(line)), 10, 64)
	if err != nil || offset < 0 || offset > size {
		return errors.New("远端执行服务续传位置无效")
	}
	if offset < size {
		if report != nil {
			report(WorkerUploadSending)
		}
		written, err := io.Copy(conn, io.NewSectionReader(file, offset, size-offset))
		if err != nil {
			_ = conn.CloseWrite()
			return workerUploadResult(ctx, conn, err)
		}
		if written != size-offset {
			return errors.New("本地候选执行服务在上传期间改变，请重新检查更新")
		}
	}
	_ = conn.CloseWrite()
	if report != nil {
		report(WorkerUploadVerifying)
	}
	line, err = reply.ReadSlice('\n')
	if err != nil {
		return workerUploadResult(ctx, conn, err)
	}
	if strings.TrimSpace(string(line)) != digest {
		return errors.New("远端执行服务摘要未确认")
	}
	return workerUploadResult(ctx, conn, nil)
}

func workerUploadResult(ctx context.Context, conn *processConn, readErr error) error {
	if errors.Is(readErr, bufio.ErrBufferFull) {
		return errors.New("远端执行服务上传响应无效")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-conn.done:
		if conn.err == nil {
			if readErr != nil {
				return fmt.Errorf("远端执行服务上传响应未完成：%w", readErr)
			}
			return nil
		}
		diagnostic := strings.TrimSpace(conn.stderr.String())
		err := fmt.Errorf("上传执行服务失败：%s (%w)", diagnostic, conn.err)
		var exit *exec.ExitError
		if errors.As(conn.err, &exit) {
			switch exit.ExitCode() {
			case 75, 76:
				return &interruptedWorkerUpload{err}
			case 255:
				if !strings.Contains(diagnostic, "Host key verification failed") && !strings.Contains(diagnostic, "Permission denied (") {
					return &interruptedWorkerUpload{err}
				}
			}
		}
		return err
	}
}

func sshWorkerStageScript(directory, digest string) (string, error) {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest {
		return "", errors.New("无效的执行服务摘要")
	}
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, "\x00\r\n") {
		return "", errors.New("设备执行服务目录无效，请重新检查设备")
	}
	// Each build has one partial file. lockf keeps its inode until every SSH
	// attempt has closed, so a replacement cannot append alongside an old cat.
	const upload = `set -eu
dir=$1
digest=$2
size=$3
stage="$dir/.partial"
binary="$dir/shioji-worker"
fail() { printf '%s\n' "$1" >&2; exit 1; }
verify() {
    [ -f "$1" ] && [ ! -L "$1" ] || return 1
    actual=$(/usr/bin/shasum -a 256 "$1") || return 1
    [ "${actual%% *}" = "$digest" ] || return 1
    /usr/bin/codesign --verify --strict "$1"
}
if [ -e "$binary" ] || [ -L "$binary" ]; then
    verify "$binary" || fail '已暂存的执行服务未通过摘要或签名校验'
    if [ -f "$stage" ] && [ ! -L "$stage" ]; then /bin/rm -f "$stage"; fi
    printf '%s\n%s\n' "$size" "$digest"
    exit 0
fi
[ ! -L "$stage" ] || fail '执行服务暂存文件无效'
offset=0
if [ -e "$stage" ]; then
    [ -f "$stage" ] || fail '执行服务暂存文件无效'
    offset=$(/usr/bin/stat -f %z "$stage")
    if [ "$offset" -gt "$size" ]; then
        /bin/rm -f "$stage"
        offset=0
    fi
fi
printf '%s\n' "$offset"
/bin/cat >> "$stage"
received=$(/usr/bin/stat -f %z "$stage")
[ "$received" -ge "$size" ] || exit 76
if [ "$received" -ne "$size" ] || ! verify "$stage"; then
    /bin/rm -f "$stage"
    fail '执行服务摘要或签名校验失败，可重新上传'
fi
/bin/chmod 700 "$stage"
if ! /bin/ln "$stage" "$binary" 2>/dev/null; then
    verify "$binary" || fail '无法发布已校验的执行服务'
fi
/bin/rm -f "$stage"
printf '%s\n' "$digest"
`
	return `set -eu
umask 077
root=` + shellQuote(directory) + `
digest=` + shellQuote(digest) + `
size=$1
case "$size" in ''|*[!0-9]*) exit 1 ;; esac
[ -d "$root" ] && [ ! -L "$root" ] || exit 1
[ "$(/usr/bin/stat -f %u "$root")" = "$(/usr/bin/id -u)" ]
[ "$(/usr/bin/stat -f %Lp "$root")" = 700 ]
updates="$root/updates"
[ ! -L "$updates" ]
/bin/mkdir -p "$updates"
[ "$(/usr/bin/stat -f %Lp "$updates")" = 700 ]
dest="$updates/$digest"
[ ! -L "$dest" ]
/bin/mkdir -p "$dest"
[ "$(/usr/bin/stat -f %Lp "$dest")" = 700 ]
[ ! -L "$dest/.upload.lock" ]
exec /usr/bin/lockf -k -t 0 "$dest/.upload.lock" /bin/sh -c ` + shellQuote(upload) + ` stage "$dest" "$digest" "$size"
`, nil
}
