package store

import (
	"database/sql"
	"errors"

	"shioji/internal/domain"
)

func receivedSubmission(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (domain.Operation, error) {
	op := domain.Operation{ID: id, Kind: ordinarySubmissionKind, State: domain.OperationPreparing}
	var pending bool
	err := q.QueryRow("SELECT digest,input IS NOT NULL,error FROM received_submissions WHERE id=?", id).Scan(&op.Digest, &pending, &op.Error)
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
	message := clipEventText(cause, 32<<10)
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
