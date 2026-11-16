package worker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/protocol"
	"shioji/internal/runtimebuild"
	"shioji/internal/store"
	"shioji/internal/transport"
)

type recoveryPreviewRecord struct {
	Preview domain.WorkerRecoveryPreview
	Owner   managedexec.ProcessInfo
	Targets map[string]managedexec.ProcessInfo
}
type deviceRecoveryRecord struct {
	Operation        domain.WorkerRecoveryOperation
	Input            domain.WorkerRecoveryInput
	Snapshot         recoveryPreviewRecord
	Runner           managedexec.ProcessInfo
	Sent             map[string]bool
	Cancelled        bool
	Service          string
	ServiceSuspended bool
	OwnerPaused      bool
}

func recoveryDirectory(dir string) (string, error) {
	st, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Getuid()) {
		return "", errors.New("恢复目录必须属于当前用户且权限为 0700")
	}
	root := filepath.Join(dir, "worker-recovery")
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	st, err = os.Lstat(root)
	if err != nil {
		return "", err
	}
	owner, ok = st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Getuid()) {
		return "", errors.New("恢复记录目录不安全")
	}
	return root, nil
}
func recoveryID(id string) bool {
	return id != "" && len(id) <= 80 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") == ""
}
func recoveryLock(dir string) (*os.File, error) {
	root, err := recoveryDirectory(dir)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(root, "record.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "recovery lock")
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func recoveryRead(dir, name string, out any) error {
	if !recoveryID(name) {
		return errors.New("恢复标识无效")
	}
	p := filepath.Join(dir, "worker-recovery", name+".json")
	st, err := os.Lstat(p)
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !ok || owner.Uid != uint32(os.Getuid()) || st.Size() > 8<<20 {
		return errors.New("恢复记录不安全")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func recoveryWrite(dir, name string, value any) error {
	if !recoveryID(name) {
		return errors.New("恢复标识无效")
	}
	root, err := recoveryDirectory(dir)
	if err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".recovery-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	e := f.Close()
	if err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(root, name+".json")); err != nil {
		return err
	}
	d, err := os.Open(root)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func recoveryOwner(dir string) (managedexec.ProcessInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
	if err != nil {
		return managedexec.ProcessInfo{}, err
	}
	defer c.Close()
	raw, err := c.(*net.UnixConn).SyscallConn()
	if err != nil {
		return managedexec.ProcessInfo{}, err
	}
	var pid int
	var inner error
	err = raw.Control(func(fd uintptr) { pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) })
	if err != nil {
		return managedexec.ProcessInfo{}, err
	}
	if inner != nil {
		return managedexec.ProcessInfo{}, inner
	}
	procs, err := managedexec.Processes()
	if err != nil {
		return managedexec.ProcessInfo{}, err
	}
	for _, p := range procs {
		if p.Identity.PID == pid {
			return p, nil
		}
	}
	return managedexec.ProcessInfo{}, errors.New("执行服务所有者不可观察")
}
func recoveryProcessRef(p managedexec.ProcessInfo) string {
	b, _ := json.Marshal(struct {
		Identity managedexec.Identity
		UID      uint32
	}{p.Identity, p.UID})
	hash := sha256.Sum256(b)
	return hex.EncodeToString(hash[:])
}
func recoverySummary(p managedexec.ProcessInfo) domain.WorkerRecoveryProcess {
	return domain.WorkerRecoveryProcess{Ref: recoveryProcessRef(p), PID: p.Identity.PID, Name: p.Name, StartedAt: time.Unix(p.Identity.Sec, int64(p.Identity.Usec)*1000).UTC(), Evidence: p.Evidence}
}
func recoveryProtected(p managedexec.ProcessInfo) bool {
	if managedexec.ControlProcess(p.Identity.PID) {
		return true
	}
	info, err := transport.WorkerProcess(p.Identity.PID)
	if err != nil {
		return false
	}
	return filepath.Base(info.Executable) == "shioji-runtime" && (slices.Contains(info.Arguments, "--network-serve") || slices.Contains(info.Arguments, "--network-prepare"))
}
func recoveryCatalog(ctx context.Context, dir, expected string) (string, []domain.Session, []string, error) {
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "state.sqlite")}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(200)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return "", nil, nil, err
	}
	defer db.Close()
	var id string
	if err = db.QueryRowContext(ctx, "SELECT device_id FROM metadata LIMIT 1").Scan(&id); err != nil {
		if expected == "" {
			return "", nil, nil, err
		}
		return expected, nil, []string{"数据库暂不可读，使用当前已认证的设备绑定；会话数量尚未确认"}, nil
	}
	if id == "" || expected != "" && id != expected {
		return "", nil, nil, errors.New("恢复目标设备身份已改变")
	}
	rows, err := db.QueryContext(ctx, "SELECT body FROM sessions")
	if err != nil {
		return id, nil, []string{"会话清单暂不可读"}, nil
	}
	defer rows.Close()
	var sessions []domain.Session
	for rows.Next() {
		var b []byte
		var s domain.Session
		if rows.Scan(&b) == nil && json.Unmarshal(b, &s) == nil && !s.Archived {
			sessions = append(sessions, s)
		}
	}
	return id, sessions, nil, rows.Err()
}

func InspectDeviceRecovery(ctx context.Context, dir, expected string) (domain.WorkerRecoveryPreview, error) {
	lock, err := recoveryLock(dir)
	if err != nil {
		return domain.WorkerRecoveryPreview{}, err
	}
	defer lock.Close()
	id, sessions, warnings, err := recoveryCatalog(ctx, dir, expected)
	if err != nil {
		return domain.WorkerRecoveryPreview{}, err
	}
	procs, err := managedexec.Processes()
	if err != nil {
		return domain.WorkerRecoveryPreview{}, err
	}
	r := recoveryPreviewRecord{Preview: domain.WorkerRecoveryPreview{DeviceID: id, CheckedAt: time.Now().UTC(), Sessions: sessions, Warnings: warnings}, Targets: map[string]managedexec.ProcessInfo{}}
	r.Preview.Ref = domain.NewID()
	r.Preview.ExpiresAt = r.Preview.CheckedAt.Add(30 * time.Second)
	r.Owner, err = recoveryOwner(dir)
	owned := map[int]string{}
	if err == nil && !recoveryProtected(r.Owner) {
		owned[r.Owner.Identity.PID] = "worker"
	}
	if err != nil {
		r.Preview.Warnings = append(r.Preview.Warnings, "执行服务未响应或未运行，仍可处理已观察的环境")
	}
	sessionByPID := map[int]string{}
	manager, _ := managedexec.New(filepath.Join(dir, "executions"), "")
	for _, s := range sessions {
		scope, e := manager.Existing(s.ID)
		if e != nil || scope == nil {
			continue
		}
		observation, e := scope.InspectExplicitMaintenance()
		if e != nil {
			r.Preview.Warnings = append(r.Preview.Warnings, "部分环境记录不完整，可手动选择进程")
			continue
		}
		for _, p := range observation.Processes {
			owned[p.Identity.PID] = p.Evidence
			sessionByPID[p.Identity.PID] = s.ID
		}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range procs {
			if _, ok := owned[p.Identity.PID]; ok {
				continue
			}
			if _, ok := owned[p.ParentPID]; ok && !recoveryProtected(p) {
				owned[p.Identity.PID] = "observed"
				sessionByPID[p.Identity.PID] = sessionByPID[p.ParentPID]
				changed = true
			}
		}
	}
	for _, p := range procs {
		if evidence, ok := owned[p.Identity.PID]; ok {
			p.Evidence = evidence
		}
		summary := recoverySummary(p)
		summary.SessionID = sessionByPID[p.Identity.PID]
		summary.Protected = recoveryProtected(p)
		if summary.Protected {
			summary.Reason = "恢复控制连接"
		}
		r.Preview.Processes = append(r.Preview.Processes, summary)
		r.Targets[summary.Ref] = p
	}
	if err = recoveryWrite(dir, "preview-"+r.Preview.Ref, r); err != nil {
		return r.Preview, err
	}
	return r.Preview, nil
}

func StartDeviceRecovery(ctx context.Context, dir, binary string, in domain.WorkerRecoveryInput) (domain.WorkerRecoveryOperation, error) {
	var none domain.WorkerRecoveryOperation
	if !recoveryID(in.ID) || !recoveryID(in.ActionID) || in.Action != "restart" || !in.RiskAccepted || in.DeviceID == "" {
		return none, errors.New("请明确确认强制重启的设备与风险")
	}
	lock, err := recoveryLock(dir)
	if err != nil {
		return none, err
	}
	defer lock.Close()
	var r deviceRecoveryRecord
	if err = recoveryRead(dir, in.ID, &r); err == nil {
		if !reflectRecoveryInput(r.Input, in) {
			return none, errors.New("恢复动作与已接纳范围不一致")
		}
		return r.Operation, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return none, err
	}
	if fence, e := transport.PendingDeviceRecovery(dir); e != nil {
		return none, e
	} else if fence != nil {
		if e = recoveryRead(dir, fence.ID, &r); e != nil {
			return none, e
		}
		if r.Operation.Active() {
			return r.Operation, nil
		}
		if e = os.Remove(filepath.Join(dir, "worker-recovery", "fence.json")); e != nil {
			return none, e
		}
		r = deviceRecoveryRecord{}
	}
	if err = recoveryRead(dir, "preview-"+in.PreviewRef, &r.Snapshot); err != nil {
		return none, errors.New("恢复预览已失效，请重新检查")
	}
	if r.Snapshot.Preview.DeviceID != in.DeviceID || time.Now().After(r.Snapshot.Preview.ExpiresAt) {
		return none, errors.New("恢复设备或预览已变化，请重新确认")
	}
	current, _, _, err := recoveryCatalog(ctx, dir, in.DeviceID)
	if err != nil || current != in.DeviceID {
		return none, errors.New("恢复设备身份核对失败")
	}
	selected := map[string]bool{}
	for _, ref := range in.ProcessRefs {
		selected[ref] = true
	}
	for _, p := range r.Snapshot.Preview.Processes {
		if p.Protected {
			if selected[p.Ref] {
				return none, errors.New("不能关闭恢复控制连接")
			}
			continue
		}
		if p.Evidence != "unknown" || selected[p.Ref] {
			exact := r.Snapshot.Targets[p.Ref]
			if managedexec.ProcessState(exact) != "running" {
				return none, errors.New("预览中的进程已变化，请刷新并重新确认")
			}
			r.Operation.Results = append(r.Operation.Results, domain.WorkerRecoveryResult{Process: p, State: "pending"})
			delete(selected, p.Ref)
		}
	}
	if len(selected) > 0 {
		return none, errors.New("所选进程不属于本次预览")
	}
	r.Input = in
	r.Sent = map[string]bool{}
	r.Operation.ID = in.ID
	r.Operation.DeviceID = in.DeviceID
	r.Operation.Phase = "accepted"
	r.Operation.Revision = 1
	r.Operation.UpdatedAt = time.Now().UTC()
	build, err := transport.InspectWorker(ctx, binary)
	if err != nil {
		return none, err
	}
	if !build.RecoveryHelper || build.Schema < store.SchemaVersion {
		return none, errors.New("恢复程序不支持当前数据库")
	}
	if in.Update {
		build, err = transport.VerifyStagedWorker(ctx, dir, in.BuildID)
		if err != nil {
			return none, err
		}
		if build.Schema < store.SchemaVersion {
			return none, errors.New("候选版本不能读取当前数据库")
		}
	}
	// Preserve the old owner's launch context in the child environment only;
	// never persist it in operation or diagnostic records.
	env := os.Environ()
	if r.Snapshot.Owner.Identity.PID > 1 {
		owner, e := transport.WorkerProcess(r.Snapshot.Owner.Identity.PID)
		if e != nil {
			return none, e
		}
		if owner.Sec != r.Snapshot.Owner.Identity.Sec || owner.Usec != r.Snapshot.Owner.Identity.Usec {
			return none, errors.New("执行所有者已改变")
		}
		env, err = transport.WorkerEnvironment(owner)
		if err != nil {
			return none, err
		}
		if slices.Contains(owner.Arguments, "--managed-service") {
			r.Service = fmt.Sprintf("gui/%d/dev.shioji.runtime.agent", os.Getuid())
		}
	}
	if err = recoveryWrite(dir, in.ID, r); err != nil {
		return none, err
	}
	if err = recoveryWrite(dir, "fence", transport.RecoveryFence{ID: in.ID, DeviceID: in.DeviceID}); err != nil {
		return none, err
	}
	cmd := exec.Command(binary, "--state-dir", dir, "--recovery-helper", "run", "--recovery-id", in.ID, "--recovery-device", in.DeviceID)
	cmd.Env = env
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		r.Operation.Phase = "failed"
		r.Operation.Message = "恢复程序未能启动，可重新检查后重试"
		recoveryWrite(dir, in.ID, r)
		return r.Operation, err
	}
	cmd.Process.Release()
	return r.Operation, nil
}
func reflectRecoveryInput(a, b domain.WorkerRecoveryInput) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}

func DeviceRecoveryStatus(dir, id string) (domain.WorkerRecoveryOperation, error) {
	lock, err := recoveryLock(dir)
	if err != nil {
		return domain.WorkerRecoveryOperation{}, err
	}
	defer lock.Close()
	var r deviceRecoveryRecord
	if err = recoveryRead(dir, id, &r); err != nil {
		return r.Operation, err
	}
	if r.Operation.Active() && time.Since(r.Operation.UpdatedAt) > 5*time.Second && (r.Runner.Identity.PID <= 1 || managedexec.ProcessState(r.Runner) != "running") {
		r.Operation.Phase = "partial"
		r.Operation.Message = "恢复进程已中断；已发出的关闭不会自动重发，请重新检查"
		r.Operation.Revision++
		recoveryWrite(dir, id, r)
	}
	return r.Operation, nil
}

func CancelDeviceRecovery(dir, id, device string) (domain.WorkerRecoveryOperation, error) {
	lock, err := recoveryLock(dir)
	if err != nil {
		return domain.WorkerRecoveryOperation{}, err
	}
	defer lock.Close()
	var r deviceRecoveryRecord
	if err = recoveryRead(dir, id, &r); err != nil {
		return r.Operation, err
	}
	if r.Operation.DeviceID != device {
		return r.Operation, errors.New("恢复操作不属于该设备")
	}
	if r.Operation.Phase == "starting" || r.Operation.Phase == "verifying" || r.Operation.Phase == "completed" {
		return r.Operation, errors.New("服务正在启动，请等待原操作结果")
	}
	r.Cancelled = true
	r.Operation.Revision++
	r.Operation.UpdatedAt = time.Now().UTC()
	err = recoveryWrite(dir, id, r)
	return r.Operation, err
}

// Called by the detached signed executable before the ordinary startup path.
// It never requires the old worker's dispatcher or database mutex.
func RunDeviceRecovery(ctx context.Context, config Config, id string) (server *Server, retErr error) {
	serverCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	dir := config.Directory
	lock, err := recoveryLock(dir)
	if err != nil {
		return nil, err
	}
	var r deviceRecoveryRecord
	if err = recoveryRead(dir, id, &r); err != nil {
		lock.Close()
		return nil, err
	}
	if !r.Operation.Active() {
		lock.Close()
		return nil, nil
	}
	if r.Runner.Identity.PID > 1 && managedexec.ProcessState(r.Runner) == "running" {
		lock.Close()
		return nil, errors.New("已有恢复程序运行")
	}
	procs, err := managedexec.Processes()
	if err != nil {
		lock.Close()
		return nil, err
	}
	for _, p := range procs {
		if p.Identity.PID == os.Getpid() {
			r.Runner = p
		}
	}
	r.Operation.Phase = "fencing"
	r.Operation.UpdatedAt = time.Now().UTC()
	err = recoveryWrite(dir, id, r)
	lock.Close()
	if err != nil {
		return nil, err
	}
	phase := func(value, message string) error {
		f, e := recoveryLock(dir)
		if e != nil {
			return e
		}
		defer f.Close()
		var current deviceRecoveryRecord
		if e = recoveryRead(dir, id, &current); e != nil {
			return e
		}
		r.Cancelled = current.Cancelled
		r.Operation.Phase = value
		r.Operation.Message = message
		r.Operation.Revision++
		r.Operation.UpdatedAt = time.Now().UTC()
		return recoveryWrite(dir, id, r)
	}
	defer func() {
		if r.OwnerPaused && managedexec.ProcessState(r.Snapshot.Owner) == "running" {
			_ = managedexec.SignalExact(r.Snapshot.Owner, unix.SIGCONT)
		}
		if retErr != nil {
			phase("partial", retErr.Error())
		}
	}()
	if r.Snapshot.Owner.Identity.PID > 1 && managedexec.ProcessState(r.Snapshot.Owner) == "running" {
		r.OwnerPaused = true
		if err = phase("fencing", ""); err != nil {
			return nil, err
		}
		if err = managedexec.SignalExact(r.Snapshot.Owner, unix.SIGSTOP); err != nil {
			return nil, err
		}
	}
	if err = expandRecoveryDescendants(&r); err != nil {
		return nil, err
	}
	if err = phase("fencing", ""); err != nil {
		return nil, err
	}
	if r.Service != "" && !r.ServiceSuspended {
		// Remove only the execution agent's launchd instance. The independent
		// network job has a different label and is never unregistered here.
		if e := exec.CommandContext(ctx, "/bin/launchctl", "bootout", r.Service).Run(); e != nil {
			return nil, errors.New("无法暂挂执行服务自动启动，请在设备上重试恢复")
		}
		r.ServiceSuspended = true
	}
	if err = phase("closing", ""); err != nil {
		return nil, err
	}
	// Stop the owner first so no old worker can dispatch new input. Captured
	// children remain exact identities after reparenting; no PID-name sweep.
	order := []int{}
	for i, result := range r.Operation.Results {
		if result.Process.PID == r.Snapshot.Owner.Identity.PID {
			order = append(order, i)
		}
	}
	for i, result := range r.Operation.Results {
		if result.Process.PID != r.Snapshot.Owner.Identity.PID {
			order = append(order, i)
		}
	}
	for _, i := range order {
		f, e := recoveryLock(dir)
		if e != nil {
			return nil, e
		}
		var latest deviceRecoveryRecord
		e = recoveryRead(dir, id, &latest)
		if e == nil {
			r.Cancelled = latest.Cancelled
		}
		f.Close()
		if e != nil {
			return nil, e
		}
		if r.Cancelled {
			phase("cancelled", "已取消尚未开始的关闭，已发出的信号无法撤回")
			return nil, nil
		}
		result := &r.Operation.Results[i]
		p := r.Snapshot.Targets[result.Process.Ref]
		status := managedexec.ProcessState(p)
		if status != "running" {
			result.State = status
			continue
		}
		if recoveryProtected(p) {
			result.State = "protected"
			result.Message = "恢复控制连接已保留"
			continue
		}
		if !r.Sent[result.Process.Ref] {
			r.Sent[result.Process.Ref] = true
			if err = phase("closing", ""); err != nil {
				return nil, err
			}
			if e = managedexec.SignalExact(p, unix.SIGKILL); e != nil {
				result.State = "failed"
				result.Message = e.Error()
				continue
			}
		}
		until := time.Now().Add(3 * time.Second)
		for managedexec.ProcessState(p) == "running" && time.Now().Before(until) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
		result.State = managedexec.ProcessState(p)
	}
	if err = phase("verifying", ""); err != nil {
		return nil, err
	}
	if r.Snapshot.Owner.Identity.PID > 1 && managedexec.ProcessState(r.Snapshot.Owner) == "running" {
		return nil, errors.New("旧执行服务仍存活，无法取得数据所有权")
	}
	for _, result := range r.Operation.Results {
		if result.State != "exited" && result.State != "changed" {
			return nil, errors.New("部分进程未确认退出，可重新检查并重试剩余对象")
		}
	}
	owner, err := Acquire(dir)
	if err != nil {
		return nil, fmt.Errorf("尚未取得独占执行权：%w", err)
	}
	keep := false
	defer func() {
		if !keep {
			owner.Close()
		}
	}()
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		return nil, err
	}
	if db.DeviceID() != r.Operation.DeviceID {
		db.Close()
		return nil, errors.New("恢复后的设备身份不一致")
	}
	if err = db.Recover(); err != nil {
		db.Close()
		return nil, err
	}
	db.Close()
	if r.Input.Update {
		// A consistent VACUUM INTO backup is made only after the owner exited.
		r.Operation.Backup, _, err = backupLegacyDatabase(ctx, dir, "recovery-"+id, r.Operation.DeviceID)
		if err != nil {
			return nil, err
		}
		if err = transport.SaveActiveWorker(dir, r.Input.BuildID); err != nil {
			return nil, err
		}
		// A different candidate must start via its normal --ensure path after
		// this helper releases the fence and ownership below.
	}
	if err = phase("starting", ""); err != nil {
		return nil, err
	}
	if err = os.Remove(filepath.Join(dir, "worker-recovery", "fence.json")); err != nil {
		return nil, err
	}
	if r.ServiceSuspended {
		home, _ := os.UserHomeDir()
		plist := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", "Shioji Runtime.app", "Contents", "Library", "LaunchAgents", "dev.shioji.runtime.agent.plist")
		owner.Close()
		if e := exec.CommandContext(ctx, "/bin/launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plist).Run(); e != nil {
			return nil, errors.New("环境已关闭，但执行服务未恢复；请重试启动")
		}
		if err = verifyRecoveredWorker(ctx, dir, &r); err != nil {
			return nil, err
		}
		phase("completed", "执行服务已恢复，待发消息保持暂停")
		return nil, nil
	}
	if r.Input.Update && r.Input.BuildID != runtimebuild.Current().BuildID {
		owner.Close()
		binary, e := transport.StagedWorkerPath(dir, r.Input.BuildID)
		if e != nil {
			return nil, e
		}
		if _, e = transport.EnsureLocalWorker(ctx, dir, binary, ""); e != nil {
			return nil, e
		}
		if err = verifyRecoveredWorker(ctx, dir, &r); err != nil {
			return nil, err
		}
		phase("completed", "执行服务已恢复，待发消息保持暂停")
		return nil, nil
	}
	config.legacyOwner = owner
	server, err = New(serverCtx, config)
	if err != nil {
		return nil, err
	}
	keep = true
	r.Operation.BuildID = server.build.BuildID
	if err = phase("completed", "执行服务已恢复，待发消息保持暂停"); err != nil {
		server.Close()
		return nil, err
	}
	return server, nil
}

// The user authorized this owner and its complete execution environment. Capture
// descendants after freezing dispatch, before their parents can be killed and
// reparent them to launchd. Completely unrelated unknown processes stay opt-in.
func expandRecoveryDescendants(r *deviceRecoveryRecord) error {
	procs, err := managedexec.Processes()
	if err != nil {
		return err
	}
	owned := map[int]bool{}
	seen := map[string]bool{}
	for _, result := range r.Operation.Results {
		seen[result.Process.Ref] = true
		exact := r.Snapshot.Targets[result.Process.Ref]
		for _, p := range procs {
			if p.Identity == exact.Identity && p.UID == exact.UID {
				owned[p.Identity.PID] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range procs {
			if owned[p.Identity.PID] || !owned[p.ParentPID] || recoveryProtected(p) {
				continue
			}
			owned[p.Identity.PID] = true
			changed = true
			p.Evidence = "observed"
			summary := recoverySummary(p)
			if !seen[summary.Ref] {
				r.Snapshot.Targets[summary.Ref] = p
				r.Operation.Results = append(r.Operation.Results, domain.WorkerRecoveryResult{Process: summary, State: "pending"})
				seen[summary.Ref] = true
			}
		}
	}
	return nil
}

func verifyRecoveredWorker(ctx context.Context, dir string, r *deviceRecoveryRecord) error {
	for {
		call, cancel := context.WithTimeout(ctx, time.Second)
		c, err := protocol.Dial(call, func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "worker.sock"))
		}, r.Operation.DeviceID)
		cancel()
		if err == nil {
			defer c.Close()
			if c.Worker == nil || r.Input.Update && c.Worker.BuildID != r.Input.BuildID {
				return errors.New("重连后的执行服务构建不一致")
			}
			r.Operation.BuildID = c.Worker.BuildID
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("环境已关闭，但执行服务尚未重新连接")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
