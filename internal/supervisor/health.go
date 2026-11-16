package supervisor

import (
	"errors"
	"os"
	"path/filepath"
)

// Service health outlives root restarts without rewriting historical receipts.
type serviceHealth struct {
	Phase, DeviceID, BuildID, Error string
	Schema, Attempts                int
}

func (h *host) healthPath() string {
	return filepath.Join(h.options.Directory, "supervisor", "health.json")
}
func (h *host) loadHealth() error {
	err := readJSON(h.healthPath(), &h.health)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Caller holds h.mu once control has started.
func (h *host) saveHealth() error { return atomicJSON(h.healthPath(), h.health) }
func (h *host) blockHealth(cause error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blocked, h.readyDone = true, false
	h.health.Phase, h.health.Error = "blocked", cause.Error()
	if h.device != "" {
		h.health.DeviceID = h.device
	}
	if h.child != nil {
		h.health.BuildID, h.health.Schema = h.child.build.BuildID, h.child.build.Schema
	}
	return h.saveHealth()
}
