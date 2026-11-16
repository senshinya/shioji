package store_test

import (
	"path/filepath"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/store"
)

type hookReceiptStore interface {
	PrepareHookOperation(domain.HookRequest) (domain.HookOperation, bool, error)
	HookOperation(domain.HookRequest) (domain.HookOperation, error)
	FinishHookOperation(domain.HookRequest, domain.OperationState, bool, string, bool) (domain.HookOperation, error)
}

func TestHookReceiptBindsContextAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.sqlite")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	receipts, ok := any(s).(hookReceiptStore)
	if !ok {
		t.Fatal("store does not persist hook operation receipts")
	}
	in := domain.HookRequest{HarnessID: "one", WorkspaceID: "workspace", HookChange: domain.HookChange{OperationID: "approval", ID: "opaque-hook", Revision: "catalog-1", DefinitionRevision: "definition-1", Action: domain.HookTrustDefinition}}
	first, fresh, err := receipts.PrepareHookOperation(in)
	if err != nil || !fresh || first.State != domain.OperationAccepted {
		t.Fatal(first, fresh, err)
	}
	if _, fresh, err = receipts.PrepareHookOperation(in); err != nil || fresh {
		t.Fatal("duplicate operation was accepted again", fresh, err)
	}
	for _, edit := range []func(*domain.HookRequest){func(r *domain.HookRequest) { r.HarnessID = "two" }, func(r *domain.HookRequest) { r.WorkspaceID = "other" }, func(r *domain.HookRequest) { r.DefinitionRevision = "newer" }} {
		other := in
		edit(&other)
		if _, _, err = receipts.PrepareHookOperation(other); err == nil {
			t.Fatal("operation identity accepted different approval context", other)
		}
	}
	if _, err = receipts.FinishHookOperation(in, domain.OperationConfirmed, true, "", false); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	op, err := any(reopened).(hookReceiptStore).HookOperation(in)
	if err != nil || !op.Saved || op.State != domain.OperationConfirmed || op.Input != in {
		t.Fatal("receipt was not durable", op, err)
	}
	late, err := any(reopened).(hookReceiptStore).FinishHookOperation(in, domain.OperationIndeterminate, false, "late timeout", false)
	if err != nil || !late.Saved || late.State != domain.OperationConfirmed {
		t.Fatal("late failure erased confirmed approval", late, err)
	}
}
