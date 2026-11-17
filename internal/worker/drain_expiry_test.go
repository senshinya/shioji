package worker

import (
	"encoding/json"
	"shioji/internal/domain"
	"shioji/internal/supervisor"
	"strings"
	"testing"
	"time"
)

func TestDrainExpiryCheckedAtAdmission(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "other"})
	s.config.ServiceInstance = "captured"
	body, _ := json.Marshal(map[string]any{"instance": "captured", "operationID": "expired", "expiresAt": time.Now().Add(-time.Second), "replacement": map[string]any{"policy": "immediate", "explicit": true}})
	out, err := s.serviceControl("worker.drain", body)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatal("expired control accepted", out, err)
	}
	if s.updating.Load() || s.drainOperation != "" {
		t.Fatal("expired control closed admission")
	}
}

func TestPrivateLifecycleImmediateRequiresExplicitAuthority(t *testing.T) {
	s, _ := memoryWorker(t, memoryHarness{id: "other"})
	s.config.ServiceInstance = "captured"
	body, _ := json.Marshal(map[string]any{"instance": "captured", "operationID": "implicit-stop", "replacement": map[string]any{"policy": "immediate", "explicit": false}})
	if _, err := s.serviceControl("worker.drain", body); err == nil {
		t.Fatal("implicit immediate drain bypassed authority")
	}
}

func TestPrivateLifecycleUsesBusinessAdmission(t *testing.T) {
	s, w := memoryWorker(t, memoryHarness{id: "other", hold: true})
	op, err := s.submit(domain.Submission{OperationID: "busy-private", WorkspaceID: w.ID, Text: "active", Config: domain.RunConfig{Model: "other-model", Permission: "limited"}})
	if err != nil {
		t.Fatal(err)
	}
	awaitReceipt(t, s, op.ID)
	s.config.ServiceInstance = "captured"
	out, err := s.DrainService(supervisor.Control{Instance: "captured", OperationID: "idle", Replacement: domain.WorkerReplacement{Policy: "when-idle"}})
	if err != nil || out.Ready || s.updating.Load() {
		t.Fatal("private control bypassed a live turn", out, err)
	}
	if s.currentRuntime(op.SessionID).Stopped() {
		t.Fatal("busy private control stopped agent")
	}
}
