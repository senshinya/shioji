package diagram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDiagramOutputCaptureRemainsBoundedDuringPipeCopy(t *testing.T) {
	var output limitedBuffer
	output.limit = 32
	_, err := io.Copy(&output, io.LimitReader(strings.NewReader(strings.Repeat("x", 4096)), 4096))
	if err != nil {
		t.Fatal(err)
	}
	if output.Len() > 32 || !output.exceeded {
		t.Fatal("pipe copy bypassed output limit", output.Len())
	}
}

func helperScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestDiagramClientRejectsInvalidVectorAndOversizedReplies(t *testing.T) {
	for _, svg := range []string{`<svg width="20" height="20"><text>lost text</text></svg>`, strings.Repeat("x", maxSVG+5000)} {
		reply, _ := json.Marshal(response{SVG: svg})
		file := filepath.Join(t.TempDir(), "reply.json")
		os.WriteFile(file, reply, 0600)
		helper := helperScript(t, "cat '"+file+"'\n")
		client := Client{Executable: helper, USVG: helper}
		if _, err := client.Render(context.Background(), Request{Source: "graph TD\n A --> B"}); err == nil {
			t.Fatal("accepted malformed or oversized helper output")
		}
	}
}
func TestDiagramCancellationKillsConverterDescendant(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	helper := helperScript(t, "sleep 30 &\necho $! > '"+pidFile+"'\nwait\n")
	client := Client{Executable: helper, USVG: helper}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.Render(ctx, Request{Source: "graph TD\n A --> B"}); done <- err }()
	deadline := time.After(10 * time.Second)
	var pid int
	for pid == 0 {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if pid != 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("helper child did not start")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-deadline:
		t.Fatal("cancel did not terminate helper")
	}
	for syscall.Kill(pid, 0) == nil {
		select {
		case <-deadline:
			t.Fatal("converter process survived cancellation", pid)
		case <-time.After(time.Millisecond):
		}
	}
}
