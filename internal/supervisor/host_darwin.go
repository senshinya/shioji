package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"sync"
	"syscall"
	"time"
)

type Options struct {
	LegacyInstall          *LegacyInstall
	Directory, InitialPath string
	Arguments              []string
	Verify                 func(context.Context, string) (string, domain.WorkerBuild, error)
	Select                 func(string) error
	// Internal test clocks; the product uses the defaults below.
	ReadyTimeout, DrainTimeout, StopGrace, KillWait time.Duration
	RetryMin, RetryMax                              time.Duration
}
type child struct {
	cmd       *exec.Cmd
	identity  managedexec.ProcessInfo
	instance  string
	build     domain.WorkerBuild
	done      chan struct{}
	err       error
	startedAt time.Time
}
type host struct {
	health            serviceHealth
	retry             crashRetry
	updateID          string
	readyDone         bool
	options           Options
	journal           journal
	instance          string
	mu                sync.Mutex
	child             *child
	device            string
	updating, blocked bool
	wake              chan struct{}
	cancelUpdate      context.CancelFunc
	updateDone        chan struct{}
	ctx               context.Context
}

func Run(ctx context.Context, o Options) error {
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 3 * time.Minute
	}
	if o.DrainTimeout == 0 {
		o.DrainTimeout = 30 * time.Second
	}
	if o.StopGrace == 0 {
		o.StopGrace = 8 * time.Second
	}
	if o.KillWait == 0 {
		o.KillWait = 5 * time.Second
	}
	if !filepath.IsAbs(o.Directory) || o.Verify == nil || o.Select == nil {
		return errors.New("incomplete supervisor configuration")
	}
	if err := os.MkdirAll(o.Directory, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(o.Directory)
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || st.Mode().Perm() != 0700 || owner.Uid != uint32(os.Getuid()) {
		return errors.New("unsafe supervisor directory")
	}
	var lease *os.File
	if o.LegacyInstall != nil {
		lease = o.LegacyInstall.Lease
		if lease == nil {
			return errors.New("missing installation control lease")
		}
		actual, e := lease.Stat()
		expected, e2 := os.Lstat(filepath.Join(o.Directory, "supervisor.lock"))
		if e != nil || e2 != nil || !os.SameFile(actual, expected) {
			return errors.New("installation control lease changed")
		}
		if e = unix.Flock(int(lease.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
			return e
		}
	} else {
		lease, err = AcquireLease(o.Directory)
		if err != nil {
			return err
		}
	}
	defer lease.Close()

	path := filepath.Join(o.Directory, "supervisor.sock")
	if existing, e := os.Lstat(path); e == nil {
		if existing.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace non-socket control path")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	h := &host{options: o, journal: journal{filepath.Join(o.Directory, "supervisor")}, instance: domain.NewID(), wake: make(chan struct{}, 1), ctx: ctx}
	if err = h.loadHealth(); err != nil {
		return err
	}
	identity, schema, identityErr := store.ReadWorkerIdentity(ctx, o.Directory)
	if identityErr == nil {
		h.device = identity
		h.health.DeviceID, h.health.Schema = identity, schema
	}
	var recovered Operation
	if o.LegacyInstall != nil {
		recovered = o.LegacyInstall.Operation
		recovered.Phase = "observing"
		err = h.journal.save(recovered)
	} else {
		recovered, err = h.journal.recover()
	}
	if err != nil {
		return err
	}

	h.blocked = recovered.Phase == "blocked" || h.health.Phase == "blocked" && !h.health.CrashLoop
	go h.serve(listener)
	go h.monitor()
	if !h.blocked {
		paused := recovered.Phase == "applied" || recovered.Phase == "rolled-back" || o.LegacyInstall != nil
		opID := ""
		if paused {
			opID = recovered.Input.ID
		}
		h.mu.Lock()
		h.updateID = opID
		h.mu.Unlock()
		if o.LegacyInstall != nil {
			// This is still an uncommitted candidate. Its first failure must
			// retain the installation handoff rather than retry another writer.
			err = h.startSelected(o.InitialPath, paused, opID)
		} else {
			// Applied/rolled-back selections may start paused until resume;
			// they retain ordinary dependency and endpoint retry semantics.
			err = h.startSelectedRetry(o.InitialPath, paused, opID)
		}
		if ctx.Err() != nil {
			return nil
		}
		if err == nil && o.LegacyInstall != nil {
			if h.device != recovered.DeviceID || h.child.build.BuildID != recovered.Input.BuildID {
				err = errors.New("installed candidate identity differs from accepted update")
			} else {
				err = o.Select(recovered.Input.BuildID)
				if err == nil {
					err = h.phase(&recovered, "applied")
				}
			}
		}
		if err == nil && paused {
			expected := recovered.Input.BuildID
			if recovered.Phase == "rolled-back" {
				expected = recovered.PreviousBuild
			}
			if h.child.build.BuildID != expected {
				err = errors.New("selected build differs from committed receipt")
			} else {
				err = h.resume(ctx, h.child, opID)
			}
		}
		if err != nil {
			if e := h.blockStartup(err); e != nil {
				return errors.Join(err, e)
			}
		} else {
			h.mu.Lock()
			h.readyDone = true
			h.updateID = ""
			h.health.Phase, h.health.Error = "ready", ""
			h.health.CrashLoop = false
			err = h.saveHealth()
			h.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}

	for {
		h.mu.Lock()
		c := h.child
		var exited <-chan struct{}
		if c != nil && !h.updating && !h.blocked {
			exited = c.done
		}
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			h.mu.Lock()
			if h.cancelUpdate != nil {
				h.cancelUpdate()
			}
			pending := h.updateDone
			h.mu.Unlock()
			if pending != nil {
				select {
				case <-pending:
				case <-time.After(o.DrainTimeout + o.StopGrace + o.KillWait):
					return errors.New("update coordinator did not stop")
				}
			}
			h.mu.Lock()
			c = h.child
			h.mu.Unlock()
			if c != nil {
				select {
				case <-c.done:
					return nil // A crash is not an unconfirmed update handoff.
				default:
				}
				id := "shutdown-" + domain.NewID()
				err := h.shutdown(c, id, o.DrainTimeout)
				if err != nil {
					record := Operation{Input: domain.WorkerReplacement{ID: id, ExpectedBuildID: c.build.BuildID, BuildID: c.build.BuildID, Policy: "immediate", Explicit: true}, Phase: "blocked", Instance: h.instance, DeviceID: h.device, SourcePID: c.cmd.Process.Pid, SourceInstance: c.instance, PreviousBuild: c.build.BuildID, Error: err.Error(), Created: time.Now().UTC()}
					h.mu.Lock()
					_ = h.journal.save(record)
					h.mu.Unlock()
				}
				return err
			}
			return nil
		case <-h.wake:
		case <-exited:
			h.mu.Lock()
			// A source drained by a newly accepted update is an expected exit,
			// even if this select began before that update was accepted.
			if h.child != c || h.updating || h.blocked {
				h.mu.Unlock()
				continue
			}
			h.readyDone = false
			attempt, exhausted := h.retry.failed(time.Now(), c.startedAt)
			h.health.Phase = "restarting"
			if exhausted {
				h.health.Phase = "blocked"
				h.health.Error = "执行服务在 5 分钟内异常退出 10 次，已停止自动重启"
				h.health.CrashLoop = true
				h.blocked = true
			}
			err = h.saveHealth()
			h.mu.Unlock()
			if err != nil {
				_ = h.blockHealth(err)
				continue
			}
			if exhausted {
				continue
			}
			timer := time.NewTimer(crashRetryDelay(attempt, o.RetryMin, o.RetryMax, 0))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			// No unknown prompt is replayed; the worker reconciles accepted records.
			if err = h.startSelectedRetry(o.InitialPath, false, ""); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				_ = h.blockStartup(err)
				continue
			}
			h.mu.Lock()
			h.readyDone = true
			h.health.Phase, h.health.Error = "ready", ""
			err = h.saveHealth()
			h.mu.Unlock()
			if err != nil {
				_ = h.blockHealth(err)
			}
		}
	}
}
func (h *host) signalWake() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *host) serve(l *net.UnixListener) {
	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Minute))
			if localPrincipal(conn) != nil {
				return
			}
			var req Request
			if err := json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&req); err != nil {
				return
			}
			out, err := h.control(req)
			reply := response{Operation: out}
			if err != nil {
				reply.Error = err.Error()
				var remote *protocol.RemoteError
				if errors.As(err, &remote) {
					reply.Code = remote.Code
				} else {
					reply.Code = "receiptUnknown"
				}
			}
			_ = json.NewEncoder(conn).Encode(reply)
		}()
	}
}
func (h *host) control(req Request) (Operation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch req.Action {
	case "status":
		var op Operation
		var err error
		if req.ID == "" {
			op, err = h.journal.current()
		} else {
			op, err = h.journal.load(req.ID)
		}
		if err == nil && op.Input.ID == "" {
			op.DeviceID = h.device
			op.Phase = "starting"
			if h.readyDone && !h.blocked {
				op.Phase = "ready"
			}
			if h.blocked {
				op.Phase = "blocked"
			}
			if h.child != nil {
				op.SourcePID = h.child.cmd.Process.Pid
			}
		}
		if err == nil && req.ID == "" && !h.updating && h.health.Phase == "blocked" {
			op.Phase, op.Error = "blocked", h.health.Error
			if op.DeviceID == "" {
				op.DeviceID = h.health.DeviceID
			}
		}
		// Applied is persisted before resume, but is published as complete only
		// after the captured candidate actually opens acceptance.
		if err == nil && h.updateID == op.Input.ID && h.updateID != "" && (!h.readyDone || h.updating) && (op.Phase == "applied" || op.Phase == "rolled-back") {
			op.Phase = "observing"
		}
		if err == nil && req.ID == "" && !h.updating && h.health.Phase == "reconnecting" {
			op.Phase, op.Error = h.health.Phase, h.health.Error
		}
		return op, err
	case "repair":
		return h.beginRepair(req)
	case "cancel":
		o, err := h.journal.load(req.ID)
		if err != nil {
			return o, err
		}
		if req.DeviceID != "" && req.DeviceID != o.DeviceID {
			return o, &protocol.RemoteError{Code: "notSent", Message: "更新不属于所选设备"}
		}
		if o.Terminal() {
			return o, nil
		}
		if o.Phase != "preparing" && o.Phase != "waiting" {
			return o, errors.New("切换已经开始，不能取消")
		}
		if h.cancelUpdate != nil {
			h.cancelUpdate()
		}
		return o, nil
	case "start":
		if h.ctx.Err() != nil {
			return Operation{}, &protocol.RemoteError{Code: "notSent", Message: "服务控制正在关闭"}
		}
		if h.blocked {
			return Operation{}, &protocol.RemoteError{Code: "notSent", Message: "服务切换未确认；保留原数据，请使用可信兼容安装器修复"}
		}
		if req.Input.Policy == "immediate" && !req.Input.Explicit {
			return Operation{}, &protocol.RemoteError{Code: "notSent", Message: "立即更新需要明确确认"}
		}
		if prior, err := h.journal.load(req.Input.ID); err == nil {
			if prior.Input != req.Input {
				return prior, &protocol.RemoteError{Code: "notSent", Message: "更新标识与原目标不同"}
			}
			return prior, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return prior, err
		}
		if active, e := h.journal.current(); e == nil && active.Input.ID != "" && active.Input.ExpectedBuildID == req.Input.ExpectedBuildID && active.Input.BuildID == req.Input.BuildID && active.Input.Policy == req.Input.Policy && (!active.Terminal() || active.Phase == "applied") {
			if h.updating && (active.Phase == "applied" || active.Phase == "rolled-back") {
				active.Phase = "observing"
			}
			return active, nil
		}
		if h.updating || !h.readyDone || h.child == nil || h.device == "" {
			return Operation{}, &protocol.RemoteError{Code: "notSent", Message: "服务尚未就绪或已有不同更新"}
		}
		if h.child.build.BuildID != req.Input.ExpectedBuildID || req.Input.BuildID == req.Input.ExpectedBuildID {
			return Operation{}, &protocol.RemoteError{Code: "notSent", Message: "执行服务版本已变化，请重新检查"}
		}
		o, err := h.journal.accept(req.Input, h.instance, h.device)
		if err != nil {
			return o, err
		}
		o.SourceInstance = h.child.instance
		o.SourcePID = h.child.cmd.Process.Pid
		if err = h.journal.save(o); err != nil {
			return o, err
		}
		h.updating = true
		h.updateID = o.Input.ID
		ctx, cancel := context.WithCancel(h.ctx)
		h.cancelUpdate = cancel
		old := h.child
		done := make(chan struct{})
		h.updateDone = done
		go func() { defer close(done); h.update(ctx, o, old) }()
		h.signalWake()
		return o, nil
	default:
		return Operation{}, errors.New("unknown supervisor command")
	}
}
func (h *host) workerCall(ctx context.Context, c *child, method string, in, out any) error {
	client, err := protocol.Dial(ctx, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(h.options.Directory, "worker.sock"))
	}, "")
	if err != nil {
		return err
	}
	defer client.Close()
	if client.Worker == nil || client.Worker.BuildID != c.build.BuildID {
		return errors.New("worker build differs from captured child")
	}
	return client.Call(ctx, method, in, out)
}
func (h *host) launch(path string, b domain.WorkerBuild, paused bool, op string) (*child, error) {
	c := &child{instance: domain.NewID(), build: b, done: make(chan struct{}), startedAt: time.Now()}
	args := append([]string{}, h.options.Arguments...)
	args = append(args, "--service-child")
	c.cmd = exec.Command(path, args...)
	c.cmd.Env = append(os.Environ(), "SHIOJI_WORKER_INSTANCE="+c.instance, "SHIOJI_SUPERVISOR_OPERATION="+op, fmt.Sprintf("SHIOJI_WORKER_PAUSED=%t", paused))
	c.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.cmd.Stdout = os.Stdout
	c.cmd.Stderr = os.Stderr
	if err := c.cmd.Start(); err != nil {
		return nil, err
	}
	list, err := managedexec.Processes()
	if err == nil {
		for _, p := range list {
			if p.Identity.PID == c.cmd.Process.Pid {
				c.identity = p
				break
			}
		}
	}
	go func() { c.err = c.cmd.Wait(); close(c.done) }()
	h.mu.Lock()
	h.child = c
	h.mu.Unlock()
	if c.identity.Identity.PID == 0 {
		return c, errors.New("started worker identity could not be observed")
	}
	return c, nil
}
func (h *host) ready(ctx context.Context, c *child, paused bool) (Ready, error) {
	var out Ready
	wait, cancel := context.WithTimeout(ctx, h.options.ReadyTimeout)
	defer cancel()
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	for {
		call, stop := context.WithTimeout(wait, time.Second)
		err := h.workerCall(call, c, "worker.local-ready", Control{Instance: c.instance}, &out)
		stop()
		if err == nil && out.PID == c.cmd.Process.Pid && out.Instance == c.instance && out.Build.BuildID == c.build.BuildID && out.DeviceID != "" && out.Paused == paused && managedexec.ProcessState(c.identity) == "running" {
			return out, nil
		}
		select {
		case <-c.done:
			return out, errors.New("candidate exited before local ready")
		case <-wait.Done():
			return out, wait.Err()
		case <-tick.C:
		}
	}
}
func (h *host) startSelected(initial string, paused bool, op string) error {
	prepare, cancel := context.WithTimeout(h.ctx, 8*time.Minute)
	path, b, err := h.options.Verify(prepare, "")
	cancel()
	if err != nil {
		return err
	}
	if path == "" {
		path = initial
	}
	c, err := h.launch(path, b, paused, op)
	if err != nil {
		if c != nil {
			if stop := h.stopExact(c, h.options.StopGrace); stop != nil {
				return PermanentStartup(errors.Join(err, stop))
			}
		}
		if c != nil {
			err = errors.Join(err, c.err)
		}
		return err
	}
	ready, err := h.ready(h.ctx, c, paused)
	if err != nil {
		if stop := h.stopExact(c, h.options.StopGrace); stop != nil {
			return PermanentStartup(errors.Join(err, stop))
		}
		return errors.Join(err, c.err)
	}
	h.mu.Lock()
	h.child = c
	h.device = ready.DeviceID
	h.health.DeviceID, h.health.BuildID, h.health.Schema = ready.DeviceID, b.BuildID, b.Schema
	h.mu.Unlock()
	return nil
}
func (h *host) phase(o *Operation, phase string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	o.Phase = phase
	return h.journal.save(*o)
}
func (h *host) stopExact(c *child, grace time.Duration) error {
	select {
	case <-c.done:
		return nil
	default:
	}
	if managedexec.ProcessState(c.identity) == "running" {
		_ = managedexec.SignalExact(c.identity, unix.SIGTERM)
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-c.done:
		return nil
	case <-timer.C:
	}
	if managedexec.ProcessState(c.identity) == "running" {
		_ = managedexec.SignalExact(c.identity, unix.SIGKILL)
	}
	timer.Reset(h.options.KillWait)
	select {
	case <-c.done:
		return nil
	case <-timer.C:
		return errors.New("captured worker did not exit after KILL")
	}
}

var errDrainAdmission = errors.New("business became active before drain")

func (h *host) shutdown(c *child, id string, grace time.Duration, replacement ...domain.WorkerReplacement) error {
	policy := domain.WorkerReplacement{Policy: "immediate", Explicit: true}
	if len(replacement) > 0 {
		policy = replacement[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	var ignored domain.WorkerUpdateResult
	err := h.workerCall(ctx, c, "worker.drain", Control{Instance: c.instance, OperationID: id, Replacement: policy}, &ignored)
	if err == nil && !ignored.Ready {
		return errDrainAdmission
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		if err := h.stopExact(c, 0); err != nil {
			return err
		}
	}
	var drained Drained
	if err := readJSON(drainPath(h.options.Directory, id, c.instance), &drained); err != nil {
		return fmt.Errorf("worker exited without a drained receipt: %w", err)
	}
	if drained.PID != c.cmd.Process.Pid || drained.Instance != c.instance || drained.OperationID != id || drained.BuildID != c.build.BuildID || drained.DeviceID != h.device {
		return errors.New("drained receipt does not identify captured writer")
	}
	return nil
}
func (h *host) update(ctx context.Context, o Operation, old *child) {
	var candidate *child
	failed := func(cause error, blocked bool) {
		o.Error = cause.Error()
		phase := "failed"
		if blocked {
			phase = "blocked"
		}
		_ = h.phase(&o, phase)
		h.mu.Lock()
		h.blocked = blocked
		h.updating = false
		h.cancelUpdate = nil
		h.updateID = ""
		h.mu.Unlock()
		h.signalWake()
	}
	prepare, cancel := context.WithTimeout(ctx, 8*time.Minute)
	path, b, err := h.options.Verify(prepare, o.Input.BuildID)
	if err == nil && b.Schema != old.build.Schema {
		err = errors.New("候选存储格式不兼容；需使用明确的数据迁移流程")
	}
	if err == nil {
		err = probeCandidate(prepare, h.options.Directory, path, b)
	}
	if err == nil {
		var backup domain.WorkerUpdateResult
		err = h.workerCall(prepare, old, "worker.local-backup", Control{Instance: old.instance, OperationID: o.Input.ID}, &backup)
	}
	cancel()
	if err != nil {
		failed(err, false)
		return
	}
	if err = h.phase(&o, "waiting"); err != nil {
		failed(err, false)
		return
	}
	expires := time.NewTimer(24 * time.Hour)
	defer expires.Stop()
waitForIdle:
	for {
		call, stop := context.WithTimeout(ctx, 10*time.Second)
		var ready domain.WorkerUpdateResult
		err = h.workerCall(call, old, "worker.prepare-drain", Control{Instance: old.instance, OperationID: o.Input.ID, Replacement: o.Input}, &ready)
		stop()
		if err == nil && ready.Ready {
			break
		}
		select {
		case <-old.done:
			failed(errors.New("old worker exited before drain acceptance"), false)
			return
		case <-ctx.Done():
			failed(ctx.Err(), false)
			return
		case <-expires.C:
			failed(errors.New("prepared update expired"), false)
			return
		case <-time.After(time.Second):
		}
	}
	h.mu.Lock()
	err = ctx.Err()
	if err == nil {
		o.Phase = "switching"
		err = h.journal.save(o)
	}
	h.mu.Unlock()
	if err != nil {
		failed(err, true)
		return
	}
	if err = h.shutdown(old, o.Input.ID, h.options.DrainTimeout, o.Input); err != nil {
		if errors.Is(err, errDrainAdmission) {
			if err = h.phase(&o, "waiting"); err != nil {
				failed(err, true)
				return
			}
			goto waitForIdle
		}
		failed(err, true)
		return
	}
	if err = h.phase(&o, "observing"); err != nil {
		failed(err, true)
		return
	}
	candidate, err = h.launch(path, b, true, o.Input.ID)
	if err == nil {
		var ready Ready
		ready, err = h.ready(ctx, candidate, true)
		if err == nil && ready.DeviceID != o.DeviceID {
			err = errors.New("candidate device identity changed")
		}
	}
	if err != nil {
		if candidate != nil {
			if e := h.shutdown(candidate, o.Input.ID+"-candidate", h.options.StopGrace); e != nil {
				failed(errors.Join(err, e), true)
				return
			}
		}
		if e := h.phase(&o, "rollback-stop"); e != nil {
			failed(e, true)
			return
		}
		previous, build, e := h.options.Verify(ctx, o.PreviousBuild)
		if e != nil {
			failed(e, true)
			return
		}
		restored, e := h.launch(previous, build, true, o.Input.ID)
		if e != nil {
			failed(e, true)
			return
		}
		ready, e := h.ready(ctx, restored, true)
		if e != nil || ready.DeviceID != o.DeviceID {
			failed(errors.New("compatible rollback failed local ready"), true)
			return
		}
		h.mu.Lock()
		h.child = restored
		h.mu.Unlock()
		if e = h.options.Select(o.PreviousBuild); e == nil {
			e = h.phase(&o, "rolled-back")
		}
		if e == nil {
			e = h.resume(ctx, restored, o.Input.ID)
		}
		if e != nil {
			failed(e, true)
			return
		}
	} else {
		if err = h.options.Select(b.BuildID); err == nil {
			err = h.phase(&o, "applied")
		}
		if err != nil {
			failed(err, true)
			return
		}
		h.mu.Lock()
		h.child = candidate
		h.mu.Unlock()
		if err = h.resume(ctx, candidate, o.Input.ID); err != nil {
			failed(err, true)
			return
		}
	}
	h.mu.Lock()
	h.updating = false
	h.updateID = ""
	h.cancelUpdate = nil
	h.readyDone = true
	h.health = serviceHealth{Phase: "ready", DeviceID: h.device, BuildID: h.child.build.BuildID, Schema: h.child.build.Schema}
	h.retry = crashRetry{}
	err = h.saveHealth()
	h.mu.Unlock()
	if err != nil {
		_ = h.blockHealth(err)
	}
	h.signalWake()
}
func (h *host) resume(ctx context.Context, c *child, id string) error {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return h.workerCall(call, c, "worker.resume", Control{Instance: c.instance, OperationID: id}, nil)
}
