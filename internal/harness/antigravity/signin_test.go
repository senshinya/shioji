package antigravity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

func TestTerminalSignInAcceptsOneCodeOnTheOwningProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agy")
	script := `#!/bin/sh
if [ "$1" != acp ] || [ "$2" != --login ]; then exit 1; fi
printf '%s\n' 'https://accounts.google.com/o/oauth2/v2/auth?state=fixture'
IFS= read -r code
[ "$code" = 'fixture-authorization-code' ]
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	a := Adapter{Executable: path}
	provider, ok := any(a).(harness.AuthenticationProvider)
	if !ok {
		t.Fatal("AGY does not expose device sign-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	login, challenge, err := provider.BeginSignIn(ctx, domain.SignInBrowser)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Cancel(context.Background())
	if challenge.URL == "" || !challenge.ManualCode {
		t.Fatalf("authorization challenge missing: %+v", challenge)
	}
	responder := login.(harness.SignInCodeReceiver)
	if err := responder.SubmitCode(ctx, "invalid\nsecond"); err == nil {
		t.Fatal("accepted a multiline native terminal payload")
	}
	if err := responder.SubmitCode(ctx, "fixture-authorization-code"); err != nil {
		t.Fatal(err)
	}
	if err := responder.SubmitCode(ctx, "replay"); err == nil {
		t.Fatal("replayed authorization to the same native process")
	}
	_, err = login.Wait(ctx)
	var setup *harness.SetupError
	if !errors.As(err, &setup) || setup.Account == nil || setup.Account.State != "authenticated" {
		t.Fatalf("catalog failure erased the verified login: %v", err)
	}
}

func TestTerminalSignInCancelStopsTheNativeProcessGroup(t *testing.T) {
	dir := t.TempDir()
	path, pidFile := filepath.Join(dir, "agy"), filepath.Join(dir, "child")
	script := `#!/bin/sh
/bin/sleep 30 &
printf '%s' "$!" > ` + strconv.Quote(pidFile) + `
printf '%s\n' 'https://accounts.google.com/o/oauth2/v2/auth?state=fixture'
wait
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	login, _, err := (Adapter{Executable: path}).BeginSignIn(ctx, domain.SignInBrowser)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Cancel(context.Background())
	raw, err := os.ReadFile(pidFile)
	pid, parseErr := strconv.Atoi(string(raw))
	if err != nil || parseErr != nil {
		t.Fatalf("fixture child is unavailable: %v %v", err, parseErr)
	}
	if err := login.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native authorization child survived cancellation")
}
