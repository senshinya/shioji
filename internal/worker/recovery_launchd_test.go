package worker

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"shioji/internal/domain"
	"shioji/internal/managedexec"
	"shioji/internal/runtimebuild"
	"shioji/internal/transport"
	"strings"
	"testing"
	"time"
)

// This opt-in integration uses only a unique test launchd label and private
// state. It never controls the installed Shioji execution/network services.
func TestNativeRecoveryRetryRestoresOriginalBuildAndRegistration(t *testing.T) {
	if os.Getenv("SHIOJI_RECOVERY_LAUNCHD_TEST") == "" {
		t.Skip("isolated launchd integration")
	}
	t.Setenv("SHIOJI_RECOVERY_FIXTURE", "1")
	dir, db := recoveryReviewDirectory(t)
	device := db.DeviceID()
	db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	label := fmt.Sprintf("dev.shioji.recovery.retry.%d", time.Now().UnixNano())
	service := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	plist := filepath.Join(dir, label+".plist")
	escape := func(value string) string {
		var out strings.Builder
		xml.EscapeText(&out, []byte(value))
		return out.String()
	}
	body := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>%s</string><string>--state-dir</string><string>%s</string><string>--service-fixture</string><string>yes</string></array><key>EnvironmentVariables</key><dict><key>SHIOJI_RECOVERY_FIXTURE</key><string>1</string></dict><key>RunAtLoad</key><true/></dict></plist>`, label, escape(os.Args[0]), escape(dir))
	if err := os.WriteFile(plist, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture-hung"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		nativeRecoveryServiceCommand(context.Background(), "bootout", service)
		if owner, e := recoveryOwner(dir); e == nil {
			managedexec.SignalExact(owner, unix.SIGKILL)
		}
	})
	if _, err := nativeRecoveryServiceCommand(ctx, "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plist); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(3 * time.Second); ; {
		if _, err := recoveryOwner(dir); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("hung fixture owner did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	preview, err := InspectDeviceRecovery(ctx, dir, device)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot recoveryPreviewRecord
	if err = recoveryRead(dir, "preview-"+preview.Ref, &snapshot); err != nil {
		t.Fatal(err)
	}
	originalDigest, err := runtimebuild.Digest(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	r := deviceRecoveryRecord{Operation: domain.WorkerRecoveryOperation{ID: "first-native", DeviceID: device, Phase: "accepted", Revision: 1, UpdatedAt: time.Now()}, Input: domain.WorkerRecoveryInput{ID: "first-native", DeviceID: device, Action: "restart", RiskAccepted: true}, Snapshot: snapshot, Sent: map[string]bool{}, SourceBinary: os.Args[0], SourceDigest: originalDigest, Service: service, ServicePlist: plist}
	pinRecoverySource(ctx, dir, &r)
	if r.SourceError != "" {
		t.Fatal(r.SourceError)
	}
	for _, p := range preview.Processes {
		if !p.Protected && p.Evidence != "unknown" {
			r.Operation.Results = append(r.Operation.Results, domain.WorkerRecoveryResult{Process: p, State: "pending"})
		}
	}
	// Inject a database-write failure after launchd suspension and exact closure.
	fault, err := sql.Open("sqlite", filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fault.Exec("ALTER TABLE queues RENAME COLUMN body TO unavailable"); err != nil {
		fault.Close()
		t.Fatal(err)
	}
	fault.Close()
	if err = recoveryWrite(dir, r.Operation.ID, r); err != nil {
		t.Fatal(err)
	}
	fence := transport.RecoveryFence{ID: r.Operation.ID, DeviceID: device}
	recoveryWrite(dir, "fence", fence)
	recoveryWrite(dir, "active", fence)
	cmd := exec.CommandContext(ctx, os.Args[0], "--state-dir", dir, "--recovery-helper", "run", "--recovery-id", r.Operation.ID, "--recovery-device", device)
	if err = cmd.Run(); err == nil {
		t.Fatal("injected write failure did not interrupt recovery")
	}
	first, err := DeviceRecoveryStatus(dir, r.Operation.ID)
	if err != nil || first.Phase != "partial" {
		t.Fatal("failure lost partial receipt", first, err)
	}
	var persisted deviceRecoveryRecord
	if err = recoveryRead(dir, r.Operation.ID, &persisted); err != nil || !persisted.ServiceSuspended {
		t.Fatal("service suspension not journaled", err)
	}
	fault, err = sql.Open("sqlite", filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fault.Exec("ALTER TABLE queues RENAME COLUMN unavailable TO body"); err != nil {
		fault.Close()
		t.Fatal(err)
	}
	fault.Close()
	os.Remove(filepath.Join(dir, "fixture-hung"))
	// A differently signed helper must restore the original build, not itself.
	helper := filepath.Join(dir, "new-helper")
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(helper, binary, 0700); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.CommandContext(ctx, "/usr/bin/codesign", "--force", "--sign", "-", "--identifier", "dev.shioji.recovery.test-helper", helper).CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	newDigest, err := runtimebuild.Digest(helper)
	if err != nil || newDigest == originalDigest {
		t.Fatal("new helper identity not distinct", err)
	}
	next, err := InspectDeviceRecovery(ctx, dir, device)
	if err != nil {
		t.Fatal(err)
	}
	op, err := StartDeviceRecovery(ctx, dir, helper, domain.WorkerRecoveryInput{ID: "second-native", ActionID: "retry-native", Action: "restart", DeviceID: device, PreviewRef: next.Ref, RiskAccepted: true})
	if err != nil {
		t.Fatal(err)
	}
	for op.Active() {
		if ctx.Err() != nil {
			t.Fatal("restoration timed out", op)
		}
		time.Sleep(25 * time.Millisecond)
		op, err = DeviceRecoveryStatus(dir, op.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if op.Phase != "completed" || op.BuildID != originalDigest {
		t.Fatal("retry upgraded without consent or failed restoration", op)
	}
	owner, err := recoveryOwner(dir)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := nativeRecoveryServiceCommand(ctx, "print", service)
	if err != nil {
		t.Fatal("original service registration not restored", err)
	}
	pid, _ := serviceObservation(registered)
	if pid != owner.Identity.PID {
		t.Fatal("restored owner is detached from the original registration")
	}
}
