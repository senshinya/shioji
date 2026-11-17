package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestExpiredIntentCannotSwitchAfterSlowIdleQuery(t *testing.T) {
	dir, options := regressionOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &host{ctx: ctx, options: options, journal: journal{filepath.Join(dir, "supervisor")}, instance: "root", wake: make(chan struct{}, 1)}
	if err := h.startSelected(options.InitialPath, false, ""); err != nil {
		t.Fatal(err)
	}
	old := h.child
	defer func() {
		_ = h.stopExact(old, 100*time.Millisecond)
		if h.child != old {
			_ = h.stopExact(h.child, 100*time.Millisecond)
		}
	}()
	in := domain.WorkerReplacement{ID: "expired", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	op, err := h.journal.accept(in, h.instance, h.device)
	if err != nil {
		t.Fatal(err)
	}
	// Decode the new durable field before implementation exists (RED).
	raw, _ := json.Marshal(op)
	var body map[string]any
	json.Unmarshal(raw, &body)
	expiresAt := time.Now().Add(2 * time.Second).UTC()
	body["expiresAt"] = expiresAt
	raw, _ = json.Marshal(body)
	json.Unmarshal(raw, &op)
	if err = h.journal.save(op); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "hold-prepare-drain"), []byte("wait"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { h.update(ctx, op, old); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(dir, "prepare-drain-entered")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle query never reached")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(time.Until(expiresAt) + 20*time.Millisecond)
	os.Remove(filepath.Join(dir, "hold-prepare-drain"))
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("expired operation did not settle")
	}
	result, err := h.journal.load(op.Input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != "failed" || !strings.Contains(result.Error, "expired") {
		t.Fatal("expired intent switched", result.Phase, result.Error)
	}
	if h.child != old {
		t.Fatal("expired intent replaced old process")
	}
}
func TestExpiryIsDurableAndDuplicateIntentKeepsDeadline(t *testing.T) {
	dir, _ := regressionOptions(t)
	j := journal{filepath.Join(dir, "supervisor")}
	in := domain.WorkerReplacement{ID: "deadline", ExpectedBuildID: "old", BuildID: "next", Policy: "when-idle"}
	first, err := j.accept(in, "root", "fixture-device")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(first)
	var body map[string]any
	json.Unmarshal(raw, &body)
	if body["expiresAt"] == nil {
		t.Fatal("accepted intent has no absolute expiry")
	}
	next, err := j.accept(in, "root", "fixture-device")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(next)
	if string(a) != string(b) {
		t.Fatal("duplicate renewed accepted deadline")
	}
}
