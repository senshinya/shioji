package managedexec

import (
	"errors"
	"path/filepath"
)

// A newly accepted product update supersedes a legacy normal-only environment
// workflow. Its original manual receipt remains intact; this grant covers only
// the registered generation, never arbitrary descendant processes.
func (s *Scope) AuthorizeRetirement(operation string) error {
	if !safeID(operation) {
		return errors.New("invalid retirement operation")
	}
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	state.Retirement = operation
	state.Active = false
	state.Maintenance = false
	return writeJSON(filepath.Join(s.Directory, "scope.json"), state)
}
