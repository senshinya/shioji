package store

import (
	"database/sql"
	"errors"

	"shioji/internal/domain"
)

func receivedSubmission(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (domain.Operation, error) {
	op := domain.Operation{ID: id, Kind: ordinarySubmissionKind, State: domain.OperationPreparing}
	var pending bool
	err := q.QueryRow(`SELECT digest,input IS NOT NULL,error,COALESCE(json_extract(input,'$.preparation'),'')
		FROM received_submissions WHERE id=?`, id).Scan(&op.Digest, &pending, &op.Error, &op.Preparation)
	if err != nil {
		return domain.Operation{}, err
	}
	if !pending {
		op.State = domain.OperationRejected
		// A crash may have interrupted a shared checkout switch.
		if warning, err := readSubmissionGit(q, id); err != nil {
			return domain.Operation{}, err
		} else if warning != "" && op.Error == "准备会话期间执行服务重启，消息未发送" {
			op.Error = warning
		}
	}
	return op, nil
}

// Keep the phase with the pending receipt so the event and a reconnect lookup
// report the same preparation. Accepting or rejecting the input clears it.
func (s *Store) MarkSubmissionPreparation(id string, phase domain.SubmissionPreparation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	op := domain.Operation{Kind: ordinarySubmissionKind, State: domain.OperationPreparing, Preparation: phase}
	err = tx.QueryRow(`UPDATE received_submissions SET input=json_set(input,'$.preparation',?)
		WHERE id=? AND input IS NOT NULL AND json_extract(input,'$.preparation') IS NULL RETURNING id`,
		phase, id).Scan(&op.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Older clients and scheduled sends do not reserve a home receipt.
	}
	if err != nil {
		return err
	}
	if _, err = journal(tx, domain.Event{Type: "operation", Operation: &op}); err != nil {
		return err
	}
	return s.commit(tx)
}

func (s *Store) ReceiveHomeSubmission(in domain.Submission) (domain.Operation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	digest := submissionDigest(in)
	op, err := operation(s.db, in.OperationID)
	if err == nil {
		if op.Digest != digest {
			return domain.Operation{}, false, errors.New("operation ID already used with different input")
		}
		return op, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.Operation{}, false, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO received_submissions(id,digest,input) VALUES(?,?,?)", in.OperationID, digest, blob(in)); err != nil {
		return domain.Operation{}, false, err
	}
	op = domain.Operation{ID: in.OperationID, Kind: ordinarySubmissionKind, State: domain.OperationPreparing}
	if _, err = journal(tx, domain.Event{Type: "operation", Operation: &op}); err != nil {
		return domain.Operation{}, false, err
	}
	if err = s.commit(tx); err != nil {
		return domain.Operation{}, false, err
	}
	return op, true, nil
}

func (s *Store) RejectReceivedSubmission(id, cause string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	message := domain.ClipDisplayText(cause, 32<<10)
	result, err := tx.Exec("UPDATE received_submissions SET input=NULL,error=? WHERE id=? AND input IS NOT NULL", message, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	op := domain.Operation{ID: id, Kind: ordinarySubmissionKind, State: domain.OperationRejected, Error: message}
	if _, err = journal(tx, domain.Event{Type: "operation", Operation: &op}); err != nil {
		return err
	}
	return s.commit(tx)
}
