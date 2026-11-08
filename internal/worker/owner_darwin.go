package worker

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

var ErrOwned = errors.New("worker already owned by another process")

type Owner struct {
	dir      string
	lock     *os.File
	listener *net.UnixListener
}

func Acquire(dir string) (*Owner, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || st.Uid != uint32(os.Getuid()) || info.Mode().Perm() != 0700 {
		return nil, errors.New("worker directory must be owned by this user with mode 0700")
	}
	fd, err := unix.Open(filepath.Join(dir, "owner.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "owner.lock")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrOwned
	}
	return &Owner{dir: dir, lock: f}, nil
}
func (o *Owner) Listen() (net.Listener, error) {
	path := filepath.Join(o.dir, "worker.sock")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("refusing to replace non-socket worker path")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		return nil, err
	}
	o.listener = l
	return l, nil
}
func (o *Owner) Close() error {
	if o.listener != nil {
		o.listener.Close()
	}
	if o.lock != nil {
		err := o.lock.Close()
		o.lock = nil
		return err
	}
	return nil
}
func authorizeLocal(conn net.Conn) error {
	u, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("local control requires a Unix socket")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Xucred
	var inner error
	if err = raw.Control(func(fd uintptr) { cred, inner = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) }); err != nil {
		return err
	}
	if inner != nil {
		return inner
	}
	if cred.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("unauthorized local principal")
	}
	return nil
}
