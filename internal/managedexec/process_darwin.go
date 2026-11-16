package managedexec

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"syscall"
)

// StartOwned registers the process and its anchor before executing even the
// harness/executor image. Their trusted helper children inherit this group.
func (s *Scope) StartOwned(ctx context.Context, ref string, argv, env []string, onStart func(int) error) (*exec.Cmd, io.WriteCloser, io.Reader, error) {
	ticket, err := s.Prepare("worker", ref)
	if err != nil {
		return nil, nil, nil, err
	}
	cmd := exec.Command(s.manager.Executable, "--managed-exec", ticket.ID)
	cmd.Env = BootstrapEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	successful := false
	defer func() {
		if !successful {
			stdin.Close()
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	cancelRead := context.AfterFunc(ctx, func() { cmd.Process.Kill() })
	defer cancelRead()
	if onStart != nil {
		if err = onStart(cmd.Process.Pid); err != nil {
			return nil, nil, nil, err
		}
	}
	reader := bufio.NewReaderSize(stdout, 4096)
	packet, err := reader.ReadSlice('\n')
	if err != nil {
		return nil, nil, nil, err
	}
	if !strings.HasPrefix(string(packet), RegistrationPrefix) {
		return nil, nil, nil, errors.New("managed bootstrap did not register")
	}
	var registration Registration
	if json.Unmarshal(packet[len(RegistrationPrefix):], &registration) != nil || registration.ID != ticket.ID || registration.Root != cmd.Process.Pid {
		return nil, nil, nil, errors.New("managed bootstrap identity differs")
	}
	if err = s.Register(registration); err != nil {
		return nil, nil, nil, err
	}
	if err = json.NewEncoder(stdin).Encode(Acknowledgement{ID: ticket.ID, Argv: argv, Env: env, PipeStdin: true}); err != nil {
		return nil, nil, nil, err
	}
	successful = true
	return cmd, stdin, reader, nil
}
