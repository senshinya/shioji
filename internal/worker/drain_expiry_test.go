package worker

import (
	"encoding/json"
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
