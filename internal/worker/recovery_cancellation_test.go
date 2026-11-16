package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/transport"
)

func TestCancelledDeviceRecoveryDoesNotStartFencingOrSuspendService(t *testing.T) {
	for _, owner := range []bool{false, true} {
		t.Run(fmt.Sprint("owner=", owner), func(t *testing.T) {
			dir, db := recoveryReviewDirectory(t)
			preview, err := InspectDeviceRecovery(context.Background(), dir, db.DeviceID())
			if err != nil {
				t.Fatal(err)
			}
			var snapshot recoveryPreviewRecord
			if err = recoveryRead(dir, "preview-"+preview.Ref, &snapshot); err != nil {
				t.Fatal(err)
			}
			r := deviceRecoveryRecord{Operation: domain.WorkerRecoveryOperation{ID: "cancel-before-run", DeviceID: db.DeviceID(), Phase: "accepted", Revision: 1, UpdatedAt: time.Now()}, Snapshot: snapshot, Sent: map[string]bool{}, SourceError: "must not start a replacement"}
			if owner {
				child := exec.Command("/bin/sleep", "60")
				if err = child.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { child.Process.Kill(); child.Wait() })
				processes, e := managedexec.Processes()
				if e != nil {
					t.Fatal(e)
				}
				for _, p := range processes {
					if p.Identity.PID == child.Process.Pid {
						r.Snapshot.Owner = p
					}
				}
				if r.Snapshot.Owner.Identity.PID == 0 {
					t.Fatal("missing fixture owner")
				}
				r.Service = fmt.Sprintf("gui/%d/dev.shioji.cancel-unregistered.%d", os.Getuid(), time.Now().UnixNano())
			}
			if err = recoveryWrite(dir, r.Operation.ID, r); err != nil {
				t.Fatal(err)
			}
			fence := transport.RecoveryFence{ID: r.Operation.ID, DeviceID: db.DeviceID()}
			if err = recoveryWrite(dir, "fence", fence); err != nil {
				t.Fatal(err)
			}
			if _, err = CancelDeviceRecovery(dir, r.Operation.ID, db.DeviceID()); err != nil {
				t.Fatal(err)
			}
			if _, err = RunDeviceRecovery(context.Background(), Config{Directory: dir}, r.Operation.ID); err != nil {
				t.Fatal("cancelled recovery performed work", err)
			}
			var result deviceRecoveryRecord
			if err = recoveryRead(dir, r.Operation.ID, &result); err != nil {
				t.Fatal(err)
			}
			if result.Operation.Phase != "cancelled" || result.OwnerPaused || result.ServiceSuspendPending || result.ServiceSuspended || len(result.Sent) > 0 {
				t.Fatal("cancelled action started shutdown", result.Operation, result.OwnerPaused, result.ServiceSuspendPending)
			}
			if _, err = os.Stat(filepath.Join(dir, "worker-recovery", "fence.json")); !os.IsNotExist(err) {
				t.Fatal("unstarted cancellation kept startup fenced", err)
			}
		})
	}
}

func TestDeviceRecoveryConsentRejectsNewChildInput(t *testing.T) {
	dir, db := recoveryReviewDirectory(t)
	w, err := db.CreateWorkspace("child consent", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: "parent", WorkspaceID: w.ID, Text: "parent work"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Apply(op.SessionID, domain.Event{Type: "work.upsert", Work: &domain.WorkActivity{ID: "child", Kind: "agent", TurnID: "child-turn", State: "completed", CanContinue: true}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := InspectDeviceRecovery(context.Background(), dir, db.DeviceID())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot recoveryPreviewRecord
	if err = recoveryRead(dir, "preview-"+preview.Ref, &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.AcceptWorkInput(domain.WorkInput{OperationID: "new-child-input", SessionID: op.SessionID, WorkID: "child", Mode: "continue", Text: "continue child"}); err != nil {
		t.Fatal(err)
	}
	if err = validateRecoveryConsent(context.Background(), dir, snapshot); err == nil {
		t.Fatal("new child input inherited old device recovery consent")
	}
}

func TestCancelledRecoveryDoesNotRaceAnExistingRunner(t *testing.T) {
	dir, db := recoveryReviewDirectory(t)
	runner := exec.Command("/bin/sleep", "60")
	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.Process.Kill(); runner.Wait() })
	processes, err := managedexec.Processes()
	if err != nil {
		t.Fatal(err)
	}
	r := deviceRecoveryRecord{Operation: domain.WorkerRecoveryOperation{ID: "running-cancel", DeviceID: db.DeviceID(), Phase: "fencing"}, Cancelled: true}
	for _, p := range processes {
		if p.Identity.PID == runner.Process.Pid {
			r.Runner = p
		}
	}
	if r.Runner.Identity.PID == 0 {
		t.Fatal("runner fixture absent")
	}
	if err = recoveryWrite(dir, r.Operation.ID, r); err != nil {
		t.Fatal(err)
	}
	if err = recoveryWrite(dir, "fence", transport.RecoveryFence{ID: r.Operation.ID, DeviceID: db.DeviceID()}); err != nil {
		t.Fatal(err)
	}
	if _, err = RunDeviceRecovery(context.Background(), Config{Directory: dir}, r.Operation.ID); err == nil {
		t.Fatal("second runner released an active runner's cancellation fence")
	}
	if fence, err := transport.PendingDeviceRecovery(dir); err != nil || fence == nil || fence.ID != r.Operation.ID {
		t.Fatal("active runner lost exclusive fence", fence, err)
	}
}

func TestNativeCancellationAfterFencingKeepsOriginalService(t *testing.T) {
	if os.Getenv("SHIOJI_RECOVERY_LAUNCHD_TEST") == "" {
		t.Skip("isolated launchd cancellation fixture")
	}
	dir, db := recoveryReviewDirectory(t)
	label := fmt.Sprintf("dev.shioji.recovery.cancel.%d", time.Now().UnixNano())
	service := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	plist := filepath.Join(dir, label+".plist")
	body := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>/bin/sleep</string><string>60</string></array><key>RunAtLoad</key><true/></dict></plist>`, label)
	if err := os.WriteFile(plist, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := nativeRecoveryServiceCommand(ctx, "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plist); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nativeRecoveryServiceCommand(context.Background(), "bootout", service) })
	var original managedexec.ProcessInfo
	for until := time.Now().Add(2 * time.Second); original.Identity.PID == 0; {
		registered, err := nativeRecoveryServiceCommand(ctx, "print", service)
		if err != nil {
			t.Fatal(err)
		}
		pid, _ := serviceObservation(registered)
		processes, err := managedexec.Processes()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range processes {
			if p.Identity.PID == pid {
				original = p
			}
		}
		if time.Now().After(until) {
			t.Fatal("fixture service owner absent")
		}
	}
	if err := managedexec.SignalExact(original, unix.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		out, err := exec.Command("/bin/ps", "-p", fmt.Sprint(original.Identity.PID), "-o", "state=").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	for until := time.Now().Add(time.Second); !strings.Contains(state(), "T"); {
		if time.Now().After(until) {
			t.Fatal("fixture did not pause")
		}
		time.Sleep(time.Millisecond)
	}
	r := deviceRecoveryRecord{Operation: domain.WorkerRecoveryOperation{ID: "cancel-fenced", DeviceID: db.DeviceID(), Phase: "fencing"}, Snapshot: recoveryPreviewRecord{Owner: original}, Sent: map[string]bool{}, OwnerPaused: true, Service: service, ServicePlist: plist, ServiceSuspendPending: true}
	if err := recoveryWrite(dir, r.Operation.ID, r); err != nil {
		t.Fatal(err)
	}
	if err := recoveryWrite(dir, "fence", transport.RecoveryFence{ID: r.Operation.ID, DeviceID: db.DeviceID()}); err != nil {
		t.Fatal(err)
	}
	if _, err := CancelDeviceRecovery(dir, r.Operation.ID, db.DeviceID()); err != nil {
		t.Fatal(err)
	}
	if _, err := RunDeviceRecovery(ctx, Config{Directory: dir}, r.Operation.ID); err != nil {
		t.Fatal(err)
	}
	registered, err := nativeRecoveryServiceCommand(ctx, "print", service)
	if err != nil {
		t.Fatal("cancelled recovery removed the service", err)
	}
	pid, _ := serviceObservation(registered)
	if pid != original.Identity.PID || strings.Contains(state(), "T") {
		t.Fatal("cancellation did not resume the original owner", pid, state())
	}
	var result deviceRecoveryRecord
	if err = recoveryRead(dir, r.Operation.ID, &result); err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != "cancelled" || result.ServiceSuspendPending || result.ServiceSuspended {
		t.Fatal("cancelled pending suspension was not released", result.Operation)
	}
	if fence, err := transport.PendingDeviceRecovery(dir); err != nil || fence != nil {
		t.Fatal("cancellation kept service startup fenced", fence, err)
	}
}
