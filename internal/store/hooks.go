package store

import (
	"database/sql"
	"errors"

	"shioji/internal/domain"
)

func hookOperation(q interface{ QueryRow(string, ...any) *sql.Row }, in domain.HookRequest) (domain.HookOperation, error) {
	op, err := load[domain.HookOperation](q.QueryRow("SELECT body FROM hook_operations WHERE id=?", in.OperationID))
	if err == nil && !op.Input.Equal(in) {
		return domain.HookOperation{}, errors.New("Hook 操作标识已用于其他请求")
	}
	return op, err
}

func (s *Store) HookOperation(in domain.HookRequest) (domain.HookOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return hookOperation(s.db, in)
}

func (s *Store) PrepareHookOperation(in domain.HookRequest) (domain.HookOperation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.OperationID == "" || len(in.OperationID) > 128 || in.HarnessID == "" || !in.HookChange.Valid() {
		return domain.HookOperation{}, false, errors.New("Hook 操作请求无效")
	}
	if op, err := hookOperation(s.db, in); !errors.Is(err, sql.ErrNoRows) {
		return op, false, err
	}
	op := domain.HookOperation{Input: in, State: domain.OperationAccepted}
	_, err := s.db.Exec("INSERT INTO hook_operations(id,body) VALUES(?,?)", in.OperationID, blob(op))
	return op, err == nil, err
}

func (s *Store) FinishHookOperation(in domain.HookRequest, state domain.OperationState, saved bool, message string, adoptionPending bool) (domain.HookOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := hookOperation(s.db, in)
	if err != nil {
		return op, err
	}
	if op.State == domain.OperationConfirmed || op.State == domain.OperationRejected {
		return op, nil
	}
	if state != domain.OperationConfirmed && state != domain.OperationRejected && state != domain.OperationIndeterminate || saved != (state == domain.OperationConfirmed) {
		return op, errors.New("Hook 操作结果无效")
	}
	op.State, op.Saved, op.Message, op.AdoptionPending = state, saved, message, adoptionPending
	_, err = s.db.Exec("UPDATE hook_operations SET body=? WHERE id=?", blob(op), in.OperationID)
	return op, err
}
