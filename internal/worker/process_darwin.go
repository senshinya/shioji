package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type processRecord struct {
	Session     string `json:"session,omitempty"`
	PID         int    `json:"pid"`
	StartedSec  int64  `json:"startedSec"`
	StartedUsec int32  `json:"startedUsec"`
	Starting    bool   `json:"starting"`
}

func recordProcess(dir string, pid int) error { return recordSessionProcess(dir, "", pid) }
func recordSessionProcess(dir, session string, pid int) error {
	record := processRecord{Session: session, PID: pid, Starting: pid == 0}
	if pid != 0 {
		proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			return err
		}
		record.StartedSec = proc.Proc.P_starttime.Sec
		record.StartedUsec = proc.Proc.P_starttime.Usec
	}
	return writeProcessRecord(dir, record)
}
func writeProcessRecord(dir string, record processRecord) error {
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "runtime-process-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, "runtime-process.json")); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func checkPreviousProcess(dir string) error {
	path := filepath.Join(dir, "runtime-process.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var record processRecord
	if err = json.Unmarshal(b, &record); err != nil {
		return errors.New("previous runtime ownership record is unreadable; manual reconciliation required")
	}
	if record.Starting || record.PID <= 0 {
		return errors.New("previous runtime launch was unconfirmed; manual reconciliation required")
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", record.PID)
	// An empty Darwin sysctl result is reported as EIO by SysctlKinfoProc.
	// Clear it only when a separate, read-only signal probe confirms no PID.
	if errors.Is(err, unix.EIO) && errors.Is(unix.Kill(record.PID, 0), unix.ESRCH) {
		err = unix.ESRCH
	}
	if err != nil && !errors.Is(err, unix.ESRCH) && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("cannot verify previous runtime ownership: %w", err)
	}
	if err == nil && proc.Proc.P_starttime.Sec == record.StartedSec && proc.Proc.P_starttime.Usec == record.StartedUsec {
		return fmt.Errorf("previous Agent process %d is still present; reconcile it before starting another runtime", record.PID)
	}
	return os.Remove(path)
}

// A start-time match prevents PID reuse from authorizing termination. A live
// process outside the dedicated app-server group is never signalled.
func ownedProcessPresent(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "runtime-process.json"))
	if err != nil {
		return false
	}
	var record processRecord
	if json.Unmarshal(b, &record) != nil || record.Starting || record.PID <= 0 {
		return false
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", record.PID)
	return err == nil && proc.Proc.P_starttime.Sec == record.StartedSec && proc.Proc.P_starttime.Usec == record.StartedUsec
}
func stopPreviousProcess(ctx context.Context, dir string) error {
	if !ownedProcessPresent(dir) {
		return checkPreviousProcess(dir)
	}
	b, err := os.ReadFile(filepath.Join(dir, "runtime-process.json"))
	if err != nil {
		return err
	}
	var record processRecord
	if err = json.Unmarshal(b, &record); err != nil {
		return err
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", record.PID)
	if err != nil {
		return err
	}
	if proc.Proc.P_starttime.Sec != record.StartedSec || proc.Proc.P_starttime.Usec != record.StartedUsec {
		return errors.New("旧进程身份已改变，请重新核对")
	}
	group, err := unix.Getpgid(record.PID)
	if err != nil {
		return err
	}
	if group != record.PID || record.PID == os.Getpid() {
		return errors.New("旧进程不属于独立的 Agent 进程组，无法安全停止")
	}
	if err = unix.Kill(-record.PID, unix.SIGTERM); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for ownedProcessPresent(dir) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("旧进程尚未退出；未启动新进程，请稍后重试")
		case <-time.After(20 * time.Millisecond):
		}
	}
	return checkPreviousProcess(dir)
}
