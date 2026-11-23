package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/background"
	"shioji/internal/domain"
	"shioji/internal/runtimebuild"
	"shioji/internal/tailnet"
)

type networkClientMarker struct {
	Version int
	PID     int
	Sec     int64
	Usec    int32
}

func registerNetworkClient(directory string, p WorkerProcessIdentity) (func(), error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || !ok || owner.Uid != uint32(os.Getuid()) {
		return nil, errors.New("网络身份目录不安全")
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	root := filepath.Join(directory, "clients")
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	st, err = os.Lstat(root)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		return nil, errors.New("网络客户端目录不安全")
	}
	marker := networkClientMarker{1, p.PID, p.Sec, p.Usec}
	data, _ := json.Marshal(marker)
	f, err := os.CreateTemp(root, ".client-")
	if err != nil {
		return nil, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	path := filepath.Join(root, strconv.Itoa(p.PID)+".json")
	if err = os.Rename(temp, path); err != nil {
		return nil, err
	}
	return func() { os.Remove(path) }, nil
}

func RegisterNetworkClient(directory string) (func(), error) {
	p, err := WorkerProcess(os.Getpid())
	if err != nil {
		return nil, err
	}
	return registerNetworkClient(directory, p)
}

func legacyNetworkOwner(p WorkerProcessIdentity, directory string) bool {
	name := filepath.Base(p.Executable)
	if p.UID != uint32(os.Getuid()) || !strings.HasPrefix(name, "shioji") || strings.Contains(name, "worker") || strings.Contains(name, "runtime") {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return true
	}
	state := filepath.Join(home, "Library", "Application Support", "Shioji", "client", "state.json")
	for i, a := range p.Arguments {
		if (a == "--client-state" || a == "-client-state") && i+1 < len(p.Arguments) {
			state = p.Arguments[i+1]
		}
		if strings.HasPrefix(a, "--client-state=") {
			state = strings.TrimPrefix(a, "--client-state=")
		} else if strings.HasPrefix(a, "-client-state=") {
			state = strings.TrimPrefix(a, "-client-state=")
		}
	}
	// Old relative launch arguments cannot safely be interpreted in the
	// helper's CWD. Require that old process to exit rather than guessing.
	if filepath.IsAbs(state) {
		abs, _ := filepath.Abs(directory)
		if filepath.Join(filepath.Dir(state), "tailnet") != abs {
			return false
		}
	}
	path := filepath.Join(directory, "clients", strconv.Itoa(p.PID)+".json")
	var marker networkClientMarker
	f, e := os.Open(path)
	if e == nil {
		defer f.Close()
		st, e := f.Stat()
		if e == nil && st.Size() < 4096 && json.NewDecoder(f).Decode(&marker) == nil && marker.Version == 1 && marker.PID == p.PID && marker.Sec == p.Sec && marker.Usec == p.Usec {
			return false
		}
	}
	return true
}

// Older GUI versions do not participate in flock. Refuse their matching
// profile until they exit; do not take a free new lock as proof of no old node.
func CheckLegacyNetworkOwners(directory string) error {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return err
	}
	for _, info := range processes {
		if info.Eproc.Ucred.Uid != uint32(os.Getuid()) {
			continue
		}
		p, e := WorkerProcess(int(info.Proc.P_pid))
		if e != nil {
			continue
		}
		if legacyNetworkOwner(p, directory) {
			return tailnet.ErrOwned
		}
	}
	return nil
}

func networkDefaultDirectory() (string, error) {
	home, err := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Shioji", "client", "tailnet"), err
}

func inspectNetworkRuntime(ctx context.Context, binary string) error {
	if err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", binary).Run(); err != nil {
		return errors.New("网络后台宿主签名校验失败")
	}
	cmd := exec.CommandContext(ctx, binary, "--network-describe")
	var output bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 8192}
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("后台宿主不支持独立网络服务，请更新应用")
	}
	var info struct {
		Version int
		Role    string
	}
	if json.Unmarshal(output.Bytes(), &info) != nil || info.Version != 1 || info.Role != "network" {
		return errors.New("网络后台宿主版本不兼容")
	}
	return nil
}

// Install the network host alongside, never over, the registered execution
// bundle. This avoids unregistering or replacing a live worker during migration.
func installNetworkRuntime(ctx context.Context, workerBinary string) (string, error) {
	source := bundledRuntime(workerBinary)
	if err := verifyRuntimeBundle(ctx, source); err != nil {
		return "", err
	}
	sourceBin := filepath.Join(source, "Contents", "MacOS", "shioji-runtime")
	if err := inspectNetworkRuntime(ctx, sourceBin); err != nil {
		return "", err
	}
	digest, err := runtimebuild.Digest(sourceBin)
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", "Network", digest)
	target := filepath.Join(root, runtimeBundleName)
	if _, e := os.Lstat(target); os.IsNotExist(e) {
		if err = os.MkdirAll(root, 0700); err != nil {
			return "", err
		}
		stage, err := os.MkdirTemp(root, ".install-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(stage)
		copy := filepath.Join(stage, runtimeBundleName)
		if err = exec.CommandContext(ctx, "/usr/bin/ditto", source, copy).Run(); err != nil {
			return "", err
		}
		if err = verifyRuntimeBundle(ctx, copy); err != nil {
			return "", err
		}
		if err = os.Rename(copy, target); err != nil {
			return "", err
		}
	} else if e != nil {
		return "", e
	}
	if err = verifyRuntimeBundle(ctx, target); err != nil {
		return "", err
	}
	bin := filepath.Join(target, "Contents", "MacOS", "shioji-runtime")
	actual, err := runtimebuild.Digest(bin)
	if err != nil || actual != digest {
		return "", errors.New("网络宿主安装内容已改变")
	}
	return bin, nil
}

func networkPreflight(ctx context.Context, binary, directory string, explicit bool) error {
	args := []string{"--network-preflight", "--network-dir", directory}
	if explicit {
		args = append(args, "--network-authorize")
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	var output bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 8192}
	cmd.Stderr = &boundedWriter{w: &output, remaining: 2048}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("网络身份预检未完成：%w", tailnet.ErrUserAction)
	}
	var result struct {
		Version int
		Ready   bool
	}
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Version != 1 || !result.Ready {
		return errors.New("网络身份预检未返回有效状态")
	}
	return nil
}

func networkRegistration(ctx context.Context, binary, action string) (domain.BackgroundService, error) {
	cmd := exec.CommandContext(ctx, binary, "--action", action, "--plist", background.NetworkPlistName)
	var output bytes.Buffer
	cmd.Stdout = &boundedWriter{w: &output, remaining: 8192}
	cmd.Stderr = io.Discard
	err := cmd.Run()
	var result domain.BackgroundService
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Format != 1 {
		return result, errors.New("网络后台服务未返回有效状态")
	}
	if err != nil {
		return result, errors.New("网络后台服务注册失败")
	}
	return result, nil
}

func networkLaunchLock(ctx context.Context, directory string) (*os.File, error) {
	fd, err := unix.Open(filepath.Join(directory, "launch.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "network-launch")
	for {
		if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return f, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func networkPeerPID(conn net.Conn) (int, error) {
	u, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("网络服务需要本机 Unix 连接")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var inner error
	err = raw.Control(func(fd uintptr) { pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) })
	if err != nil {
		return 0, err
	}
	return pid, inner
}

func networkHostProcess(p WorkerProcessIdentity, directory string) bool {
	serve := false
	for _, a := range p.Arguments {
		clean := strings.TrimLeft(a, "-")
		if clean == "network-serve" || clean == "network-serve=true" {
			serve = true
		}
	}
	if !serve {
		return false
	}
	dir := ""
	for i, a := range p.Arguments {
		clean := strings.TrimLeft(a, "-")
		if clean == "network-dir" && i+1 < len(p.Arguments) {
			dir = p.Arguments[i+1]
		}
		if strings.HasPrefix(clean, "network-dir=") {
			dir = strings.TrimPrefix(clean, "network-dir=")
		}
	}
	if dir == "" {
		dir, _ = networkDefaultDirectory()
	}
	return dir == directory
}

type networkHostSelection struct {
	Version        int
	Binary, Digest string
}

func readNetworkHostSelection(directory string) (networkHostSelection, error) {
	var selected networkHostSelection
	data, err := os.ReadFile(filepath.Join(directory, "network-host.json"))
	if err != nil {
		return selected, err
	}
	if len(data) > 4096 || json.Unmarshal(data, &selected) != nil || selected.Version != 1 || !filepath.IsAbs(selected.Binary) {
		return selected, errors.New("网络宿主记录无效")
	}
	digest, err := runtimebuild.Digest(selected.Binary)
	if err != nil || digest != selected.Digest {
		return selected, errors.New("网络宿主记录内容已改变")
	}
	return selected, nil
}
func saveNetworkHostSelection(directory, binary string) error {
	digest, err := runtimebuild.Digest(binary)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(networkHostSelection{1, binary, digest})
	f, err := os.CreateTemp(directory, ".network-host-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	return os.Rename(f.Name(), filepath.Join(directory, "network-host.json"))
}
func verifyInstalledNetworkHost(ctx context.Context, binary string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, "Library", "Application Support", "Shioji", "Runtime", "Network")
	if !strings.HasPrefix(binary, root+string(os.PathSeparator)) || !strings.HasSuffix(binary, "/Shioji Runtime.app/Contents/MacOS/shioji-runtime") {
		return errors.New("网络服务程序身份不匹配")
	}
	bundle := filepath.Clean(filepath.Join(filepath.Dir(binary), "..", ".."))
	if err = verifyRuntimeBundle(ctx, bundle); err != nil {
		return err
	}
	digest, err := runtimebuild.Digest(binary)
	if err != nil || digest != filepath.Base(filepath.Dir(bundle)) {
		return errors.New("网络服务版本身份不匹配")
	}
	return nil
}

// ServiceManagement can still associate a registered network agent with the
// old shared Runtime path. Only explicit reconnect may move that registration.
// Use the last verified network bundle for unregister, since the shared
// bundle may have been replaced by an execution update since launch.
func legacyNetworkRegistration(ctx context.Context, directory string, owner WorkerProcessIdentity) (networkHostSelection, error) {
	root := filepath.Join(filepath.Dir(filepath.Dir(directory)), "Runtime")
	bundle := filepath.Join(root, runtimeBundleName)
	if owner.Executable != filepath.Join(bundle, "Contents", "MacOS", "shioji-runtime") ||
		owner.UID != uint32(os.Getuid()) || !networkHostProcess(owner, directory) {
		return networkHostSelection{}, errors.New("网络服务进程身份不匹配")
	}
	if err := verifyRuntimeBundle(ctx, bundle); err != nil {
		return networkHostSelection{}, err
	}
	registration, err := readNetworkHostSelection(directory)
	if err != nil {
		return networkHostSelection{}, fmt.Errorf("无法核对旧 Tailnet 后台注册：%w", err)
	}
	if err = verifyInstalledNetworkHost(ctx, registration.Binary); err != nil {
		return networkHostSelection{}, err
	}
	return registration, nil
}

func launchNetworkHost(ctx context.Context, binary, directory string, production, register bool) (*WorkerProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if production {
		action := "status"
		if register {
			action = "register"
		}
		state, err := networkRegistration(ctx, binary, action)
		if err != nil {
			return nil, err
		}
		if state.State != "enabled" {
			return nil, networkUserActionError("请在系统设置允许 Shioji 网络后台服务")
		}
		return nil, nil
	}
	cmd := exec.Command(binary, "--network-serve", "--network-dir", directory)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go cmd.Wait()
	owner, err := WorkerProcess(cmd.Process.Pid)
	if err != nil {
		return nil, err
	}
	return &owner, nil
}
func stopNetworkHost(ctx context.Context, owner WorkerProcessIdentity, registration, directory string, production bool) error {
	if production {
		state, err := networkRegistration(ctx, registration, "unregister")
		if err != nil {
			return err
		}
		if state.State != "notRegistered" && state.State != "notFound" {
			return errors.New("网络后台服务尚未停止")
		}
	} else if owner.PID > 0 {
		current, err := WorkerProcess(owner.PID)
		if err != nil {
			return nil
		}
		if current.Sec != owner.Sec || current.Usec != owner.Usec || current.Executable != owner.Executable || !networkHostProcess(current, directory) {
			return errors.New("网络服务进程身份已改变")
		}
		if err = syscall.Kill(owner.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}
	for owner.PID > 0 {
		current, err := WorkerProcess(owner.PID)
		if err != nil || current.Sec != owner.Sec || current.Usec != owner.Usec {
			return nil
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
func networkOperations(directory, workerBinary, developmentHost string) (func(int) error, func(context.Context, bool) error) {
	var mu sync.Mutex
	known := map[int]WorkerProcessIdentity{}
	productionDir, _ := networkDefaultDirectory()
	production := directory == productionDir
	verify := func(pid int) error {
		p, err := WorkerProcess(pid)
		if err != nil {
			return err
		}
		if p.UID != uint32(os.Getuid()) || !networkHostProcess(p, directory) {
			return errors.New("网络服务进程身份不匹配")
		}
		mu.Lock()
		defer mu.Unlock()
		if old, ok := known[pid]; ok && old.Sec == p.Sec && old.Usec == p.Usec && old.Executable == p.Executable {
			return nil
		}
		call, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if production {
			err = verifyInstalledNetworkHost(call, p.Executable)
		} else {
			err = inspectNetworkRuntime(call, p.Executable)
		}
		if err != nil {
			return err
		}
		if len(known) > 8 {
			clear(known)
		}
		known[pid] = p
		return nil
	}
	ensure := func(ctx context.Context, explicit bool) error {
		if err := CheckLegacyNetworkOwners(directory); err != nil {
			return networkUserActionError(err.Error())
		}
		lock, err := networkLaunchLock(ctx, directory)
		if err != nil {
			return err
		}
		defer lock.Close()
		var old *WorkerProcessIdentity
		var oldRegistration string
		var oldInfo tailnet.HostInfo
		if conn, e := (&net.Dialer{}).DialContext(ctx, "unix", tailnet.ServiceSocket(directory)); e == nil {
			pid, e := networkPeerPID(conn)
			conn.Close()
			if e != nil {
				return e
			}
			owner, e := WorkerProcess(pid)
			if e != nil {
				return e
			}
			old = &owner
			if e = verify(pid); e == nil {
				oldRegistration = owner.Executable
				oldInfo, e = tailnet.InspectService(ctx, directory, verify)
				if e != nil {
					return e
				}
			} else {
				if !production || !explicit {
					return e
				}
				legacy, legacyErr := legacyNetworkRegistration(ctx, directory, owner)
				if legacyErr != nil {
					return legacyErr
				}
				oldRegistration, oldInfo.BuildID = legacy.Binary, legacy.Digest
			}
			if !explicit {
				if !oldInfo.Compatible {
					return networkUserActionError("网络服务需要更新，请重新连接")
				}
				return nil
			}
		}
		var bin string
		if !explicit {
			if selected, e := readNetworkHostSelection(directory); e == nil {
				bin = selected.Binary
				if production {
					err = verifyInstalledNetworkHost(ctx, bin)
				} else {
					err = inspectNetworkRuntime(ctx, bin)
				}
			}
		}
		if bin == "" {
			if production {
				if developmentHost != "" {
					bin = developmentHost
					err = verifyInstalledNetworkHost(ctx, bin)
				} else {
					bin, err = installNetworkRuntime(ctx, workerBinary)
				}
			} else {
				bin = developmentHost
				if bin == "" {
					bin = filepath.Join(bundledRuntime(workerBinary), "Contents", "MacOS", "shioji-runtime")
				}
				bin, err = filepath.Abs(bin)
				if err == nil {
					err = inspectNetworkRuntime(ctx, bin)
				}
			}
		}
		if err != nil {
			return err
		}
		if err = networkPreflight(ctx, bin, directory, explicit); err != nil {
			return err
		}
		digest, err := runtimebuild.Digest(bin)
		if err != nil {
			return err
		}
		if old != nil && oldInfo.Compatible && oldInfo.BuildID == digest {
			return saveNetworkHostSelection(directory, bin)
		}
		if old != nil {
			if err = stopNetworkHost(ctx, *old, oldRegistration, directory, production); err != nil {
				return err
			}
		}
		spawned, err := launchNetworkHost(ctx, bin, directory, production, explicit)
		if err == nil {
			for {
				if info, e := tailnet.InspectService(ctx, directory, verify); e == nil && info.Compatible && info.BuildID == digest {
					return saveNetworkHostSelection(directory, bin)
				}
				if spawned != nil {
					p, e := WorkerProcess(spawned.PID)
					if e != nil || p.Sec != spawned.Sec || p.Usec != spawned.Usec {
						err = errors.New("网络后台宿主启动失败")
						break
					}
				}
				timer := time.NewTimer(50 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					err = ctx.Err()
				case <-timer.C:
				}
				if err != nil {
					break
				}
			}
		}
		// Restoring the previous signed owner is only failure recovery for an
		// explicit handoff. GUI cancellation and revoked OS approval are respected.
		if old != nil && !errors.Is(ctx.Err(), context.Canceled) && !errors.Is(err, tailnet.ErrUserAction) {
			rollback, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			candidate := WorkerProcessIdentity{Executable: bin}
			if spawned != nil {
				candidate = *spawned
			}
			if e := stopNetworkHost(rollback, candidate, bin, directory, production); e != nil {
				return fmt.Errorf("网络升级失败且候选宿主未退出：%w", e)
			}
			if _, e := launchNetworkHost(rollback, oldRegistration, directory, production, true); e != nil {
				return fmt.Errorf("网络升级失败，旧宿主恢复失败：%w", e)
			}
			for {
				info, e := tailnet.InspectService(rollback, directory, verify)
				if e == nil && (oldInfo.BuildID == "" || info.BuildID == oldInfo.BuildID) {
					if e = saveNetworkHostSelection(directory, oldRegistration); e != nil {
						return e
					}
					return errors.New("网络服务升级未完成，已恢复原版本")
				}
				timer := time.NewTimer(50 * time.Millisecond)
				select {
				case <-rollback.Done():
					timer.Stop()
					return errors.New("网络服务升级失败，旧宿主尚未恢复，请重新连接")
				case <-timer.C:
				}
			}
		}
		return err
	}
	return verify, ensure
}

// PrepareNetworkHost runs inside the detached signed preparation host. GUI
// cancellation cannot interrupt the stop/start/rollback critical section.
func PrepareNetworkHost(ctx context.Context, directory, workerBinary, candidate string) error {
	_, ensure := networkOperations(directory, workerBinary, candidate)
	return ensure(ctx, true)
}
func NewNetworkClient(directory, workerBinary, developmentHost string) *tailnet.Client {
	verify, direct := networkOperations(directory, workerBinary, developmentHost)
	ensure := func(ctx context.Context, explicit bool) error {
		if !explicit {
			return direct(ctx, false)
		}
		return prepareNetworkDetached(ctx, directory, workerBinary, developmentHost)
	}
	return tailnet.NewClient(directory, ensure, verify)
}
