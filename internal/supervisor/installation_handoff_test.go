package supervisor

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
	"shioji/internal/store"
)

// A bootstrap installer must leave a resumable receipt, not become the host
// that opens execution admission under an SSH responsibility chain.
func TestRuntimeResumesInstallationAfterInstallerReleasesControl(t *testing.T) {
	dir, options := regressionOptions(t)
	op := Operation{Legacy: true, DeviceID: "fixture-device", Phase: "installing", PreviousBuild: "old",
		Created: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour),
		TargetSchema: store.SchemaVersion,
		Input:        domain.WorkerReplacement{ID: "runtime-install", BuildID: "next", ExpectedBuildID: "old", Policy: "when-idle", Explicit: true, RuntimeHandoff: true}}
	if err := (journal{filepath.Join(dir, "supervisor")}).save(op); err != nil {
		t.Fatal(err)
	}
	regressionHost(t, options, "applied")
	selected, err := selectedBuild(filepath.Join(dir, "selected.json"))
	if err != nil || selected != "next" {
		t.Fatalf("Runtime did not commit the installation: selected=%q err=%v", selected, err)
	}
	status, err := Call(context.Background(), dir, Request{Action: "status", ID: op.Input.ID})
	if err != nil || status.Phase != "applied" {
		t.Fatalf("installation receipt was lost: %+v %v", status, err)
	}
}

func TestOldHostInstanceIsReadOnlyFromEnvironmentNotArguments(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		argc             uint32
	}{
		{"marker", "/worker\x00\x00/worker\x00--state-dir\x00/private/state\x00PATH=/bin\x00SHIOJI_WORKER_INSTANCE=opaque-instance\x00\x00", "opaque-instance", 3},
		{"argument-is-not-authority", "/worker\x00/worker\x00SHIOJI_WORKER_INSTANCE=argument\x00PATH=/bin\x00\x00", "", 2},
		{"truncated-argv", "/worker\x00/worker\x00", "", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := make([]byte, 4)
			binary.LittleEndian.PutUint32(raw, tc.argc)
			raw = append(raw, []byte(tc.body)...)
			if got := sourceInstanceFromProcargs(raw); got != tc.want {
				t.Fatalf("instance=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestInstallationHandoffDoesNotCommitSelectionOrOpenWriter(t *testing.T) {
	dir, _ := regressionOptions(t)
	lease, err := AcquireLease(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	op := Operation{Legacy: true, DeviceID: "fixture-device", Phase: "observing", PreviousBuild: "old",
		Input: domain.WorkerReplacement{ID: "handoff", BuildID: "next", ExpectedBuildID: "old", Policy: "when-idle"}}
	if err := InstallationHandoff(dir, LegacyInstall{Operation: op, Lease: lease}); err != nil {
		t.Fatal(err)
	}
	pending, ok, err := PendingInstallation(dir)
	if err != nil || !ok || pending.Input != op.Input || pending.HostStartDeadline.IsZero() {
		t.Fatal("handoff lost its accepted operation", pending, err)
	}
	for _, name := range []string{"selected.json", "started-next"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatal("installer started or selected a business writer", name, err)
		}
	}
	if other, err := AcquireLease(dir); err == nil {
		other.Close()
		t.Fatal("handoff released control before native Runtime launch")
	}
}

func TestInstallationTimeoutRemainsResumableWithoutStatusLaunchingAnything(t *testing.T) {
	dir, options := regressionOptions(t)
	op := Operation{Legacy: true, DeviceID: "fixture-device", Phase: "installing", PreviousBuild: "old", TargetSchema: store.SchemaVersion,
		HostStartDeadline: time.Now().Add(-time.Minute),
		Input:             domain.WorkerReplacement{ID: "interrupted-launch", BuildID: "next", ExpectedBuildID: "old", Policy: "when-idle", RuntimeHandoff: true}}
	if err := (journal{filepath.Join(dir, "supervisor")}).save(op); err != nil {
		t.Fatal(err)
	}
	status, err := Query(context.Background(), dir, op.Input.ID)
	if err != nil || status.Phase != "blocked" {
		t.Fatal("missing Runtime remained an unlimited spinner", status, err)
	}
	if pending, ok, err := PendingInstallation(dir); err != nil || !ok || pending.Input != op.Input {
		t.Fatal("status destroyed the resumable handoff", pending, err)
	}
	regressionHost(t, options, "applied")
}
