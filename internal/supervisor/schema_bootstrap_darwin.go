package supervisor

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
)

func acquireSchemaBootstrap(directory string) (*os.File, error) {
	fd, err := unix.Open(filepath.Join(directory, "schema-bootstrap.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	lease := os.NewFile(uintptr(fd), "schema bootstrap lease")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lease.Close()
		return nil, ErrOwned
	}
	return lease, nil
}

func checkSchemaBootstrapStatus(directory string, op Operation) Operation {
	if !op.Legacy || op.SourceSchema == 0 && !op.Input.RuntimeHandoff || op.Terminal() {
		return op
	}
	j := journal{filepath.Join(directory, "supervisor")}
	if current, err := j.current(); err != nil || current.Input.ID == op.Input.ID {
		return op
	}
	if lease, err := acquireSchemaBootstrap(directory); err == nil {
		defer lease.Close()
		// The installer publishes the current pointer before releasing this lock.
		if current, err := j.current(); err == nil && current.Input.ID != op.Input.ID {
			op.Phase, op.Error = "failed", "数据库迁移助手中断，请重新检查设备后重试"
		}
	}
	return op
}

// StartRuntimeMigration uses the existing drained-writer bootstrap for a
// same-schema fixed host whose macOS responsibility still belongs to SSH.
func StartRuntimeMigration(ctx context.Context, directory, device string, in domain.WorkerReplacement, candidate domain.WorkerBuild, start func(*os.File) error) (Operation, error) {
	refuse := func(reason string) (Operation, error) {
		return Operation{}, &protocol.RemoteError{Code: "notSent", Message: reason}
	}
	if !safeID(in.ID) || device == "" || !in.RuntimeHandoff || in.SchemaMigration ||
		in.ExpectedBuildID == "" || in.BuildID != candidate.BuildID || candidate.RuntimeHost < 1 ||
		in.Policy != "when-idle" && (in.Policy != "immediate" || !in.Explicit) {
		return refuse("执行 Runtime 接管意图或候选不匹配")
	}
	j := journal{filepath.Join(directory, "supervisor")}
	if prior, err := j.load(in.ID); err == nil {
		if prior.Input != in || prior.DeviceID != device {
			return refuse("接管标识已绑定其他更新")
		}
		return prior, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Operation{}, err
	}
	if schema, err := schemaAt(ctx, directory, device); err != nil || schema != candidate.Schema {
		return refuse("Runtime 接管不能改变设备数据库版本")
	}
	lease, err := acquireSchemaBootstrap(directory)
	if err != nil {
		return refuse("该设备已有执行服务接管正在准备")
	}
	defer lease.Close()
	if prior, e := j.load(in.ID); e == nil {
		if prior.Input != in || prior.DeviceID != device {
			return refuse("接管标识已绑定其他更新")
		}
		return prior, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return Operation{}, e
	}
	now := time.Now().UTC()
	op := Operation{Legacy: true, Input: in, Phase: "preparing", DeviceID: device, PreviousBuild: in.ExpectedBuildID,
		TargetSchema: candidate.Schema, Created: now, ExpiresAt: now.Add(24 * time.Hour)}
	if err := j.saveRecord(op); err != nil {
		return op, err
	}
	if err := start(lease); err != nil {
		op.Phase, op.Error = "failed", err.Error()
		return op, errors.Join(err, j.saveRecord(op))
	}
	return op, nil
}

func StartSchemaMigration(ctx context.Context, directory, device string, in domain.WorkerReplacement, candidate domain.WorkerBuild, start func(*os.File) error) (Operation, error) {
	refuse := func(reason string) (Operation, error) {
		return Operation{}, &protocol.RemoteError{Code: "notSent", Message: reason}
	}
	if !safeID(in.ID) || len(in.ID) > 110 || device == "" || !in.Explicit || !in.SchemaMigration || in.Policy != "when-idle" ||
		in.ExpectedBuildID == "" || in.ExpectedBuildID == in.BuildID || in.BuildID != candidate.BuildID {
		return refuse("数据库迁移授权或目标不匹配")
	}
	j := journal{filepath.Join(directory, "supervisor")}
	if existing, err := j.load(in.ID); err == nil {
		if existing.Input != in || existing.DeviceID != device {
			return refuse("迁移标识已绑定其他更新")
		}
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Operation{}, err
	}
	probe, err := Call(ctx, directory, Request{Action: "schema-capability"})
	if err == nil {
		if probe.DeviceID != device || probe.Phase != "schema-capable" {
			return refuse("固定服务的设备身份未确认")
		}
		if !in.RuntimeHandoff {
			return Call(ctx, directory, Request{Action: "start", DeviceID: device, Input: in})
		}
	} else {
		var remote *protocol.RemoteError
		if !errors.As(err, &remote) || remote.Message != "unknown supervisor command" {
			return refuse("无法确认当前固定执行服务，连接后可重新检查")
		}
	}
	schema, err := schemaAt(ctx, directory, device)
	if err != nil || !candidate.MigratesSchema(schema) {
		return refuse("候选版本未声明支持实际数据库的迁移")
	}
	if err := schemaMigrationSpace(directory); err != nil {
		return refuse(err.Error())
	}
	lease, err := acquireSchemaBootstrap(directory)
	if err != nil {
		return refuse("该设备已有数据库迁移正在准备")
	}
	defer lease.Close()
	if prior, e := j.load(in.ID); e == nil && prior.Input == in && prior.DeviceID == device {
		return prior, nil
	}
	now := time.Now().UTC()
	op := Operation{Legacy: true, Input: in, Phase: "preparing", DeviceID: device, PreviousBuild: in.ExpectedBuildID,
		SourceSchema: schema, TargetSchema: candidate.Schema, Created: now, ExpiresAt: now.Add(24 * time.Hour)}
	if err = saveSchemaPlan(directory, op); err != nil {
		return Operation{}, err
	}
	if err = j.saveRecord(op); err != nil {
		return op, err
	}
	if err = start(lease); err != nil {
		op.Phase, op.Error = "failed", err.Error()
		err = errors.Join(err, j.saveRecord(op))
	}
	return op, err
}

func RunSchemaBootstrap(ctx context.Context, directory, id, device, path string, candidate domain.WorkerBuild, guard *os.File, install func(LegacyInstall, bool) error) error {
	if guard == nil {
		return errors.New("缺少数据库迁移锁")
	}
	defer guard.Close()
	actual, err := guard.Stat()
	expected, statErr := os.Lstat(filepath.Join(directory, "schema-bootstrap.lock"))
	if err != nil || statErr != nil || !os.SameFile(actual, expected) ||
		unix.Flock(int(guard.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return errors.New("数据库迁移锁身份不匹配")
	}
	unix.CloseOnExec(int(guard.Fd()))
	j := journal{filepath.Join(directory, "supervisor")}
	op, err := j.load(id)
	if err != nil {
		return err
	}
	if op.Phase != "preparing" || !op.Legacy || op.DeviceID != device || op.Input.BuildID != candidate.BuildID ||
		candidate.Schema != op.TargetSchema ||
		op.Input.SchemaMigration && (!op.Input.Explicit || !candidate.MigratesSchema(op.SourceSchema)) ||
		!op.Input.SchemaMigration && (!op.Input.RuntimeHandoff || op.SourceSchema != 0 || candidate.RuntimeHost < 1) {
		return errors.New("迁移回执的设备、构建或存储版本未确认")
	}
	fail := func(cause error, blocked bool) error {
		op.Phase, op.Error = "failed", cause.Error()
		if blocked {
			op.Phase = "blocked"
		}
		return errors.Join(cause, j.saveRecord(op))
	}
	ctx, cancel := context.WithDeadline(ctx, op.ExpiresAt)
	defer cancel()
	preflight, stop := context.WithTimeout(ctx, 8*time.Minute)
	if err = probeCandidate(preflight, directory, path, candidate); err != nil {
		stop()
		return fail(err, false)
	}
	// The old host rejects a schema-changing candidate before drain and writes
	// the exact source instance in its receipt. Capture that identity using its
	// normal RPC without relying on process names, argv, or environment.
	probeInput := op.Input
	probeInput.ID += "-inspect"
	probeInput.SchemaMigration = false
	probeInput.RuntimeHandoff = false
	var source Operation
	if op.Input.SchemaMigration {
		source, err = captureSchemaSource(preflight, directory, op.DeviceID, probeInput)
	} else {
		source, err = captureRuntimeSource(preflight, directory, op)
	}
	stop()
	if err != nil {
		return fail(err, false)
	}

	op.SourcePID, op.SourceInstance, op.Instance = source.SourcePID, source.SourceInstance, source.Instance
	op.Phase = "waiting"
	if err = j.saveRecord(op); err != nil {
		return err
	}
	var fixed, writer managedexec.ProcessInfo
	var managed bool
	for {
		if LegacyCancelled(directory, op.Input.ID) {
			return fail(errors.New("数据库迁移已取消"), false)
		}
		if err = ctx.Err(); err != nil {
			return fail(err, false)
		}
		call, end := context.WithTimeout(ctx, 5*time.Second)
		active, e := Call(call, directory, Request{Action: "status"})
		if e != nil || active.Input.ID != source.Input.ID || active.Phase != source.Phase || active.Instance != op.Instance {
			end()
			return fail(errors.New("原固定服务已有其他操作，请重新检查设备"), false)
		}
		client, w, h, e := captureSchemaPeers(call, directory, op)
		if e != nil {
			end()
			return fail(e, false)
		}
		managed, writer, fixed = client.Worker.Managed, w, h
		var result domain.WorkerUpdateResult
		in := Control{Instance: op.SourceInstance, OperationID: op.Input.ID, Replacement: op.Input, ExpiresAt: op.ExpiresAt}
		in.AdmitBefore = time.Now().Add(3 * time.Second)
		e = client.Call(call, "worker.drain", in, &result)
		client.Close()
		end()
		intent, intentErr := ReadDrainIntent(directory, op.Input.ID)
		accepted := intentErr == nil && intent.DeviceID == op.DeviceID && intent.Instance == op.SourceInstance && intent.BuildID == op.PreviousBuild
		if accepted {
			break
		}
		if e != nil || result.Ready || !errors.Is(intentErr, os.ErrNotExist) {
			return fail(errors.Join(errors.New("未能确认原 worker 的排空接纳"), e, intentErr), true)
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err(), false)
		case <-time.After(time.Second):
		}
	}

	op.Phase = "switching"
	if err = j.saveRecord(op); err != nil {
		return err
	}
	handoff, stop := context.WithTimeout(ctx, 45*time.Second)
	defer stop()
	if err = waitForSchemaDrain(handoff, directory, op, writer); err != nil {
		return fail(err, true)
	}
	writerLease, err := acquireDatabaseLease(directory)
	if err != nil {
		return fail(errors.New("原 writer 已被其他实例接手，请重新检查设备"), false)
	}
	defer writerLease.Close()
	active, err := Call(handoff, directory, Request{Action: "status"})
	if err != nil || active.Input.ID != source.Input.ID || active.Instance != op.Instance {
		return fail(errors.New("迁移排空后固定服务归属变化，请重新检查"), false)
	}
	// A restarting old host cannot acquire this writer lease. Only terminate
	// the captured parent of the drained child, then acquire its control lease.
	if err = managedexec.SignalExact(fixed, unix.SIGTERM); err != nil && managedexec.ProcessState(fixed) == "running" {
		return fail(err, true)
	}
	if err = waitForSchemaExit(handoff, fixed); err != nil {
		return fail(err, true)
	}
	fixedLease, err := AcquireLease(directory)
	if err != nil {
		return fail(errors.New("已有其他固定服务接管设备，请重新检查"), false)
	}
	defer fixedLease.Close()
	if err = j.save(op); err != nil {
		return err
	}
	if err = writerLease.Close(); err != nil {
		return fail(err, true)
	}
	guard.Close()
	if managed && op.Input.RuntimeHandoff && candidate.RuntimeHost >= domain.RuntimeHostRegistrationRefresh {
		op.ExecutionRegistration = RefreshExecutionRegistration
	}
	return install(LegacyInstall{Operation: op, Lease: fixedLease}, managed)
}

func captureRuntimeSource(ctx context.Context, directory string, op Operation) (Operation, error) {
	active, err := Call(ctx, directory, Request{Action: "source"})
	legacy := false
	var remote *protocol.RemoteError
	if errors.As(err, &remote) && remote.Message == "unknown supervisor command" {
		legacy = true
		active, err = Call(ctx, directory, Request{Action: "status"})
	}
	if err != nil {
		return active, err
	}
	if active.DeviceID != op.DeviceID || active.Phase != "ready" && active.Phase != "applied" && active.Phase != "rolled-back" && active.Phase != "failed" {
		return active, errors.New("原固定服务仍在切换或恢复，暂不接管")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "worker.sock"))
	if err != nil {
		return active, err
	}
	pid, err := schemaPeerPID(conn)
	if err != nil {
		conn.Close()
		return active, err
	}
	instance := active.SourceInstance
	if legacy {
		// Older fixed hosts do not expose their current child instance. Read
		// only Shioji's launch marker from the authenticated socket peer, never
		// log process arguments/environment, and confirm it through worker RPC.
		raw, e := unix.SysctlRaw("kern.procargs2", pid)
		if e != nil {
			conn.Close()
			return active, errors.New("无法读取旧执行服务的接管标识，原服务保持运行")
		}
		instance = sourceInstanceFromProcargs(raw)
	}
	if instance == "" {
		conn.Close()
		return active, errors.New("原执行服务缺少可核对的接管标识")
	}
	client, err := protocol.Connect(ctx, conn, op.DeviceID)
	if err != nil {
		conn.Close()
		return active, err
	}
	defer client.Close()
	var ready Ready
	if err := client.Call(ctx, "worker.local-ready", Control{Instance: instance}, &ready); err != nil {
		return active, err
	}
	if ready.Paused || ready.PID != pid || ready.Instance != instance || ready.DeviceID != op.DeviceID ||
		ready.Build.BuildID != op.PreviousBuild || ready.Build.Schema != op.TargetSchema {
		return active, errors.New("原执行服务的运行身份或数据库版本已变化")
	}
	active.SourcePID, active.SourceInstance = ready.PID, ready.Instance
	return active, nil
}

func sourceInstanceFromProcargs(raw []byte) string {
	if len(raw) < 4 {
		return ""
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	if argc < 1 || argc > len(raw) {
		return ""
	}
	raw = raw[4:]
	end := bytes.IndexByte(raw, 0) // executable path, before alignment padding
	if end < 0 {
		return ""
	}
	raw = bytes.TrimLeft(raw[end+1:], "\x00")
	for range argc {
		end = bytes.IndexByte(raw, 0)
		if end < 0 {
			return ""
		}
		raw = raw[end+1:]
	}
	const marker = "SHIOJI_WORKER_INSTANCE="
	for len(raw) > 0 {
		end = bytes.IndexByte(raw, 0)
		if end <= 0 {
			return ""
		}
		if bytes.HasPrefix(raw[:end], []byte(marker)) {
			return string(raw[len(marker):end])
		}
		raw = raw[end+1:]
	}
	return ""
}

func captureSchemaSource(ctx context.Context, directory, device string, in domain.WorkerReplacement) (Operation, error) {
	_, err := Call(ctx, directory, Request{Action: "start", Input: in})
	if err != nil {
		return Operation{}, err
	}
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	for {
		op, err := Call(ctx, directory, Request{Action: "status", ID: in.ID})
		if err != nil {
			return op, err
		}
		if op.Input != in || op.DeviceID != device || op.SourcePID <= 1 || op.SourceInstance == "" || op.Instance == "" {
			return op, errors.New("旧固定服务回执未确认实际 writer 身份")
		}
		if op.Phase == "failed" && op.Error == "候选存储格式不兼容；需使用明确的数据迁移流程" {
			return op, nil
		}
		if op.Phase != "preparing" {
			return op, errors.New("旧固定服务未返回预期的 schema 预检结果")
		}
		select {
		case <-ctx.Done():
			return op, ctx.Err()
		case <-tick.C:
		}
	}
}

func captureSchemaPeers(ctx context.Context, directory string, op Operation) (*protocol.Client, managedexec.ProcessInfo, managedexec.ProcessInfo, error) {
	var writer, fixed managedexec.ProcessInfo
	fixedConn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "supervisor.sock"))
	if err != nil {
		return nil, writer, fixed, err
	}
	fixedPID, err := schemaPeerPID(fixedConn)
	fixedConn.Close()
	if err != nil {
		return nil, writer, fixed, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "worker.sock"))
	if err != nil {
		return nil, writer, fixed, err
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	pid, err := schemaPeerPID(conn)
	if err != nil {
		return nil, writer, fixed, err
	}
	procs, err := managedexec.Processes()
	if err != nil {
		return nil, writer, fixed, err
	}
	for _, proc := range procs {
		switch proc.Identity.PID {
		case pid:
			writer = proc
		case fixedPID:
			fixed = proc
		}
	}
	if pid != op.SourcePID || fixed.Identity.PID <= 1 || writer.ParentPID != fixedPID || managedexec.ControlProcess(fixedPID) {
		err = errors.New("旧 writer 与固定服务的进程归属发生变化")
		return nil, writer, fixed, err
	}
	client, err := protocol.Connect(ctx, conn, op.DeviceID)
	expectedSchema := op.SourceSchema
	if expectedSchema == 0 {
		expectedSchema = op.TargetSchema
	}
	if err == nil && (client.Worker == nil || client.Worker.BuildID != op.PreviousBuild || client.Worker.Schema != expectedSchema) {
		err = errors.New("旧 writer 的构建或 schema 发生变化")
	}
	return client, writer, fixed, err
}

func schemaPeerPID(conn net.Conn) (int, error) {
	u := conn.(*net.UnixConn)
	if err := localPrincipal(u); err != nil {
		return 0, err
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var inner error
	err = raw.Control(func(fd uintptr) {
		pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	})
	return pid, errors.Join(err, inner)
}

func ReleaseInterruptedSchemaControl(ctx context.Context, directory string, interrupted Operation) error {
	current, interruptedOK := InterruptedSchemaControl(directory)
	if !interruptedOK || current.Input != interrupted.Input || current.DeviceID != interrupted.DeviceID {
		return errors.New("待恢复的数据库迁移记录已变化")
	}
	// This operation is only for old control already blocked at startup. The
	// running migration-capable host is left in charge of its own recovery.
	status, err := Call(ctx, directory, Request{Action: "status"})
	if err != nil {
		lease, e := AcquireLease(directory)
		if e != nil {
			return err
		}
		return lease.Close()
	}
	if !sameSchemaIntent(status.Input, current.Input) || status.DeviceID != current.DeviceID || status.Phase != "blocked" || status.Error != interruptedSwitchError {
		return errors.New("仍在核对数据库迁移，请稍后刷新")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(directory, "supervisor.sock"))
	if err != nil {
		return err
	}
	defer conn.Close()
	pid, err := schemaPeerPID(conn)
	if err != nil || managedexec.ControlProcess(pid) {
		return errors.New("待恢复的固定服务身份无法确认")
	}
	lease, err := acquireDatabaseLease(directory)
	if err != nil {
		return errors.New("原数据库仍有写入者，等待释放后可重试")
	}
	defer lease.Close()
	list, err := managedexec.Processes()
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.Identity.PID != pid {
			continue
		}
		latest, e := Call(ctx, directory, Request{Action: "status"})
		if e != nil || !sameSchemaIntent(latest.Input, current.Input) || latest.DeviceID != current.DeviceID ||
			latest.Phase != "blocked" || latest.Error != interruptedSwitchError {
			return errors.New("待恢复的固定服务状态已变化")
		}
		if e = managedexec.SignalExact(p, unix.SIGTERM); e != nil && managedexec.ProcessState(p) == "running" {
			return e
		}
		return waitForSchemaExit(ctx, p)
	}
	return errors.New("原固定服务身份已变化")
}

func waitForSchemaDrain(ctx context.Context, directory string, op Operation, writer managedexec.ProcessInfo) error {
	if err := waitForSchemaExit(ctx, writer); err != nil {
		return err
	}
	var receipt Drained
	if err := readJSON(drainPath(directory, op.Input.ID, op.SourceInstance), &receipt); err != nil {
		return err
	}
	if receipt.PID != op.SourcePID || receipt.Instance != op.SourceInstance || receipt.OperationID != op.Input.ID ||
		receipt.DeviceID != op.DeviceID || receipt.BuildID != op.PreviousBuild {
		return errors.New("旧 writer 未提供与本次迁移绑定的排空回执")
	}
	return nil
}

func waitForSchemaExit(ctx context.Context, process managedexec.ProcessInfo) error {
	tick := time.NewTicker(15 * time.Millisecond)
	defer tick.Stop()
	for {
		switch managedexec.ProcessState(process) {
		case "exited", "changed":
			return nil
		case "unobservable":
			return errors.New("无法确认待接管进程已退出")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
