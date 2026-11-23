package supervisor

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"time"
)

var ErrOwned = errors.New("execution supervisor already owned")

type LegacyInstall struct {
	Operation Operation
	Lease     *os.File
}

// AcquireLease is also used by the one-time signed installer. Only this control
// lease is handed to its detached helper; no business-writer FD is inherited.
func AcquireLease(directory string) (*os.File, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(directory, "supervisor.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "supervisor control")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrOwned
	}
	return f, nil
}
func AcceptLegacy(directory string, in domain.WorkerReplacement, device string) (Operation, error) {
	j := journal{filepath.Join(directory, "supervisor")}
	op, err := j.accept(in, "legacy-install", device)
	if err != nil {
		return op, err
	}
	op.Legacy = true
	return op, j.save(op)
}
func SaveLegacy(directory string, op Operation) error {
	if !op.Legacy {
		return errors.New("not a legacy installation")
	}
	return (journal{filepath.Join(directory, "supervisor")}).save(op)
}
func LegacyCancelled(directory, id string) bool {
	if !safeID(id) {
		return false
	}
	var cancel struct{ ID string }
	return readJSON(filepath.Join(directory, "supervisor", "cancel", id+".json"), &cancel) == nil && cancel.ID == id
}
func CancelLegacy(ctx context.Context, directory, id, device string) (Operation, error) {
	op, err := Query(ctx, directory, id)
	if err != nil {
		return op, err
	}
	if !op.Legacy || device == "" || op.DeviceID != device {
		return op, errors.New("旧服务更新不属于所选设备")
	}
	if op.Terminal() {
		return op, nil
	}
	if op.Phase != "preparing" && op.Phase != "waiting" {
		return op, errors.New("切换已经开始，无法取消")
	}
	// This is a cancellation request, never a claim that switching was stopped.
	err = atomicJSON(filepath.Join(directory, "supervisor", "cancel", id+".json"), struct{ ID string }{id})
	return op, err
}

func LegacyOperation(directory, id string) (Operation, error) {
	op, err := (journal{filepath.Join(directory, "supervisor")}).load(id)
	if err == nil && !op.Legacy {
		err = errors.New("not a legacy installation")
	}
	return op, err
}

// InstallationHandoff is published only after the source writer and fixed
// host have exited. The installer retains its control lease until publication;
// Runtime must acquire that same lease before consuming the record.
func InstallationHandoff(directory string, install LegacyInstall) error {
	if install.Lease == nil || !install.Operation.Legacy {
		return errors.New("missing installation control lease")
	}
	actual, err := install.Lease.Stat()
	expected, statErr := os.Lstat(filepath.Join(directory, "supervisor.lock"))
	if err != nil || statErr != nil || !os.SameFile(actual, expected) {
		return errors.New("installation control lease changed")
	}
	if err := unix.Flock(int(install.Lease.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	op := install.Operation
	if op.Phase != "switching" && op.Phase != "observing" {
		return errors.New("installation is not ready for Runtime")
	}
	op.Phase = "installing"
	op.HostStartDeadline = time.Now().Add(45 * time.Second)
	return SaveLegacy(directory, op)
}

// PendingInstallation is read by the Runtime entry point to select the signed
// candidate's supervisor code, without committing its worker selection early.
func PendingInstallation(directory string) (Operation, bool, error) {
	op, err := (journal{filepath.Join(directory, "supervisor")}).current()
	return op, err == nil && op.Legacy && op.Phase == "installing", err
}

// A status query never launches or resends anything. Keep the underlying
// handoff resumable, but report a missing host after its bounded launch window.
func installationStatus(directory string, op Operation) Operation {
	if op.Phase != "installing" || op.HostStartDeadline.IsZero() || time.Now().Before(op.HostStartDeadline) {
		return op
	}
	lease, err := AcquireLease(directory)
	if err != nil {
		return op
	}
	defer lease.Close()
	current, ok, err := PendingInstallation(directory)
	if err == nil && ok && current.Input == op.Input && current.DeviceID == op.DeviceID {
		op.Phase = "blocked"
		op.Error = "执行 Runtime 尚未接管；重新连接设备后将接续原更新"
	}
	return op
}
