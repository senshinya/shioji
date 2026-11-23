package app

import (
	"context"
	"errors"
	"testing"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/supervisor"
)

func TestSSHOwnedFixedHostUpgradePersistsRuntimeHandoffBeforeDispatch(t *testing.T) {
	u := workerUpgradeClient{directory: t.TempDir(), device: "device",
		knownBuild: &domain.WorkerBuild{BuildID: "old", Schema: 17, Lifecycle: 1}}
	candidate := &domain.WorkerBuild{BuildID: "next", Schema: 17, Lifecycle: 1, RuntimeHost: 1}
	u.legacy = func(_ context.Context, in domain.WorkerReplacement) (supervisor.Operation, error) {
		stored, err := u.load()
		if err != nil || stored == nil || stored.Input != in || !in.RuntimeHandoff || in.SchemaMigration {
			t.Fatal("Runtime ownership upgrade was not durably accepted before sending", stored, in, err)
		}
		return supervisor.Operation{}, errors.New("lost SSH response")
	}
	if _, err := u.update(context.Background(), nil, candidate, true, "when-idle", false); err == nil {
		t.Fatal("unknown receipt was reported as completed")
	}
	prior, err := u.load()
	if err != nil || prior == nil {
		t.Fatal(prior, err)
	}
	u.legacy = func(context.Context, domain.WorkerReplacement) (supervisor.Operation, error) {
		t.Fatal("unknown Runtime handoff was resent")
		return supervisor.Operation{}, nil
	}
	u.control = func(_ context.Context, action, id string) (supervisor.Operation, error) {
		if action != "status" || id != prior.Input.ID {
			t.Fatalf("wrong observation: %s %s", action, id)
		}
		return supervisor.Operation{DeviceID: "device", Input: prior.Input, Phase: "waiting"}, nil
	}
	state, err := u.update(context.Background(), nil, candidate, false, "when-idle", false)
	if err != nil || state.Phase != WorkerWaiting {
		t.Fatal("reconnection did not observe the original handoff", state, err)
	}
}

func TestRuntimeHandoffPreparationFailureIsDefiniteRejection(t *testing.T) {
	u := workerUpgradeClient{directory: t.TempDir(), device: "device",
		knownBuild: &domain.WorkerBuild{BuildID: "old", Schema: 17, Lifecycle: 1}}
	u.legacy = func(context.Context, domain.WorkerReplacement) (supervisor.Operation, error) {
		return supervisor.Operation{}, &protocol.RemoteError{Code: "notSent", Message: "Runtime unavailable"}
	}
	state, err := u.update(context.Background(), nil, &domain.WorkerBuild{BuildID: "next", Schema: 17, RuntimeHost: 1}, true, "when-idle", false)
	if err == nil || state.Phase != WorkerUpdateFailed {
		t.Fatal("failed preflight became an unknown in-flight operation", state, err)
	}
}
