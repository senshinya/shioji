package managedexec

import (
	"errors"
	"os"
	"path/filepath"
)

// Detach releases the current Agent slot, not its processes. Its fenced state
// and original launch identities remain available for independent cleanup.
func (s *Scope) Detach() error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	state.Active, state.Maintenance, state.Detached = false, false, true
	if err = writeJSON(filepath.Join(s.Directory, s.Generation, "scope.json"), state); err != nil {
		return err
	}
	return s.writeState(state)
}

func (s *Scope) ResolveDetached() error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	state.Detached = false
	if err = writeJSON(filepath.Join(s.Directory, s.Generation, "scope.json"), state); err != nil {
		return err
	}
	return s.writeState(state)
}

func (s *Scope) IsDetached() (bool, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	return state.Detached, err
}

func (m *Manager) Detached() ([]*Scope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	paths, err := filepath.Glob(filepath.Join(m.Directory, "*", "*", "scope.json"))
	if err != nil {
		return nil, err
	}
	var pending []*Scope
	for _, path := range paths {
		var state scopeState
		if err = readJSON(path, &state); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		generationDir := filepath.Dir(path)
		if filepath.Base(generationDir) != state.Generation || len(state.Generation) != 32 {
			return nil, errors.New("detached execution generation changed")
		}
		if state.Detached {
			dir := filepath.Dir(generationDir)
			pending = append(pending, &Scope{manager: m, Session: filepath.Base(dir), Generation: state.Generation, Directory: dir})
		}
	}
	return pending, nil
}
