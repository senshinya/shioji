package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type WorkerProcessIdentity struct {
	PID        int      `json:"pid"`
	Sec        int64    `json:"sec"`
	Usec       int32    `json:"usec"`
	UID        uint32   `json:"uid"`
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
}
type LegacyInspection struct {
	Info       domain.LegacyWorkerInfo
	Owner      WorkerProcessIdentity
	SessionIDs []string
}

func workerProcessData(pid int) (WorkerProcessIdentity, []string, error) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return WorkerProcessIdentity{}, nil, err
	}
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return WorkerProcessIdentity{}, nil, err
	}
	if len(raw) < 5 {
		return WorkerProcessIdentity{}, nil, errors.New("无法核对旧服务程序")
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	end := bytes.IndexByte(raw[4:], 0)
	if argc < 1 || end < 0 {
		return WorkerProcessIdentity{}, nil, errors.New("旧服务程序路径无效")
	}
	out := WorkerProcessIdentity{PID: pid, Sec: p.Proc.P_starttime.Sec, Usec: p.Proc.P_starttime.Usec, UID: p.Eproc.Ucred.Uid, Executable: string(raw[4 : 4+end])}
	at := 4 + end + 1
	for at < len(raw) && raw[at] == 0 {
		at++
	}
	for i := 0; i < argc; i++ {
		if at >= len(raw) {
			return out, nil, errors.New("旧服务启动参数不完整")
		}
		n := bytes.IndexByte(raw[at:], 0)
		if n < 0 {
			return out, nil, errors.New("旧服务启动参数无效")
		}
		out.Arguments = append(out.Arguments, string(raw[at:at+n]))
		at += n + 1
	}
	var environment []string
	for at < len(raw) && raw[at] != 0 {
		n := bytes.IndexByte(raw[at:], 0)
		if n < 0 {
			break
		}
		environment = append(environment, string(raw[at:at+n]))
		at += n + 1
	}
	return out, environment, nil
}
func WorkerProcess(pid int) (WorkerProcessIdentity, error) {
	p, _, e := workerProcessData(pid)
	return p, e
}

// Environment never crosses IPC. The owning device keeps a private temporary
// snapshot for explicitly authorized replacement and interrupted migration resume.
func WorkerEnvironment(expected WorkerProcessIdentity) ([]string, error) {
	actual, environment, err := workerProcessData(expected.PID)
	if err != nil {
		return nil, err
	}
	if actual.PID != expected.PID || actual.Sec != expected.Sec || actual.Usec != expected.Usec || actual.UID != expected.UID || actual.Executable != expected.Executable {
		return nil, errors.New("旧服务启动身份已改变")
	}
	return environment, nil
}
func InspectLegacyWorker(ctx context.Context, directory, expected string) (LegacyInspection, error) {
	var out LegacyInspection
	info, err := os.Lstat(directory)
	if err != nil {
		return out, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || !ok || st.Uid != uint32(os.Getuid()) {
		return out, errors.New("旧服务目录必须是当前用户的私有目录")
	}
	path := filepath.Join(directory, "worker.sock")
	socket, err := os.Lstat(path)
	if err != nil {
		return out, err
	}
	if socket.Mode()&os.ModeSocket == 0 || socket.Mode().Perm() != 0600 {
		return out, errors.New("旧服务 socket 不安全")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return out, err
	}
	var pid int
	var peer *unix.Xucred
	var inner error
	err = raw.Control(func(fd uintptr) {
		peer, inner = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if inner == nil {
			pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		}
	})
	if err != nil {
		return out, err
	}
	if inner != nil {
		return out, inner
	}
	if peer.Uid != uint32(os.Getuid()) || pid <= 1 || pid == os.Getpid() && strings.HasSuffix(os.Args[0], "shioji-worker") {
		return out, errors.New("旧服务所有者不匹配")
	}
	out.Owner, err = WorkerProcess(pid)
	if err != nil {
		return out, err
	}
	if out.Owner.UID != uint32(os.Getuid()) {
		return out, errors.New("旧服务用户已改变")
	}
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	codec, nonce := protocol.NewCodec(conn, conn), domain.NewID()
	if err = codec.Write(protocol.Frame{Type: "hello", Major: 1, Nonce: nonce, DeviceID: expected}); err != nil {
		return out, err
	}
	var welcome protocol.Frame
	if err = codec.Read(&welcome); err != nil {
		return out, err
	}
	if welcome.Type != "welcome" || welcome.Major != 1 || welcome.Nonce != nonce || welcome.DeviceID == "" || welcome.Epoch == "" || expected != "" && welcome.DeviceID != expected {
		return out, errors.New("旧服务身份或协议已改变")
	}
	out.Info = domain.LegacyWorkerInfo{DeviceID: welcome.DeviceID, Major: 1, Minor: welcome.Minor, Supported: welcome.Minor == 3}
	fingerprint, _ := json.Marshal(struct {
		Owner             WorkerProcessIdentity
		Device, Directory string
	}{out.Owner, welcome.DeviceID, directory})
	sum := sha256.Sum256(fingerprint)
	out.Info.OwnerRef = hex.EncodeToString(sum[:])
	call := func(method string, input, out any) error {
		body, e := json.Marshal(input)
		if e != nil {
			return e
		}
		id := domain.NewID()
		if e = codec.Write(protocol.Frame{Type: "command", ID: id, DeviceID: welcome.DeviceID, Epoch: welcome.Epoch, Method: method, Body: body}); e != nil {
			return e
		}
		var f protocol.Frame
		if e = codec.Read(&f); e != nil {
			return e
		}
		if f.Type != "result" || f.ID != id || f.DeviceID != welcome.DeviceID || f.Epoch != welcome.Epoch {
			return errors.New("旧服务回执身份不一致")
		}
		if f.Error != "" {
			return errors.New(f.Error)
		}
		return json.Unmarshal(f.Body, out)
	}
	if !out.Info.Supported {
		out.Info.Message = "此旧版协议尚未验证，无法自动迁移"
		return out, nil
	}
	var snapshot struct {
		DeviceID string `json:"deviceId"`
		Sessions []struct {
			ID     string                `json:"id"`
			Status domain.ExecutionState `json:"status"`
		} `json:"sessions"`
		Operations []struct {
			State domain.OperationState `json:"state"`
		} `json:"operations"`
	}
	if err = call("snapshot", struct{}{}, &snapshot); err != nil {
		return out, err
	}
	if snapshot.DeviceID != welcome.DeviceID {
		return out, errors.New("旧服务数据不属于该设备")
	}
	for _, operation := range snapshot.Operations {
		if operation.State != domain.OperationConfirmed && operation.State != domain.OperationRejected && operation.State != domain.OperationAbandoned {
			out.Info.Uncertain = true
		}
	}
	for _, session := range snapshot.Sessions {
		if session.ID == "" {
			return out, errors.New("旧会话记录不完整")
		}
		out.SessionIDs = append(out.SessionIDs, session.ID)
		if session.Status.Active() {
			out.Info.Busy = true
		} else if !session.Status.CanSubmit() {
			out.Info.Uncertain = true
		}
		var detail struct {
			Session struct {
				ID string `json:"id"`
			} `json:"session"`
			Requests []struct {
				State domain.RequestState `json:"state"`
			} `json:"requests"`
			Queue struct {
				Paused bool `json:"paused"`
				Items  []struct {
					ID string `json:"id"`
				} `json:"items"`
			} `json:"queue"`
		}
		if err = call("session", struct {
			ID    string `json:"id"`
			Limit int    `json:"limit"`
		}{session.ID, 1}, &detail); err != nil {
			return out, err
		}
		if detail.Session.ID != session.ID {
			return out, errors.New("旧会话身份不一致")
		}
		for _, r := range detail.Requests {
			if r.State == domain.RequestPending || r.State == domain.RequestSubmitting || r.State == "" {
				out.Info.Busy = true
			}
		}
		if len(detail.Queue.Items) > 0 && !detail.Queue.Paused {
			out.Info.Busy = true
		}
	}
	if out.Info.Busy {
		out.Info.Message = "设备仍有任务或待处理请求，请空闲后更新"
	} else if out.Info.Uncertain {
		out.Info.Message = "旧版无法确认全部执行状态，停止旧服务可能中断任务"
	}
	if filepath.Base(out.Owner.Executable) != "shioji-worker" {
		out.Info.Supported = false
		out.Info.Message = fmt.Sprintf("无法确认旧服务程序 %s", filepath.Base(out.Owner.Executable))
	}
	return out, nil
}
