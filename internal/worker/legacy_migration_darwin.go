package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/runtimebuild"
	"shioji/internal/transport"
)

type legacyMigrationRecord struct {
	Format     int                             `json:"format"`
	Status     domain.WorkerMigrationStatus    `json:"status"`
	OwnerRef   string                          `json:"ownerRef"`
	Source     transport.WorkerProcessIdentity `json:"source"`
	Runner     transport.WorkerProcessIdentity `json:"runner"`
	Candidate  string                          `json:"candidate"`
	StopIssued bool                            `json:"stopIssued"`
	Sessions   []string                        `json:"sessions,omitempty"`
}

func migrationPath(dir, id string) (string, error) {
	if id == "" || len(id) > 80 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return "", errors.New("无效的迁移标识")
	}
	return filepath.Join(dir, "legacy-migrations", id+".json"), nil
}
func migrationLock(dir string) (*os.File, error) {
	root := filepath.Join(dir, "legacy-migrations")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Getuid()) {
		return nil, errors.New("迁移目录不安全")
	}
	fd, err := unix.Open(filepath.Join(root, "migration.lock"), unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "migration.lock")
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func readMigration(dir, id string) (legacyMigrationRecord, error) {
	var r legacyMigrationRecord
	path, err := migrationPath(dir, id)
	if err != nil {
		return r, err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return r, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 128<<10 {
		return r, errors.New("迁移记录不安全")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	if err == nil && (r.Format != 1 || r.Status.ID != id || r.Status.DeviceID == "") {
		err = errors.New("迁移记录身份不一致")
	}
	return r, err
}
func writeMigration(dir string, r *legacyMigrationRecord) error {
	r.Format = 1
	r.Status.OwnerRef = r.OwnerRef
	path, err := migrationPath(dir, r.Status.ID)
	if err != nil {
		return err
	}
	r.Status.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".migration-*")
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
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func writeMigrationIndex(path, id string) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".active-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.WriteString(id); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func sameWorkerProcess(a, b transport.WorkerProcessIdentity) bool {
	return a.PID == b.PID && a.Sec == b.Sec && a.Usec == b.Usec && a.UID == b.UID && a.Executable == b.Executable
}
func migrationRunnerPresent(r legacyMigrationRecord) bool {
	p, err := transport.WorkerProcess(r.Runner.PID)
	return err == nil && sameWorkerProcess(p, r.Runner)
}
func LegacyMigrationStatus(dir, id string) (domain.WorkerMigrationStatus, error) {
	r, err := readMigration(dir, id)
	if err != nil {
		return r.Status, err
	}
	if r.Status.Phase.Active() && !migrationRunnerPresent(r) && time.Since(r.Status.UpdatedAt) > 5*time.Second {
		lock, err := migrationLock(dir)
		if err != nil {
			return r.Status, err
		}
		defer lock.Close()
		r, err = readMigration(dir, id)
		if err != nil {
			return r.Status, err
		}
		if r.Status.Phase.Active() && !migrationRunnerPresent(r) {
			r.Status.Phase = domain.MigrationFailed
			r.Status.Message = "服务准备进程已中断，备份和进度已保留，请重新检查"
			err = writeMigration(dir, &r)
			if err != nil {
				return r.Status, err
			}
		}
	}
	if r.Status.Phase == domain.MigrationFailed && r.StopIssued && legacySourceGone(r.Source) {
		if _, e := readMigrationEnvironment(dir, id); e == nil {
			r.Status.CanResume = true
		}
	}
	return r.Status, nil
}
func StartLegacyMigration(ctx context.Context, dir, binary string, in domain.WorkerMigrationInput) (domain.WorkerMigrationStatus, error) {
	if _, err := migrationPath(dir, in.ID); err != nil {
		return domain.WorkerMigrationStatus{}, err
	}
	lock, err := migrationLock(dir)
	if err != nil {
		return domain.WorkerMigrationStatus{}, err
	}
	defer lock.Close()
	if old, err := readMigration(dir, in.ID); err == nil {
		if old.Status.DeviceID != in.DeviceID || old.OwnerRef != in.OwnerRef {
			return old.Status, errors.New("迁移请求与已接纳操作不一致")
		}
		return old.Status, nil
	} else if !os.IsNotExist(err) {
		return domain.WorkerMigrationStatus{}, err
	}
	activePath := filepath.Join(dir, "legacy-migrations", "active")
	if active, err := os.ReadFile(activePath); err == nil && string(active) != "" {
		old, e := readMigration(dir, string(active))
		if e != nil {
			return domain.WorkerMigrationStatus{}, e
		}
		if old.Status.Phase.Active() {
			return old.Status, nil
		}
	}
	source, err := transport.InspectLegacyWorker(ctx, dir, in.DeviceID)
	if err != nil {
		return domain.WorkerMigrationStatus{}, err
	}
	if source.Info.OwnerRef != in.OwnerRef || !source.Info.Supported {
		return domain.WorkerMigrationStatus{}, errors.New("旧服务身份或支持情况已改变，请重新检查")
	}
	r := legacyMigrationRecord{Status: domain.WorkerMigrationStatus{ID: in.ID, DeviceID: in.DeviceID, Phase: domain.MigrationPrepared}, OwnerRef: in.OwnerRef, Source: source.Owner, Sessions: source.SessionIDs}
	if source.Info.Busy {
		r.Status.Phase = domain.MigrationBlocked
		r.Status.Message = source.Info.Message
		err = writeMigration(dir, &r)
		return r.Status, err
	}
	if err = exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", source.Owner.Executable).Run(); err != nil {
		return r.Status, errors.New("旧服务程序签名无法验证")
	}
	candidate, err := transport.StageWorker(ctx, dir, binary)
	if err != nil {
		return r.Status, err
	}
	r.Candidate = candidate.BuildID
	path, err := transport.StagedWorkerPath(dir, candidate.BuildID)
	if err != nil {
		return r.Status, err
	}
	environment, err := transport.WorkerEnvironment(source.Owner)
	if err != nil {
		return r.Status, err
	}
	if err = writeMigration(dir, &r); err != nil {
		return r.Status, err
	}
	if err = writeMigrationIndex(activePath, in.ID); err != nil {
		return r.Status, err
	}
	if err = writeMigrationEnvironment(dir, in.ID, environment); err != nil {
		return r.Status, err
	}
	err = spawnMigrationRunner(dir, path, &r, environment)
	return r.Status, err
}

func migrationEnvironmentPath(dir, id string) (string, error) {
	p, e := migrationPath(dir, id)
	return p + ".environment", e
}
func writeMigrationEnvironment(dir, id string, environment []string) (retErr error) {
	p, e := migrationEnvironmentPath(dir, id)
	if e != nil {
		return e
	}
	fd, e := unix.Open(p, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), p)
	defer f.Close()
	defer func() {
		if retErr != nil {
			_ = os.Remove(p)
		}
	}()
	raw := make([][]byte, len(environment))
	for i, v := range environment {
		raw[i] = []byte(v)
	}
	if e = json.NewEncoder(f).Encode(raw); e != nil {
		return e
	}
	return f.Sync()
}
func readMigrationEnvironment(dir, id string) ([]string, error) {
	p, e := migrationEnvironmentPath(dir, id)
	if e != nil {
		return nil, e
	}
	st, e := os.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 1<<20 {
		return nil, errors.New("原服务启动环境不可用")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var raw [][]byte
	if e = json.Unmarshal(b, &raw); e != nil {
		return nil, e
	}
	result := make([]string, len(raw))
	for i, v := range raw {
		result[i] = string(v)
	}
	return result, nil
}
func spawnMigrationRunner(dir, binary string, r *legacyMigrationRecord, environment []string) error {
	if len(r.Source.Arguments) < 1 {
		return errors.New("原服务启动参数缺失")
	}
	args := append([]string{}, r.Source.Arguments[1:]...)
	args = append(args, "--state-dir", dir, "--legacy-migration", "run", "--migration-id", r.Status.ID)
	cmd := exec.Command(binary, args...)
	cmd.Env = environment
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e := cmd.Start(); e != nil {
		r.Status.Phase = domain.MigrationFailed
		r.Status.Message = "无法启动迁移任务"
		if !r.StopIssued {
			if path, e := migrationEnvironmentPath(dir, r.Status.ID); e == nil {
				_ = os.Remove(path)
			}
		}
		writeMigration(dir, r)
		return e
	}
	runner, e := transport.WorkerProcess(cmd.Process.Pid)
	cmd.Process.Release()
	if e == nil {
		r.Runner = runner
		return writeMigration(dir, r)
	}
	// A live child will register itself under this same record lock. Do not
	// report the start as unaccepted merely because its identity raced exit.
	return nil
}
func legacySourceGone(source transport.WorkerProcessIdentity) bool {
	actual, e := transport.WorkerProcess(source.PID)
	if e == nil {
		return !sameWorkerProcess(actual, source)
	}
	return errors.Is(unix.Kill(source.PID, 0), unix.ESRCH)
}
func ResumeLegacyMigration(ctx context.Context, dir string, in domain.WorkerMigrationInput) (domain.WorkerMigrationStatus, error) {
	lock, e := migrationLock(dir)
	if e != nil {
		return domain.WorkerMigrationStatus{}, e
	}
	defer lock.Close()
	r, e := readMigration(dir, in.ID)
	if e != nil {
		return r.Status, e
	}
	if r.Status.DeviceID != in.DeviceID || r.OwnerRef != in.OwnerRef {
		return r.Status, errors.New("迁移请求身份不一致")
	}
	if r.Status.Phase == domain.MigrationCompleted || migrationRunnerPresent(r) {
		return r.Status, nil
	}
	if !r.StopIssued || !legacySourceGone(r.Source) {
		return r.Status, errors.New("旧服务停止尚未确认，请重新检查；不会重复停止")
	}
	environment, e := readMigrationEnvironment(dir, in.ID)
	if e != nil {
		return r.Status, e
	}
	if _, e = transport.VerifyStagedWorker(ctx, dir, r.Candidate); e != nil {
		return r.Status, e
	}
	binary, e := transport.StagedWorkerPath(dir, r.Candidate)
	if e != nil {
		return r.Status, e
	}
	r.Status.Phase = domain.MigrationPrepared
	r.Status.Message = ""
	r.Status.CanResume = false
	if e = writeMigration(dir, &r); e != nil {
		return r.Status, e
	}
	e = spawnMigrationRunner(dir, binary, &r, environment)
	return r.Status, e
}

// Called only by the detached signed helper. A retry queries this record; it
// does not issue a second signal for a stop whose outcome is unknown.
func RunLegacyMigration(ctx context.Context, config Config, id string) (server *Server, retErr error) {
	lock, err := migrationLock(config.Directory)
	if err != nil {
		return nil, err
	}
	r, err := readMigration(config.Directory, id)
	if err != nil {
		lock.Close()
		return nil, err
	}
	me, err := transport.WorkerProcess(os.Getpid())
	if err != nil {
		lock.Close()
		return nil, err
	}
	if r.Runner.PID != 0 && !sameWorkerProcess(me, r.Runner) {
		lock.Close()
		return nil, errors.New("迁移任务所有者已改变")
	}
	r.Runner = me
	if err = writeMigration(config.Directory, &r); err != nil {
		lock.Close()
		return nil, err
	}
	lock.Close()
	// The runner remains independent of the short-lived start/status commands.
	// Serialize runners separately without blocking their progress readers.
	fd, err := unix.Open(filepath.Join(config.Directory, "legacy-migrations", "run.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.New("已有迁移任务运行")
	}
	phase := func(p domain.WorkerMigrationPhase, message string) error {
		f, e := migrationLock(config.Directory)
		if e != nil {
			return e
		}
		defer f.Close()
		r.Status.Phase = p
		r.Status.Message = message
		return writeMigration(config.Directory, &r)
	}
	defer func() {
		if retErr != nil {
			_ = phase(domain.MigrationFailed, retErr.Error())
		}
		if !r.StopIssued {
			if path, e := migrationEnvironmentPath(config.Directory, id); e == nil {
				_ = os.Remove(path)
			}
		}
	}()
	candidate, err := transport.VerifyStagedWorker(ctx, config.Directory, r.Candidate)
	if err != nil {
		return nil, err
	}
	if runtimebuild.Current().BuildID != r.Candidate {
		return nil, errors.New("迁移程序不是已核验的候选版本")
	}
	if !r.StopIssued {
		source, err := transport.InspectLegacyWorker(ctx, config.Directory, r.Status.DeviceID)
		if err != nil {
			return nil, err
		}
		if source.Info.OwnerRef != r.OwnerRef || !sameWorkerProcess(source.Owner, r.Source) {
			return nil, errors.New("旧服务身份已改变，未停止任何进程")
		}
		if source.Info.Busy {
			_ = phase(domain.MigrationBlocked, source.Info.Message)
			return nil, nil
		}
		if err = preflightLegacyDatabase(ctx, config.Directory, r.Status.DeviceID, candidate.Schema); err != nil {
			return nil, err
		}
		// Explicit consent authorizes this exact owner. It does not promise atomic
		// idle admission from a legacy protocol that never implemented that fence.
		r.StopIssued = true
		if err = phase(domain.MigrationStopping, ""); err != nil {
			return nil, err
		}
		actual, err := transport.WorkerProcess(r.Source.PID)
		if err != nil || !sameWorkerProcess(actual, r.Source) {
			return nil, errors.New("停止前旧服务身份发生变化")
		}
		if err = unix.Kill(r.Source.PID, unix.SIGTERM); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			actual, e := transport.WorkerProcess(r.Source.PID)
			if e != nil {
				if errors.Is(unix.Kill(r.Source.PID, 0), unix.ESRCH) {
					break
				}
			} else if !sameWorkerProcess(actual, r.Source) {
				break
			}
			if time.Now().After(deadline) {
				return nil, errors.New("旧服务未能正常停止，未强制结束；请重新检查")
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	} else if !legacySourceGone(r.Source) {
		return nil, errors.New("旧服务停止尚未确认；不会重复停止，请重新检查")
	}
	owner, err := Acquire(config.Directory)
	if err != nil {
		return nil, fmt.Errorf("无法取得设备目录所有权，可能有其他服务启动：%w", err)
	}
	keep := false
	defer func() {
		if !keep {
			owner.Close()
		}
	}()
	if err = phase(domain.MigrationBackingUp, ""); err != nil {
		return nil, err
	}
	r.Status.Backup, r.Sessions, err = backupLegacyDatabase(ctx, config.Directory, id, r.Status.DeviceID)
	if err != nil {
		return nil, err
	}
	if err = phase(domain.MigrationStarting, ""); err != nil {
		return nil, err
	}
	if err = transport.SaveActiveWorker(config.Directory, r.Candidate); err != nil {
		return nil, err
	}
	config.legacyOwner = owner
	config.legacyExpected = &r
	server, err = New(ctx, config)
	if err != nil {
		return nil, err
	}
	keep = true
	if err = phase(domain.MigrationVerifying, ""); err != nil {
		server.Close()
		return nil, err
	}
	snap, err := server.store.Snapshot()
	if err != nil {
		server.Close()
		return nil, err
	}
	if err = verifyLegacyCatalog(r, snap); err != nil {
		server.Close()
		return nil, err
	}
	if err = phase(domain.MigrationCompleted, ""); err != nil {
		server.Close()
		return nil, err
	}
	if path, e := migrationEnvironmentPath(config.Directory, id); e == nil {
		_ = os.Remove(path)
	}
	return server, nil
}
func verifyLegacyCatalog(r legacyMigrationRecord, snapshot domain.Snapshot) error {
	ids := make([]string, 0, len(snapshot.Sessions))
	for _, s := range snapshot.Sessions {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	expected := append([]string{}, r.Sessions...)
	slices.Sort(expected)
	if snapshot.DeviceID != r.Status.DeviceID || !reflect.DeepEqual(ids, expected) {
		return errors.New("迁移后的设备或会话目录不一致，数据和备份已保留")
	}
	return nil
}
func openLegacyReadOnly(dir string) (*sql.DB, error) {
	path := filepath.Join(dir, "state.sqlite")
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("旧数据库不是普通文件")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	return sql.Open("sqlite", u.String())
}
func backupLegacyDatabase(ctx context.Context, dir, id, device string) (string, []string, error) {
	if _, err := migrationPath(dir, id); err != nil {
		return "", nil, err
	}
	root := filepath.Join(dir, "backups")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", nil, err
	}
	st, err := os.Lstat(root)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		return "", nil, errors.New("备份目录不安全")
	}
	final := filepath.Join(root, "legacy-"+id+".sqlite")
	db, err := openLegacyReadOnly(dir)
	if err != nil {
		return "", nil, err
	}
	defer db.Close()
	var got string
	if err = db.QueryRowContext(ctx, "SELECT device_id FROM metadata WHERE id=1").Scan(&got); err != nil || got != device {
		return "", nil, errors.New("旧数据库身份不一致")
	}
	if _, err = os.Lstat(final); os.IsNotExist(err) {
		f, e := os.CreateTemp(root, ".legacy-backup-*")
		if e != nil {
			return "", nil, e
		}
		tmp := f.Name()
		f.Close()
		defer os.Remove(tmp)
		if _, err = db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
			return "", nil, err
		}
		f, err = os.OpenFile(tmp, os.O_RDWR, 0600)
		if err != nil {
			return "", nil, err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return "", nil, err
		}
		if err = os.Link(tmp, final); err != nil {
			return "", nil, err
		}
		d, e := os.Open(root)
		if e != nil {
			return "", nil, e
		}
		err = d.Sync()
		d.Close()
		if err != nil {
			return "", nil, err
		}
	} else if err != nil {
		return "", nil, err
	}
	st, err = os.Lstat(final)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return "", nil, errors.New("备份文件不安全")
	}
	u := url.URL{Scheme: "file", Path: final, RawQuery: "mode=ro"}
	copy, err := sql.Open("sqlite", u.String())
	if err != nil {
		return "", nil, err
	}
	defer copy.Close()
	if err = copy.QueryRowContext(ctx, "SELECT device_id FROM metadata WHERE id=1").Scan(&got); err != nil || got != device {
		return "", nil, errors.New("备份身份不一致")
	}
	rows, err := copy.QueryContext(ctx, "SELECT id FROM sessions ORDER BY id")
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return "", nil, err
		}
		ids = append(ids, id)
	}
	return final, ids, rows.Err()
}

// The owning device verifies both the catalog identity and schema before a
// signal; a successful socket handshake alone cannot authorize another DB.
func preflightLegacyDatabase(ctx context.Context, dir, device string, maximum int) error {
	db, err := openLegacyReadOnly(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	var actual string
	if err = db.QueryRowContext(ctx, "SELECT device_id FROM metadata WHERE id=1").Scan(&actual); err != nil || actual != device {
		return errors.New("旧数据库身份不一致，未停止旧服务")
	}
	var schema int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); err != nil {
		return err
	}
	if schema > maximum {
		return errors.New("新版服务不能读取当前数据库，未停止旧服务")
	}
	return nil
}
