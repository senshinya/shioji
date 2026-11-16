package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"testing"
)

func TestUpdateJournalBindsOperationAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	j := journal{directory: dir}
	in := domain.WorkerReplacement{ID: "update-one", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	op, err := j.accept(in, "instance", "device")
	if err != nil || op.Phase != "preparing" {
		t.Fatal(op, err)
	}
	op.Phase = "switching"
	if err = j.save(op); err != nil {
		t.Fatal(err)
	}
	restarted := journal{directory: dir}
	if _, err = restarted.accept(domain.WorkerReplacement{ID: in.ID, ExpectedBuildID: "old", BuildID: "different", Policy: "when-idle"}, "instance", "device"); err == nil {
		t.Fatal("operation ID rebound to another target")
	}
	recovered, err := restarted.recover()
	if err != nil || recovered.Phase != "blocked" {
		t.Fatal("interrupted switching silently restarted writer", recovered, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "operations", "update-one.json")); err != nil {
		t.Fatal("per-ID receipt lost", err)
	}
}

func TestOfflineStatusNeverClaimsUnresumedAppliedCandidate(t *testing.T) {
	dir := t.TempDir()
	j := journal{directory: filepath.Join(dir, "supervisor")}
	in := domain.WorkerReplacement{ID: "offline", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	op, err := j.accept(in, "instance", "device")
	if err != nil {
		t.Fatal(err)
	}
	op.Phase = "applied"
	if err = j.save(op); err != nil {
		t.Fatal(err)
	}
	observed, err := Query(context.Background(), dir, in.ID)
	if err != nil || observed.Phase != "observing" {
		t.Fatal("offline journal published unconfirmed acceptance", observed, err)
	}
}
