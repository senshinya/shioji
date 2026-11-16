package managedexec

// Persist the exact requested turn before sending cancellation. Startup can
// reconcile it without replaying either the input or the interrupt request.
func (s *Scope) RecordCancellation(turn string) error {
	s.manager.mu.Lock()
	defer s.manager.mu.Unlock()
	state, err := s.load()
	if err != nil {
		return err
	}
	state.CancelledTurn = turn
	return s.writeState(state)
}
