package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"shioji/internal/domain"
)

const goalOperationKind = "goal"

func goalDigest(in domain.GoalRequest) string {
	digest := sha256.Sum256(blob(in))
	return hex.EncodeToString(digest[:])
}

func (s *Store) GoalOperation(in domain.GoalRequest) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := operation(s.db, in.OperationID)
	if err == nil && (op.Kind != goalOperationKind || op.SessionID != in.SessionID || op.Digest != goalDigest(in)) {
		return domain.Operation{}, errors.New("目标操作与原请求不一致")
	}
	return op, err
}

// AcceptGoal records a single, exact mutation before the worker hands it to a
// native owner. The source snapshot can advance its metrics before acceptance.
func (s *Store) AcceptGoal(in domain.GoalRequest, source string) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var op domain.Operation
	tx, err := s.db.Begin()
	if err != nil {
		return op, err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", in.SessionID))
	if err != nil {
		return op, err
	}
	if session.Archived || session.ThreadID != source || !session.Goal.SameTarget(&in.Target) {
		return op, errors.New("目标已改变，请重新核对")
	}
	op = domain.Operation{ID: in.OperationID, SessionID: in.SessionID, Kind: goalOperationKind, State: domain.OperationDispatched, Digest: goalDigest(in)}
	if _, err = tx.Exec("INSERT INTO operations(id,session_id,state,digest,kind) VALUES(?,?,?,?,?)", op.ID, op.SessionID, op.State, op.Digest, op.Kind); err != nil {
		return op, err
	}
	if _, err = journal(tx, domain.Event{Type: "operation", SessionID: op.SessionID, Operation: &op}); err != nil {
		return op, err
	}
	return op, s.commit(tx)
}

// RecordGoalReceipt is a fallback for a native acknowledgement arriving before
// the event stream. Never replace a newer snapshot already observed there.
func (s *Store) RecordGoalReceipt(id, source string, before, after *domain.Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return err
	}
	if session.ThreadID != source || !equalGoal(session.Goal, before) {
		return nil
	}
	_, err = s.applyLocked(id, domain.Event{Type: "goal", GoalSource: source, Goal: after})
	return err
}

func equalGoal(a, b *domain.Goal) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
