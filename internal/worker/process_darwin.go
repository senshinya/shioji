package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type processRecord struct {
	PID         int   `json:"pid"`
	StartedSec  int64 `json:"startedSec"`
	StartedUsec int32 `json:"startedUsec"`
	Starting    bool  `json:"starting"`
}

func recordProcess(dir string, pid int) error {
	record := processRecord{PID: pid, Starting: pid == 0}
	if pid != 0 {
		proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			return err
		}
		record.StartedSec = proc.Proc.P_starttime.Sec
		record.StartedUsec = proc.Proc.P_starttime.Usec
	}
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
	if err != nil && !errors.Is(err, unix.ESRCH) && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("cannot verify previous runtime ownership: %w", err)
	}
	if err == nil && proc.Proc.P_starttime.Sec == record.StartedSec && proc.Proc.P_starttime.Usec == record.StartedUsec {
		return fmt.Errorf("previous Codex process %d is still present; reconcile it before starting another runtime", record.PID)
	}
	return os.Remove(path)
}
