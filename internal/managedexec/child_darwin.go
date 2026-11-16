// Package managedexec owns the native process groups launched for a session.
package managedexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const RegistrationPrefix = "SHIOJI_LAUNCH "

// BootstrapEnv avoids CoreFoundation's user-service lookup during dyld
// initialization, before Go can register the process. Derive the encoding from
// the real UID; target environment and loader settings arrive only in the ACK.
func BootstrapEnv() []string {
	return []string{"PATH=/usr/bin:/bin", fmt.Sprintf("__CF_USER_TEXT_ENCODING=0x%X:0:0", os.Getuid())}
}

type Registration struct {
	ID           string
	Root, Anchor int
}

type Acknowledgement struct {
	ID        string
	Program   string
	Argv, Env []string
	PipeStdin bool
}

// ChildMain runs before worker configuration, listeners, or application startup.
func ChildMain(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "--managed-anchor":
		return true, runAnchor()
	case "--managed-exec":
		if len(args) != 2 || len(args[1]) > 256 {
			return true, errors.New("invalid managed launch")
		}
		return true, runChild(args[1], os.Stdin, os.Stdout)
	default:
		return false, nil
	}
}

func runAnchor() error {
	signal.Ignore(syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	commit := os.NewFile(3, "ownership-commit")
	ready := os.NewFile(4, "anchor-ready")
	if commit == nil || ready == nil {
		return errors.New("anchor has no ownership channel")
	}
	if _, err := ready.Write([]byte{1}); err != nil {
		return err
	}
	ready.Close()
	var b [1]byte
	_, err := io.ReadFull(commit, b[:])
	commit.Close()
	if err != nil || b[0] != 1 {
		return err
	}
	// stdin/out/err are /dev/null; only the owning worker retires this anchor.
	for {
		time.Sleep(24 * time.Hour)
	}
}

func runChild(id string, input *os.File, output io.Writer) error {
	// A PTY must not echo a private ACK into agent output or retain canonical
	// input buffering. Restore its exact terminal settings before user code.
	saved, terminalErr := unix.IoctlGetTermios(int(input.Fd()), unix.TIOCGETA)
	if terminalErr == nil {
		raw := *saved
		raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
		raw.Oflag &^= unix.OPOST
		raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
		raw.Cflag &^= unix.CSIZE | unix.PARENB
		raw.Cflag |= unix.CS8
		raw.Cc[unix.VMIN] = 1
		raw.Cc[unix.VTIME] = 0
		if err := unix.IoctlSetTermios(int(input.Fd()), unix.TIOCSETA, &raw); err != nil {
			return err
		}
		defer unix.IoctlSetTermios(int(input.Fd()), unix.TIOCSETA, saved)
	}
	commitRead, commitWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer commitWrite.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		commitRead.Close()
		return err
	}
	defer readyRead.Close()
	binary, err := os.Executable()
	if err != nil {
		commitRead.Close()
		readyWrite.Close()
		return err
	}
	anchor := exec.Command(binary, "--managed-anchor")
	anchor.Env = BootstrapEnv()
	anchor.ExtraFiles = []*os.File{commitRead, readyWrite}
	if err = anchor.Start(); err != nil {
		commitRead.Close()
		readyWrite.Close()
		return err
	}
	commitRead.Close()
	readyWrite.Close()
	committed := false
	defer func() {
		if !committed {
			commitWrite.Close()
			anchor.Wait()
		}
	}()
	var ready [1]byte
	if _, err = io.ReadFull(readyRead, ready[:]); err != nil || ready[0] != 1 {
		return errors.New("anchor did not become ready")
	}
	packet := Registration{ID: id, Root: os.Getpid(), Anchor: anchor.Process.Pid}
	b, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(output, "%s%s\n", RegistrationPrefix, b); err != nil {
		return err
	}
	// A byte-at-a-time bounded read leaves all subsequent user stdin untouched.
	var line []byte
	for len(line) < 8<<20 {
		var one [1]byte
		if _, err := io.ReadFull(input, one[:]); err != nil {
			return err
		}
		ch := one[0]
		line = append(line, ch)
		if ch == '\n' {
			break
		}
	}
	var ack Acknowledgement
	if len(line) == 0 || line[len(line)-1] != '\n' || json.Unmarshal(line, &ack) != nil || ack.ID != id || len(ack.Argv) == 0 {
		return errors.New("managed launch was not acknowledged")
	}
	// Resolve using the target PATH, after acknowledgement. No target environment
	// or loader configuration is present in this bootstrap image.
	target := ack.Program
	if target == "" {
		target = ack.Argv[0]
	}
	if target == "" {
		return errors.New("managed launch omitted program")
	}
	os.Clearenv()
	for _, value := range ack.Env {
		name, value, ok := strings.Cut(value, "=")
		if !ok {
			return errors.New("invalid target environment")
		}
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	if !strings.ContainsRune(target, '/') {
		target, err = exec.LookPath(target)
		if err != nil {
			return err
		}
	}
	if !ack.PipeStdin && terminalErr != nil {
		null, err := os.Open(os.DevNull)
		if err != nil {
			return err
		}
		err = unix.Dup2(int(null.Fd()), 0)
		null.Close()
		if err != nil {
			return err
		}
	}
	if terminalErr == nil {
		if err = unix.IoctlSetTermios(int(input.Fd()), unix.TIOCSETA, saved); err != nil {
			return err
		}
	}
	if _, err = commitWrite.Write([]byte{1}); err != nil {
		return err
	}
	commitWrite.Close()
	committed = true
	return unix.Exec(target, ack.Argv, ack.Env)
}
