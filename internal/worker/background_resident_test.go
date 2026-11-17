package worker

import (
	"bufio"
	"context"
	"os/exec"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestBackgroundDisableIdleResidentNode(t *testing.T) {
	for _, outcome := range []string{"notRegistered", "unavailable", "active-native-work"} {
		t.Run(outcome, func(t *testing.T) {
			s, w := memoryWorker(t, idleHarness{})
			d := submitIdleFixture(t, s, w, "resident", "")
			runtime := s.currentRuntime(d.Session.ID)
			scope, err := s.executions.Existing(d.Session.ID)
			if err != nil {
				t.Fatal(err)
			}
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			defer cancel()
			script := `require('node:child_process').spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'ignore'});console.log('ready');process.stdin.resume();setInterval(()=>{},1000)`
			cmd, in, out, err := scope.StartOwned(ctx, "worker", []string{node, "-e", script}, []string{"PATH=/opt/homebrew/bin:/usr/bin:/bin"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { scope.Stop(context.Background()); in.Close(); cmd.Wait() }()
			if _, err = bufio.NewReader(out).ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			if active, err := s.store.HasActiveWork(d.Session.ID); err != nil || active {
				t.Fatal("fixture not idle", active, err)
			}
			if outcome == "active-native-work" {
				if _, err = s.store.Apply(d.Session.ID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "native", TurnID: "turn/resident", State: "running", Kind: "background", OriginRef: d.Session.ThreadID}}); err != nil {
					t.Fatal(err)
				}
			}
			s.build.Managed = true
			called := false
			s.config.DisableBackground = func(context.Context, string) (domain.BackgroundService, error) {
				called = true
				if scope.Admitting() {
					t.Error("new tool admission remains open during unregister")
				}
				return domain.BackgroundService{Format: 1, State: outcome}, nil
			}
			result, err := s.disableBackground()
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "active-native-work" {
				if called || result.Message == "" || !scope.Admitting() {
					t.Fatal("native work lost protection/admission", called, result)
				}
				return
			}
			if !called || result.State != outcome {
				t.Fatal("idle resident Node blocked owned unregister", called, result)
			}
			if outcome == "notRegistered" {
				if scope.Admitting() || !s.backgroundStopping.Load() {
					t.Fatal("stop did not retain admission fence")
				}
			} else {
				if !scope.Admitting() || s.backgroundStopping.Load() || s.currentRuntime(d.Session.ID) != runtime || runtime.Stopped() {
					t.Fatal("unavailable unregister did not restore same live runtime")
				}
				next, err := s.submit(domain.Submission{OperationID: "after-unavailable", SessionID: d.Session.ID, WorkspaceID: w.ID, Text: "continue", Config: domain.RunConfig{Model: "idle-model", Permission: "limited"}})
				if err != nil {
					t.Fatal("known non-operation retained admission fence", err)
				}
				awaitReceipt(t, s, next.ID)
			}
		})
	}
}
