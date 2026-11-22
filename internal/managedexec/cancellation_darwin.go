package managedexec

// Persist the requested turn or its input reference before sending cancellation.
func (s *Scope) RecordCancellation(ref string) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	state.CancelledTurn = ref
	return s.writeState(state)
}

func (s *Scope) CancelledTurn() (string, error) {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	return state.CancelledTurn, err
}
