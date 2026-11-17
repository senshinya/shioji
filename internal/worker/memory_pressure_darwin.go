package worker

import (
	"context"
	"errors"
	"sort"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/harness"
)

func nativeMemoryPressure() uint32 {
	level, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
	if err != nil {
		return 0
	}
	return level
}
func (s *Server) memoryPressure() uint32 {
	if s.pressureSample != nil {
		return s.pressureSample()
	}
	return nativeMemoryPressure()
}

// Caller holds dispatchMu. Kernel WARNING reclaims; only CRITICAL refuses.
// Concurrent starts share a reclamation and never hold the device gate waiting.
func (s *Server) admitMemoryPressure(exclude string) error {
	level := s.memoryPressure()
	if level != 2 && level != 4 {
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	if ongoing := s.pressureReclaim; ongoing != nil {
		s.dispatchMu.Unlock()
		select {
		case <-ongoing:
		case <-ctx.Done():
		}
		s.dispatchMu.Lock()
	} else {
		ongoing := make(chan struct{})
		s.pressureReclaim = ongoing
		func() {
			defer func() { s.pressureReclaim = nil; close(ongoing) }()
			s.runtimeMu.RLock()
			var ids []string
			for id := range s.runtimes {
				if id != exclude && s.sessionTransitions[id] == nil {
					ids = append(ids, id)
				}
			}
			s.runtimeMu.RUnlock()
			sort.Slice(ids, func(i, j int) bool { return s.idleRuntimes[ids[i]].since.Before(s.idleRuntimes[ids[j]].since) })
			reclaimed := 0
			for _, id := range ids {
				if reclaimed >= 3 || ctx.Err() != nil {
					break
				}
				if level := s.memoryPressure(); level != 2 && level != 4 {
					break
				}
				state, err := s.store.RuntimeGCIdle(id)
				if err != nil || !state.Idle {
					continue
				}
				r := s.currentRuntime(id)
				if r == nil || !r.ManagedCoverage() || !r.Readiness().Capabilities.Continue {
					continue
				}
				if pending, ok := r.(harness.PendingRuntime); ok && pending.PendingOperations() != 0 {
					continue
				}
				reclaimed++
				_ = s.reclaimRuntime(id, time.Now(), true, ctx)
			}
		}()
	}
	// Re-check the kernel verdict; cleanup success alone cannot prove recovery.
	for attempt := 0; s.memoryPressure() == 4; attempt++ {
		if attempt >= 2 {
			return errors.New("设备内存压力过高，请稍后重试；输入未发送")
		}
		s.dispatchMu.Unlock()
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-s.ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		s.dispatchMu.Lock()
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
	}
	return s.updateAdmission()
}
