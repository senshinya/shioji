package tailnet

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

var ErrOwned = errors.New("Tailnet 已由另一个进程持有，请先退出旧版应用")

// Keep the inode in place: removing a lock file would allow two independent
// flock owners after another process had already opened the old inode.
func lockIdentity(directory string) (*os.File, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || !ok || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("Tailnet 身份目录权限不安全")
	}
	if err = os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(directory, "owner.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "tailnet-owner")
	var s unix.Stat_t
	if err = unix.Fstat(fd, &s); err != nil || s.Uid != uint32(os.Getuid()) || s.Mode&unix.S_IFMT != unix.S_IFREG || s.Nlink != 1 {
		f.Close()
		return nil, errors.New("Tailnet 身份锁不安全")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrOwned
		}
		return nil, err
	}
	return f, nil
}

func peerProcess(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Xucred
	var pid int
	var inner error
	err = raw.Control(func(fd uintptr) {
		cred, inner = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if inner == nil {
			pid, inner = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		}
	})
	if err != nil {
		return 0, err
	}
	if inner != nil {
		return 0, inner
	}
	if cred.Uid != uint32(os.Getuid()) || pid <= 0 {
		return 0, errors.New("Tailnet 仅允许本机当前用户访问")
	}
	return pid, nil
}
