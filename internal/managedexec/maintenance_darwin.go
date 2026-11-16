package managedexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

var ErrManualStillRunning = errors.New("后台环境尚未退出，未授权的强制结束不会自动执行")

type manualStopState struct {
	ExitConfirmed bool          `json:",omitempty"`
	Explicit      bool          `json:",omitempty"`
	Targets       []ProcessInfo `json:",omitempty"`
	Accepted      bool          `json:",omitempty"`
	Operation     string
	NormalSent    bool
	ForceAction   string
	ForceSent     bool
}

type MaintenanceProcess struct {
	Identity         Identity
	Name             string
	Infrastructure   bool
	UID              uint32
	ParentPID, Group int
	Evidence         string
	Anchor           bool
}

type MaintenanceObservation struct {
	Ref       string
	Idle      bool
	Processes []MaintenanceProcess
}

func (s *Scope) InspectMaintenance() (MaintenanceObservation, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	return s.inspectMaintenance()
}

func (s *Scope) inspectMaintenance() (out MaintenanceObservation, err error) {
	state, err := s.load()
	if err != nil {
		return out, err
	}
	for _, registered := range state.Launches {
		if !registered {
			return out, errors.New("工具启动结果尚未确认")
		}
	}
	tickets, err := s.tickets()
	if err != nil {
		return out, err
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return out, err
	}
	groups := map[int]bool{}
	owned := map[int]bool{}
	var identities []Identity
	out.Idle = true
	for _, t := range tickets {
		if t.Group <= 1 || t.Group == unix.Getpgrp() {
			return out, errors.New("不能将控制服务纳入处理范围")
		}
		if err = knownMemberStayedInGroup(t); err != nil {
			return out, err
		}
		var members []unix.KinfoProc
		for _, p := range procs {
			if int(p.Eproc.Pgid) == t.Group && p.Proc.P_stat != 5 {
				members = append(members, p)
			}
		}
		if len(members) == 0 {
			continue
		}
		if !anchorPresent(t, members) {
			return out, errors.New("后台进程的归属无法确认")
		}
		groups[t.Group] = true
		live, tracked := s.manager.live[t.ID]
		idleService := t.IdleService && tracked && live.scope.Generation == s.Generation && live.ticket.IdleService && !t.ServiceUnverified && !live.ticket.ServiceUnverified
		if t.Kind != "worker" && !idleService {
			out.Idle = false
		}
		for _, p := range members {
			id := identity(p)
			owned[id.PID] = true
			identities = append(identities, id)
			if id == t.Anchor {
				continue
			}
			infrastructure := t.Kind == "worker" && id == t.Root
			if id != t.Root {
				out.Idle = false
			}
			out.Processes = append(out.Processes, MaintenanceProcess{Identity: id, Name: unix.ByteSliceToString(p.Proc.P_comm[:]), Infrastructure: infrastructure})
		}
	}
	// A detached group has no verified anchor; naming it a descendant is not
	// sufficient authority to signal it. Registered sibling groups remain valid.
	detached := false
	for _, p := range procs {
		if p.Proc.P_stat != 5 && owned[int(p.Eproc.Ppid)] && !groups[int(p.Eproc.Pgid)] {
			detached = true
			out.Processes = append(out.Processes, MaintenanceProcess{Identity: identity(p), Name: unix.ByteSliceToString(p.Proc.P_comm[:]), UID: p.Eproc.Ucred.Uid, ParentPID: int(p.Eproc.Ppid), Group: int(p.Eproc.Pgid), Evidence: "observed"})
		}
	}
	if detached {
		out.Idle = false
		return out, errors.New("存在未完整登记的脱离进程，可明确确认强制关闭")
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].PID < identities[j].PID })
	sort.Slice(out.Processes, func(i, j int) bool { return out.Processes[i].Identity.PID < out.Processes[j].Identity.PID })
	b, _ := json.Marshal(struct {
		Generation string
		Processes  []Identity
	}{s.Generation, identities})
	hash := sha256.Sum256(b)
	out.Ref = hex.EncodeToString(hash[:])
	return out, nil
}

// The caller must commit durable operation acceptance before closing anything.
// Until then, rollback restores admission without sending any signal.
func (s *Scope) BeginManualStop(operation, expectedRef string) (func() error, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	if operation == "" {
		return nil, errors.New("缺少处理标识")
	}
	observation, err := s.inspectMaintenance()
	if err != nil {
		return nil, err
	}
	if observation.Ref != expectedRef {
		return nil, errors.New("后台进程已变化，请重新检查并确认")
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
	next.Manual = &manualStopState{Operation: operation}
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
		if current.Manual == nil || current.Manual.Operation != operation || current.Manual.NormalSent || current.Manual.ForceAction != "" {
			return errors.New("已接纳的停止不能撤回")
		}
		return s.writeState(previous)
	}, nil
}

// AcceptManualStop opens the signal boundary only after the owning worker's
// transaction has persisted the exact operation and all selected queues.
func (s *Scope) AcceptManualStop(operation string) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if state.Manual == nil || state.Manual.Operation != operation {
		return errors.New("停止许可与已接纳操作不一致")
	}
	state.Manual.Accepted = true
	return s.writeState(state)
}
func (s *Scope) ManualStopOperation() (string, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return "", err
	}
	if state.Manual == nil {
		return "", nil
	}
	return state.Manual.Operation, nil
}

// Recovery releases an interrupted preparation fence, never the no-force guard.
// A crashed generation cannot accept new input; it can be selected afresh.
func (s *Scope) RecoverManualStop(accepted bool) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if state.Manual == nil {
		return nil
	}
	state.Active = false
	state.Maintenance = false
	state.Manual.Accepted = accepted
	if !state.Manual.ForceSent {
		state.Manual.ForceAction = ""
	}
	return s.writeState(state)
}

func (s *Scope) ManualStopPending() (bool, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	return state.Manual != nil, err
}

// A missing/unreadable record never grants force during shutdown.
func (s *Scope) ForceAllowed() bool {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	return err == nil && (state.Retirement != "" || state.Manual == nil || state.Manual.ForceAction != "")
}

// Adapter read/close paths must not signal a recycled PID or bypass normal-only
// consent. User descendants are retired through the scope, never by this helper.
func (s *Scope) SignalRoot(pid int, sig unix.Signal) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if sig == unix.SIGKILL && state.Manual != nil && state.Manual.ForceAction == "" {
		return ErrManualStillRunning
	}
	tickets, err := s.tickets()
	if err != nil {
		return err
	}
	for _, ticket := range tickets {
		if ticket.Root.PID != pid {
			continue
		}
		p, e := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if errors.Is(e, unix.ESRCH) || errors.Is(e, unix.EIO) && errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
			return nil
		}
		if e != nil {
			return e
		}
		if identity(*p) != ticket.Root || int(p.Eproc.Pgid) != ticket.Group {
			return errors.New("运行实例已变化，未发送信号")
		}
		if p.Proc.P_stat == 5 {
			return nil
		}
		return unix.Kill(pid, sig)
	}
	return errors.New("无法验证运行实例的归属")
}

func (s *Scope) ForceManualStop(operation, action string) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if action == "" || state.Manual == nil || state.Manual.Operation != operation {
		return errors.New("强制结束不属于已确认的处理范围")
	}
	if state.Manual.Explicit {
		_, err = s.inspectExplicitMaintenance()
	} else {
		_, err = s.inspectMaintenance()
	}
	if err != nil {
		return err
	}
	if state.Manual.Explicit {
		observation, e := s.inspectExplicitMaintenance()
		if e != nil {
			return e
		}
		state.Manual.Targets = nil
		for _, p := range observation.Processes {
			state.Manual.Targets = append(state.Manual.Targets, ProcessInfo{Identity: p.Identity, UID: p.UID, ParentPID: p.ParentPID, Group: p.Group, Name: p.Name, Evidence: p.Evidence, Anchor: p.Anchor})
		}
	}
	if state.Manual.ForceAction == action {
		return nil
	}
	state.Manual.Accepted = true
	state.Manual.ForceAction = action
	state.Manual.ForceSent = false
	return s.writeState(state)
}

// Cancellation revokes unsent force and reopens only a lifetime that has never
// begun stopping. A partially retired generation keeps its durable guard.
func (s *Scope) CancelManualStop(operation string) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if state.Manual == nil || state.Manual.Operation != operation {
		return nil
	}
	if !state.Manual.ForceSent {
		state.Manual.ForceAction = ""
	}
	if state.Active && !state.Manual.NormalSent && !state.Manual.ForceSent {
		state.Manual = nil
	}
	state.Maintenance = false
	return s.writeState(state)
}

func (s *Scope) stopManual(ctx context.Context) error {
	s.manager.mu.Lock()
	state, err := s.load()
	if err != nil {
		s.manager.mu.Unlock()
		return err
	}
	if state.Manual == nil || !state.Manual.Accepted {
		s.manager.mu.Unlock()
		return errors.New("人工停止尚未接纳，未发送退出请求")
	}
	if _, err = s.inspectMaintenance(); err != nil {
		s.manager.mu.Unlock()
		return err
	}
	force := state.Manual.ForceAction != ""
	sent := state.Manual.NormalSent
	if force {
		sent = state.Manual.ForceSent
	}
	if !sent {
		tickets, e := s.tickets()
		if e != nil {
			s.manager.mu.Unlock()
			return e
		}
		state.Active = false
		state.Maintenance = false
		if force {
			state.Manual.ForceSent = true
		} else {
			state.Manual.NormalSent = true
		}
		// Persist the send boundary first. Recovery observes; it never resends
		// an uncertain stop merely because its receipt was not delivered.
		if err = s.writeState(state); err != nil {
			s.manager.mu.Unlock()
			return err
		}
		sig := unix.SIGTERM
		if force {
			sig = unix.SIGKILL
		}
		for _, t := range tickets {
			members, e := groupMembers(t.Group)
			if e != nil {
				err = e
				break
			}
			if len(members) == 0 {
				continue
			}
			if !anchorPresent(t, members) {
				err = errors.New("停止前进程归属已变化")
				break
			}
			if e = unix.Kill(-t.Group, sig); e != nil && !errors.Is(e, unix.ESRCH) {
				err = e
				break
			}
		}
	}
	s.manager.mu.Unlock()
	if err != nil {
		return err
	}
	until := time.Now().Add(2 * time.Second)
	for {
		err = s.ObserveManualStop()
		if err == nil || !force || !errors.Is(err, ErrManualStillRunning) {
			return err
		}
		if time.Now().After(until) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Trusted anchors contain no user work. Retire one only after proving that no
// executable member or detached child survives in its registered lifetime.
func (s *Scope) ObserveManualStop() error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	if state.Manual == nil {
		return errors.New("人工停止记录缺失")
	}
	if _, err = s.inspectMaintenance(); err != nil {
		return err
	}
	tickets, err := s.tickets()
	if err != nil {
		return err
	}
	remaining := false
	for _, t := range tickets {
		members, e := groupMembers(t.Group)
		if e != nil {
			return e
		}
		if len(members) == 1 && identity(members[0]) == t.Anchor {
			if e = unix.Kill(t.Anchor.PID, unix.SIGKILL); e != nil && !errors.Is(e, unix.ESRCH) {
				return e
			}
			members, e = groupMembers(t.Group)
			if e != nil {
				return e
			}
		}
		if len(members) > 0 {
			remaining = true
			continue
		}
		t.State = LaunchStopped
		if err = writeJSON(s.launchPath(t.ID), t); err != nil {
			return err
		}
		delete(s.manager.live, t.ID)
	}
	if remaining {
		return ErrManualStillRunning
	}
	state.Active = false
	state.Maintenance = false
	return s.writeState(state)
}
