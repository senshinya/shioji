package managedexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

// ProcessInfo contains identity and presentation evidence, never argv or env.
// Observed ancestry does not create a registered process-group ticket.
type ProcessInfo struct {
	Identity         Identity
	UID              uint32
	ParentPID, Group int
	Name, Evidence   string
	Anchor           bool `json:",omitempty"`
}

func Processes() ([]ProcessInfo, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	return processInfos(procs), nil
}

func processInfos(procs []unix.KinfoProc) []ProcessInfo {
	var out []ProcessInfo
	for _, p := range procs {
		if p.Proc.P_stat != 5 && p.Eproc.Ucred.Uid == uint32(os.Getuid()) {
			out = append(out, ProcessInfo{Identity: identity(p), UID: p.Eproc.Ucred.Uid, ParentPID: int(p.Eproc.Ppid), Group: int(p.Eproc.Pgid), Name: unix.ByteSliceToString(p.Proc.P_comm[:]), Evidence: "unknown"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Identity.PID < out[j].Identity.PID })
	return out
}

func ProcessState(p ProcessInfo) string {
	current, err := unix.SysctlKinfoProc("kern.proc.pid", p.Identity.PID)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EIO) && errors.Is(unix.Kill(p.Identity.PID, 0), unix.ESRCH) {
		return "exited"
	}
	if err != nil {
		return "unobservable"
	}
	if identity(*current) != p.Identity || current.Eproc.Ucred.Uid != p.UID {
		return "changed"
	}
	if current.Proc.P_stat == 5 {
		return "exited"
	}
	return "running"
}

// The caller cannot terminate itself or its controlling chain. The independent
// device helper additionally protects the actual Tailnet runtime identities.
func ControlProcess(pid int) bool {
	return pid <= 1 || controlProcessIDs()[pid]
}
func controlProcessIDs() map[int]bool {
	protected := map[int]bool{}
	for at, n := os.Getpid(), 0; at > 1 && n < 128; n++ {
		protected[at] = true
		p, err := unix.SysctlKinfoProc("kern.proc.pid", at)
		if err != nil {
			break
		}
		next := int(p.Eproc.Ppid)
		if next == at {
			break
		}
		at = next
	}
	return protected
}

func SignalExact(p ProcessInfo, sig unix.Signal) error {
	if p.UID != uint32(os.Getuid()) || ControlProcess(p.Identity.PID) {
		return errors.New("恢复控制连接或其他用户进程不能关闭")
	}
	if ProcessState(p) != "running" {
		return errors.New("原进程已退出或身份已变化，请刷新确认")
	}
	// Register a kernel exit observation before the final identity recheck.
	// Darwin has no pidfd signal operation: this narrows, not eliminates, the
	// query/signal race. We never follow a replacement PID or process group.
	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(kq)
	change := unix.Kevent_t{Ident: uint64(p.Identity.PID), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}
	if _, err = unix.Kevent(kq, []unix.Kevent_t{change}, nil, nil); err != nil {
		return err
	}
	var events [1]unix.Kevent_t
	zero := unix.Timespec{}
	n, err := unix.Kevent(kq, nil, events[:], &zero)
	if err != nil {
		return err
	}
	if n > 0 || ProcessState(p) != "running" {
		return errors.New("发出信号前原进程已退出或身份变化")
	}
	return unix.Kill(p.Identity.PID, sig)
}

func (s *Scope) InspectExplicitMaintenance() (MaintenanceObservation, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	return s.inspectExplicitMaintenance()
}

func (s *Scope) inspectExplicitMaintenance() (out MaintenanceObservation, err error) {
	procs, err := Processes()
	if err != nil {
		return out, err
	}
	return s.observeProcesses(procs, controlProcessIDs())
}

func (s *Scope) observeProcesses(procs []ProcessInfo, protected map[int]bool) (out MaintenanceObservation, err error) {
	tickets, err := s.tickets()
	if err != nil {
		return out, err
	}
	owned := map[Identity]ProcessInfo{}
	var previous []ProcessInfo
	path := filepath.Join(s.Directory, s.Generation, "observed-processes.json")
	if err = readJSON(path, &previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	for _, p := range procs {
		if p.Identity.PID <= 1 || protected[p.Identity.PID] {
			continue
		}
		for _, prior := range previous {
			if p.Identity == prior.Identity && p.UID == prior.UID {
				p.Evidence = prior.Evidence
				p.Anchor = prior.Anchor
				owned[p.Identity] = p
				break
			}
		}
		for _, ticket := range tickets {
			if p.Identity == ticket.Root || p.Identity == ticket.Anchor {
				p.Evidence = "registered"
				p.Anchor = p.Identity == ticket.Anchor
				owned[p.Identity] = p
			}
			// Only an identity-verified live anchor grants group membership.
			if p.Group == ticket.Group {
				for _, anchor := range procs {
					if anchor.Identity == ticket.Anchor && anchor.Group == ticket.Group {
						p.Evidence = "registered"
						p.Anchor = p.Identity == ticket.Anchor
						owned[p.Identity] = p
						break
					}
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range procs {
			if _, ok := owned[p.Identity]; ok || p.Identity.PID <= 1 || protected[p.Identity.PID] {
				continue
			}
			for id := range owned {
				if p.ParentPID == id.PID {
					p.Evidence = "observed"
					owned[p.Identity] = p
					changed = true
					break
				}
			}
		}
	}
	var records []ProcessInfo
	for _, p := range procs {
		if actual, ok := owned[p.Identity]; ok {
			records = append(records, actual)
			out.Processes = append(out.Processes, MaintenanceProcess{Identity: actual.Identity, Name: actual.Name, UID: actual.UID, ParentPID: actual.ParentPID, Group: actual.Group, Evidence: actual.Evidence, Anchor: actual.Anchor})
		}
	}
	if !slices.Equal(previous, records) {
		if err = writeJSON(path, records); err != nil {
			return out, err
		}
	}
	b, _ := json.Marshal(struct {
		Generation string
		Processes  []ProcessInfo
	}{s.Generation, records})
	hash := sha256.Sum256(b)
	out.Ref = hex.EncodeToString(hash[:])
	out.Idle = len(records) == 0
	return out, nil
}

func (s *Scope) BeginExplicitStop(operation, ref string, force bool) (func() error, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	if operation == "" {
		return nil, errors.New("缺少处理标识")
	}
	observation, err := s.inspectExplicitMaintenance()
	if err != nil {
		return nil, err
	}
	if observation.Ref != ref {
		return nil, errors.New("后台环境已变化，请刷新并重新确认")
	}
	previous, err := s.load()
	if err != nil {
		return nil, err
	}
	if previous.Maintenance {
		return nil, ErrMaintenance
	}
	next := previous
	next.Maintenance = true
	next.Manual = &manualStopState{Operation: operation, Explicit: true}
	if force {
		next.Manual.ForceAction = operation + ":force"
	}
	for _, p := range observation.Processes {
		next.Manual.Targets = append(next.Manual.Targets, ProcessInfo{Identity: p.Identity, UID: p.UID, ParentPID: p.ParentPID, Group: p.Group, Name: p.Name, Evidence: p.Evidence, Anchor: p.Anchor})
	}
	if err = s.writeState(next); err != nil {
		return nil, err
	}
	return func() error {
		s.manager.mu.Lock()
		defer s.manager.mu.Unlock()
		current, err := s.load()
		if err != nil {
			return err
		}
		if current.Manual == nil || current.Manual.Operation != operation || current.Manual.Accepted {
			return errors.New("已接纳的停止不能撤回")
		}
		return s.writeState(previous)
	}, nil
}

func (s *Scope) StopExplicit(ctx context.Context) error {
	s.manager.mu.Lock()
	state, err := s.load()
	if err != nil {
		s.manager.mu.Unlock()
		return err
	}
	if state.Manual == nil || !state.Manual.Explicit || !state.Manual.Accepted {
		s.manager.mu.Unlock()
		return errors.New("明确关闭尚未接纳")
	}
	manual := state.Manual
	force := manual.ForceAction != ""
	sent := manual.NormalSent
	if force {
		sent = manual.ForceSent
	}
	if !sent {
		if force {
			manual.ForceSent = true
		} else {
			manual.NormalSent = true
		}
		state.Active = false
		if err = s.writeState(state); err != nil {
			s.manager.mu.Unlock()
			return err
		}
		// Children first; all targets were captured before admission. New or
		// unobservable descendants require a new preview, never a name sweep.
		for i := len(manual.Targets) - 1; i >= 0; i-- {
			p := manual.Targets[i]
			if !force && p.Anchor {
				continue
			}
			if ProcessState(p) != "running" {
				continue
			}
			sig := unix.SIGTERM
			if force {
				sig = unix.SIGKILL
			}
			if e := SignalExact(p, sig); e != nil {
				err = errors.Join(err, e)
			}
		}
	}
	s.manager.mu.Unlock()
	if err != nil {
		return err
	}
	for {
		err = s.ObserveExplicitStop()
		if err == nil || !force {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Scope) ObserveExplicitStop() error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if state.Manual == nil || !state.Manual.Explicit {
		return errors.New("明确关闭记录缺失")
	}
	observation, err := s.inspectExplicitMaintenance()
	if err != nil {
		return err
	}
	anchors := []ProcessInfo{}
	for _, p := range observation.Processes {
		if !p.Anchor {
			return ErrManualStillRunning
		}
		anchors = append(anchors, ProcessInfo{Identity: p.Identity, UID: p.UID})
	}
	for _, p := range anchors {
		if err = SignalExact(p, unix.SIGKILL); err != nil {
			return err
		}
	}
	if len(anchors) > 0 {
		return ErrManualStillRunning
	}
	state.Active = false
	state.Maintenance = false
	return s.writeState(state)
}

func (s *Scope) ExplicitStopProven() (bool, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return false, err
	}
	if state.Manual == nil || !state.Manual.Explicit {
		return false, nil
	}
	if state.Active || !state.Manual.Accepted || (!state.Manual.NormalSent && !state.Manual.ForceSent && !state.Manual.ExitConfirmed) {
		return false, ErrManualStillRunning
	}
	observation, err := s.inspectExplicitMaintenance()
	if err != nil {
		return false, err
	}
	if len(observation.Processes) > 0 {
		return false, ErrManualStillRunning
	}
	return true, nil
}

// ConfirmExplicitExit links a device helper's accepted operation to this exact
// generation after the caller obtained exclusive worker ownership. It emits no
// signal; live or newly observed descendants prevent a proof.
func (s *Scope) ConfirmExplicitExit(operation string) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	if operation == "" {
		return errors.New("缺少设备恢复操作")
	}
	state, err := s.load()
	if err != nil {
		return err
	}
	observation, err := s.inspectExplicitMaintenance()
	if err != nil {
		return err
	}
	if len(observation.Processes) > 0 {
		return ErrManualStillRunning
	}
	state.Active = false
	state.Maintenance = true
	state.Manual = &manualStopState{Operation: operation, Explicit: true, Accepted: true, ExitConfirmed: true}
	return s.writeState(state)
}
