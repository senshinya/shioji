package transport

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSSHRejectsOptionAndCommandInjection(t *testing.T) {
	for _, target := range []string{"", "-oProxyCommand=touch /tmp/injected", "host\ncommand", "user@host;command", "user@host$(whoami)"} {
		if _, err := (SSH{Target: target}).command(context.Background(), "true"); err == nil {
			t.Fatal("unsafe target accepted", target)
		}
	}
}
func TestSSHArgumentsPreserveConfigAndRequireHostTrust(t *testing.T) {
	s := SSH{Target: "my-mac", Port: 2200}
	cmd, err := s.command(context.Background(), shellQuote("/Users/a b/worker's binary")+" --bridge")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, "\n")
	for _, required := range []string{"StrictHostKeyChecking=yes", "BatchMode=yes", "ForwardAgent=no", "--\nmy-mac", "-p\n2200"} {
		if !strings.Contains(args, required) {
			t.Fatal("SSH protection/target missing", args)
		}
	}
	if strings.Contains(args, "-F\n") || strings.Contains(args, "StrictHostKeyChecking=no") {
		t.Fatal("user configuration or host trust bypassed", args)
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != "'/Users/a b/worker'\\''s binary' --bridge" {
		t.Fatal("worker path is not shell-quoted", got)
	}
}
func TestProcessTransportCarriesBytesAndCancels(t *testing.T) {
	if os.Getenv("SHIOJI_TRANSPORT_HELPER") == "1" {
		io.Copy(os.Stdout, os.Stdin)
		os.Exit(0)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestProcessTransportCarriesBytesAndCancels$")
	cmd.Env = append(os.Environ(), "SHIOJI_TRANSPORT_HELPER=1")
	conn, err := dialProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Include helper startup on a loaded CI runner; cancellation below still
	// has to close the transport rather than relying on an expired deadline.
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	payload := []byte{0, 255, 1, 10, 13, 0}
	done := make(chan error, 1)
	go func() { _, e := conn.Write(payload); done <- e }()
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply, payload) {
		t.Fatal("process transport corrupts protocol", reply)
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	cancel()
	if _, err := conn.Read(reply); err == nil {
		t.Fatal("canceled SSH process remains readable")
	}
}

func TestProcessTransportHalfCloseKeepsFinalResponse(t *testing.T) {
	conn, err := dialProcess(exec.Command("/bin/sh", "-c", "data=$(/bin/cat); /usr/bin/printf 'reply:%s' \"$data\""))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("finished input"))
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(conn)
	if err != nil || string(got) != "reply:finished input" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestProcessTransportCloseAlsoStopsOwnedProxyChildren(t *testing.T) {
	conn, err := dialProcess(exec.Command("/bin/sh", "-c", "/bin/sleep 60 & /usr/bin/printf '%s\\n' \"$!\"; wait"))
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	done := make(chan struct{})
	go func() { conn.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		syscall.Kill(pid, syscall.SIGKILL)
		<-done
		t.Fatal("SSH close waited for an owned proxy child")
	}
	if output, _ := exec.Command("/bin/ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output(); strings.TrimSpace(string(output)) != "" && !strings.HasPrefix(strings.TrimSpace(string(output)), "Z") {
		t.Fatal("owned proxy remains running after close", string(output))
	}
}
