package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/store"
	"shioji/internal/supervisor"
	"shioji/internal/transport"
	"sync"
	"syscall"
	"time"
)

// StartLegacyUpgrade is the explicit installer relay for an older worker. The
// signed detached helper keeps only the control lease, and becomes the fixed
// entry after it has released the old business writer and registered tools.
func StartLegacyUpgrade(ctx context.Context, directory, executable, device string, in domain.WorkerReplacement) (supervisor.Operation, error) {
	if in.ID == "" || device == "" || in.BuildID == "" || in.Policy != "when-idle" && in.Policy != "immediate" || in.Policy == "immediate" && !in.Explicit {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: "旧服务更新意图不完整"}
	}
	if _, _, _, err := recoveryCatalog(ctx, directory, device); err != nil {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	lease, err := supervisor.AcquireLease(directory)
	if err != nil {
		if active, e := supervisor.Query(ctx, directory, ""); e == nil && active.DeviceID == device && active.Input.ExpectedBuildID == in.ExpectedBuildID && active.Input.BuildID == in.BuildID && active.Input.Policy == in.Policy {
			return active, nil
		}
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: err.Error()}
	}
	defer lease.Close()
	op, err := supervisor.AcceptLegacy(directory, in, device)
	if err != nil {
		return op, err
	}
	if op.Terminal() {
		return op, nil
	}
	log, err := os.OpenFile(filepath.Join(directory, "supervisor", "legacy-install.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		op.Phase = "failed"
		op.Error = err.Error()
		_ = supervisor.SaveLegacy(directory, op)
		return op, err
	}
	defer log.Close()
	cmd := exec.Command(executable, "--state-dir", directory, "--service-migrate", "run", "--service-id", in.ID, "--service-device", device)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{lease}
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		op.Phase = "failed"
		op.Error = err.Error()
		_ = supervisor.SaveLegacy(directory, op)
		return op, err
	}
	_ = cmd.Process.Release()
	return op, nil
}

func legacyEndpoint(ctx context.Context, directory, device, build string) (managedexec.ProcessInfo, *domain.WorkerBuild, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "worker.sock"))
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return managedexec.ProcessInfo{}, nil, nil
	}
	if err != nil {
		return managedexec.ProcessInfo{}, nil, err
	}
	defer conn.Close()
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return managedexec.ProcessInfo{}, nil, err
	}
	var pid int
	var inner error
	if err = raw.Control(func(fd uintptr) { pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) }); err != nil {
		return managedexec.ProcessInfo{}, nil, err
	}
	if inner != nil {
		return managedexec.ProcessInfo{}, nil, inner
	}
	var owner managedexec.ProcessInfo
	procs, err := managedexec.Processes()
	if err != nil {
		return owner, nil, err
	}
	for _, p := range procs {
		if p.Identity.PID == pid {
			owner = p
			break
		}
	}
	if owner.Identity.PID == 0 || managedexec.ControlProcess(pid) {
		return owner, nil, errors.New("原执行服务的身份不能确认")
	}
	client, err := protocol.Connect(ctx, conn, device)
	if err != nil {
		return owner, nil, err
	}
	defer client.Close()
	if client.Worker == nil || build != "" && client.Worker.BuildID != build || managedexec.ProcessState(owner) != "running" {
		return owner, nil, errors.New("原执行服务版本或身份已改变")
	}
	return owner, client.Worker, nil
}

func RunLegacyUpgrade(ctx context.Context, directory, executable, id, device string, lease *os.File) (retErr error) {
	if lease == nil {
		return errors.New("缺少迁移控制锁")
	}
	actual, e := lease.Stat()
	expected, e2 := os.Lstat(filepath.Join(directory, "supervisor.lock"))
	if e != nil || e2 != nil || !os.SameFile(actual, expected) {
		return errors.New("迁移控制锁身份不匹配")
	}
	if e = unix.Flock(int(lease.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return e
	}
	unix.CloseOnExec(int(lease.Fd()))
	op, err := supervisor.LegacyOperation(directory, id)
	if err != nil {
		return err
	}
	if op.Terminal() {
		return nil
	}
	if !op.Legacy || op.Input.ID != id || op.DeviceID != device {
		return errors.New("旧服务安装回执不属于当前设备")
	}
	failure := func(err error, blocked bool) error {
		op.Phase = "failed"
		if blocked {
			op.Phase = "blocked"
		}
		op.Error = err.Error()
		return errors.Join(err, supervisor.SaveLegacy(directory, op))
	}
	prepare, cancel := context.WithTimeout(ctx, 8*time.Minute)
	candidate, err := transport.StageWorker(prepare, directory, executable)
	if err == nil && candidate.BuildID != op.Input.BuildID {
		err = errors.New("安装程序与接纳的候选构建不一致")
	}
	if err == nil {
		dir, e := os.MkdirTemp(directory, ".legacy-probe-")
		if e != nil {
			err = e
		} else {
			command := exec.CommandContext(prepare, executable, "--probe", "--state-dir", dir)
			command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir}
			var data []byte
			data, err = command.Output()
			if err == nil {
				var result domain.WorkerBuild
				if json.Unmarshal(data, &result) != nil || result.BuildID != candidate.BuildID {
					err = errors.New("候选隔离探测未通过")
				}
			}
			os.RemoveAll(dir)
		}
	}
	cancel()
	if err != nil {
		return failure(err, false)
	}
	op.Phase = "waiting"
	if err = supervisor.SaveLegacy(directory, op); err != nil {
		return err
	}
	expires := time.NewTimer(24 * time.Hour)
	defer expires.Stop()
	for {
		if supervisor.LegacyCancelled(directory, id) {
			return failure(errors.New("更新已取消；原服务保持运行"), false)
		}
		ready, e := store.ReadWorkerStopReady(ctx, directory, device)
		if e != nil {
			return failure(fmt.Errorf("无法核验旧服务的业务状态，原件已保留：%w", e), false)
		}
		if e == nil && (ready.Ready || op.Input.Policy == "immediate") {
			break
		}
		select {
		case <-ctx.Done():
			return failure(ctx.Err(), false)
		case <-expires.C:
			return failure(errors.New("准备的旧服务更新已过期"), false)
		case <-time.After(time.Second):
		}
	}
	call, stop := context.WithTimeout(ctx, 5*time.Second)
	source, build, err := legacyEndpoint(call, directory, device, op.Input.ExpectedBuildID)
	stop()
	if err != nil {
		return failure(err, false)
	}
	if build != nil && build.Lifecycle == 1 {
		return failure(errors.New("设备已经使用固定服务入口，请重新查询原更新"), false)
	}
	previous, err := transport.ActiveWorkerBuild(directory)
	if err != nil {
		return failure(err, false)
	}
	op.SourcePID = source.Identity.PID
	op.SourceInstance = "legacy-" + domain.NewID()
	op.Phase = "switching"
	if err = supervisor.SaveLegacy(directory, op); err != nil {
		return err
	}
	if supervisor.LegacyCancelled(directory, id) {
		return failure(errors.New("更新已取消；原服务保持运行"), false)
	}
	// Prevent an existing launchd entry from restarting an obsolete writer. Its
	// next invocation runs this signed fixed entry and waits on our control lease.
	if err = transport.SaveActiveWorker(directory, candidate.BuildID); err != nil {
		return failure(err, false)
	}
	if source.Identity.PID > 1 {
		signalErr := managedexec.SignalExact(source, unix.SIGTERM)
		if signalErr != nil && managedexec.ProcessState(source) == "running" {
			if previous != "" {
				_ = transport.SaveActiveWorker(directory, previous)
			}
			return failure(signalErr, false)
		}
		deadline := time.Now().Add(30 * time.Second)
		for managedexec.ProcessState(source) == "running" && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return failure(ctx.Err(), true)
			case <-time.After(20 * time.Millisecond):
			}
		}
		if managedexec.ProcessState(source) == "running" {
			_ = managedexec.SignalExact(source, unix.SIGKILL)
		}
		deadline = time.Now().Add(5 * time.Second)
		for managedexec.ProcessState(source) == "running" && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if state := managedexec.ProcessState(source); state != "exited" && state != "changed" {
			return failure(errors.New("原服务尚未确认退出，保留原件后重试检查或使用兼容安装器修复"), true)
		}
	}
	var owner *Owner
	wait, cancelOwner := context.WithTimeout(ctx, 10*time.Second)
	defer cancelOwner()
	for {
		owner, err = Acquire(directory)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrOwned) {
			return failure(err, true)
		}
		select {
		case <-wait.Done():
			return failure(errors.New("原业务写入者仍持有锁，请稍后检查或使用兼容安装器修复"), true)
		case <-time.After(30 * time.Millisecond):
		}
	}
	defer func() {
		if owner != nil {
			owner.Close()
		}
	}()
	if _, _, _, err = recoveryCatalog(ctx, directory, device); err != nil {
		return failure(err, true)
	}
	// All known groups are cleanup targets; no process-name or complete OS-tree
	// inventory participates in migration admission.
	if err = retireLegacyRegistered(ctx, directory, executable, id); err != nil {
		return failure(err, true)
	}
	if err = archiveLegacyRecoveryFence(directory); err != nil {
		return failure(err, true)
	}
	db, err := store.Open(filepath.Join(directory, "state.sqlite"))
	if err != nil {
		return failure(err, true)
	}
	backup := filepath.Join(directory, "update-backup-"+id+".sqlite")
	if _, err = db.BackupWorker(backup); err != nil {
		db.Close()
		return failure(err, true)
	}
	intent := supervisor.DrainIntent{ID: id, Instance: op.SourceInstance, DeviceID: device, BuildID: op.Input.ExpectedBuildID}
	snapshot, err := db.Snapshot()
	if err == nil {
		for _, session := range snapshot.Sessions {
			q, e := db.Queue(session.ID)
			if e != nil {
				err = e
				break
			}
			if !q.Paused {
				intent.Queues = append(intent.Queues, session.ID)
			}
		}
	}
	if err == nil {
		err = supervisor.SaveDrainIntent(directory, intent)
	}
	if err == nil {
		err = db.MigrateLegacyMaintenance(id)
	}
	if err == nil {
		err = db.Recover()
	}
	err = errors.Join(err, db.Close())
	if err != nil {
		return failure(err, true)
	}
	if err = owner.Close(); err != nil {
		return failure(err, true)
	}
	owner = nil
	op.Phase = "observing"
	if err = supervisor.SaveLegacy(directory, op); err != nil {
		return err
	}
	return transport.RunWorkerSupervisor(ctx, directory, executable, transport.ExistingCodex(), false, &supervisor.LegacyInstall{Operation: op, Lease: lease})
}

func retireLegacyRegistered(ctx context.Context, directory, executable, operation string) error {
	work, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	var mu sync.Mutex
	var result error
	for _, namespace := range []string{"executions", "title-executions"} {
		root := filepath.Join(directory, namespace)
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		manager, err := managedexec.New(root, executable)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			wait.Add(1)
			go func(id string, m *managedexec.Manager) {
				defer wait.Done()
				scope, e := m.Existing(id)
				if e == nil && scope != nil {
					e = scope.AuthorizeRetirement(operation)
					if e == nil {
						e = scope.Terminate(work, true)
					}
				}
				mu.Lock()
				result = errors.Join(result, e)
				mu.Unlock()
			}(entry.Name(), manager)
		}
	}
	wait.Wait()
	if result != nil {
		return fmt.Errorf("旧运行环境的登记范围尚未确认退出：%w", result)
	}
	return nil
}
