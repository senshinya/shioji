package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"time"
)

type Request struct {
	Action, ID, DeviceID, Instance string
	Input                          domain.WorkerReplacement
}
type response struct {
	Operation  Operation
	Background domain.BackgroundService
	Error      string
	Code       string
}
type Ready struct {
	PID                int
	Instance, DeviceID string
	Build              domain.WorkerBuild
	Paused             bool
}
type Control struct {
	// Empty means drain, preserving the original private control contract.
	Action string `json:"action,omitempty"`
	// AttemptID correlates one private-channel delivery, not durable authority.
	AttemptID string `json:"attemptId,omitempty"`
	// AdmitBefore bounds one when-idle delivery attempt, independently of intent TTL.
	AdmitBefore           time.Time `json:"admitBefore,omitempty"`
	ExpiresAt             time.Time `json:"expiresAt,omitempty"`
	Instance, OperationID string
	Replacement           domain.WorkerReplacement
}
type Drained struct {
	PID                                      int
	Instance, OperationID, DeviceID, BuildID string
}

func drainPath(directory, id, instance string) string {
	return filepath.Join(directory, "supervisor", "drained", id+"-"+instance+".json")
}
func WriteDrained(directory string, d Drained) error {
	if !safeID(d.OperationID) || !safeID(d.Instance) {
		return errors.New("invalid drain identity")
	}
	return atomicJSON(drainPath(directory, d.OperationID, d.Instance), d)
}
func Call(ctx context.Context, directory string, in Request) (Operation, error) {
	out, err := exchange(ctx, directory, in)
	return out.Operation, err
}

func BackgroundCall(ctx context.Context, directory string, in Request) (domain.BackgroundService, error) {
	out, err := exchange(ctx, directory, in)
	return out.Background, err
}

func exchange(ctx context.Context, directory string, in Request) (response, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "supervisor.sock"))
	if err != nil {
		return response{}, err
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Minute)
	if until, ok := ctx.Deadline(); ok {
		deadline = until
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err = json.NewEncoder(conn).Encode(in); err != nil {
		return response{}, err
	}
	var out response
	if err = json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&out); err != nil {
		return out, err
	}
	if out.Error != "" {
		return out, &protocol.RemoteError{Code: out.Code, Message: out.Error}
	}
	return out, nil
}
func localPrincipal(conn *net.UnixConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Xucred
	var inner error
	if err = raw.Control(func(fd uintptr) { cred, inner = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) }); err != nil {
		return err
	}
	if inner != nil {
		return inner
	}
	if cred.Uid != uint32(os.Getuid()) {
		return errors.New("unauthorized supervisor principal")
	}
	return nil
}

// Query remains available when no business writer is running. Disk fallback is
// read-only and cannot certify that a committed candidate opened acceptance.
func Query(ctx context.Context, directory, id string) (Operation, error) {
	op, err := Call(ctx, directory, Request{Action: "status", ID: id})
	if err == nil {
		return checkSchemaBootstrapStatus(directory, hydrateSchemaPlan(directory, op)), nil
	}
	if ctx.Err() != nil {
		return op, ctx.Err()
	}
	j := journal{filepath.Join(directory, "supervisor")}
	if id == "" {
		op, err = j.current()
	} else {
		op, err = j.load(id)
	}
	if err != nil {
		return op, err
	}
	if id == "" {
		var health serviceHealth
		if e := readJSON(filepath.Join(directory, "supervisor", "health.json"), &health); e == nil && health.Phase == "blocked" {
			op.Phase, op.Error = "blocked", health.Error
			if op.DeviceID == "" {
				op.DeviceID = health.DeviceID
			}
			return op, nil
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return op, e
		}
	}
	if op.Phase == "applied" || op.Phase == "rolled-back" {
		op.Phase = "observing"
		op.Error = "服务控制尚未连接，正在核对是否已开放接纳"
	}
	return checkSchemaBootstrapStatus(directory, hydrateSchemaPlan(directory, op)), nil
}
