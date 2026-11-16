package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/transport"
)

var ErrLegacyRecoveryActive = errors.New("旧恢复程序仍在收尾，请稍后重新检查服务")

// Called only under owner.lock by the new fixed service. A historic global
// partial fence is preserved verbatim, but is no longer a global startup veto.
// The source receipts and per-session unknown-input guards remain untouched.
func archiveLegacyRecoveryFence(directory string) error {
	path := filepath.Join(directory, "worker-recovery", "fence.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var fence transport.RecoveryFence
	if json.Unmarshal(raw, &fence) == nil && recoveryID(fence.ID) {
		var prior deviceRecoveryRecord
		if recoveryRead(directory, fence.ID, &prior) == nil && prior.Runner.Identity.PID > 1 && managedexec.ProcessState(prior.Runner) == "running" {
			return ErrLegacyRecoveryActive
		}
	}
	archive := filepath.Join(directory, "worker-recovery", "superseded")
	if err = os.MkdirAll(archive, 0700); err != nil {
		return err
	}
	if err = os.Rename(path, filepath.Join(archive, "fence-"+domain.NewID()+".json")); err != nil {
		return err
	}
	for _, dir := range []string{archive, filepath.Dir(path)} {
		f, e := os.Open(dir)
		if e != nil {
			return e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
