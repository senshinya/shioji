package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"shioji/internal/domain"
)

const (
	ordinarySubmissionKind = "submit"
	steerSubmissionKind    = "steer"
)

var ErrInputHandoffRevoked = errors.New("输入已停止或已经交接")

func unhanded(op domain.Operation) bool {
	return op.State == domain.OperationAccepted && (op.Kind == ordinarySubmissionKind || op.Kind == steerSubmissionKind)
}

// Validate the display copy before preparing a checkout or other external state.
func CheckSubmissionJournal(in domain.Submission) error {
	item := domain.Item{Text: in.Text, Attachments: in.Attachments, Skills: in.Skills, ReferencePositions: in.ReferencePositions}
	if len(blob(item)) > 448<<10 {
		return errors.New("event exceeds recoverable page budget")
	}
	return nil
}

func clipEventText(text string, limit int) string {
	if len(blob(text)) <= limit {
		return text
	}
	const suffix = "\n[Output limit reached]"
	low, high := 0, len(text)
	for low < high {
		mid := low + (high-low+1)/2
		if len(blob(text[:mid]+suffix)) <= limit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	for low < len(text) && !utf8.RuneStart(text[low]) {
		low--
	}
	return strings.ToValidUTF8(text[:low], "�") + suffix
}

// A shared checkout is an external mutation. Keep its intent until the
// input transaction commits so a restart can explain a rejected submission.
func (s *Store) RecordSubmissionGit(id, checkout, original, branch string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("INSERT INTO submission_git_preparation (operation_id,checkout,original_branch,branch) VALUES(?,?,?,?)", id, checkout, original, branch)
	return err
}

func (s *Store) SubmissionGitWarning(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readSubmissionGit(s.db, id)
}

func readSubmissionGit(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (string, error) {
	var checkout, original, branch string
	err := q.QueryRow("SELECT checkout,original_branch,branch FROM submission_git_preparation WHERE operation_id=?", id).Scan(&checkout, &original, &branch)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if original == "" {
		original = "分离 HEAD"
	}
	return fmt.Sprintf("输入未接纳，目录“%s”可能已从“%s”切换到“%s”。请核对当前分支后重新提交。", checkout, original, branch), nil
}

// SealUnreceivedInput returns any existing receipt, or records a terminal
// rejection. Older workers also honor the operation tombstone.
func (s *Store) SealUnreceivedInput(id string) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := operation(s.db, id)
	if !errors.Is(err, sql.ErrNoRows) {
		return op, err
	}
	if _, err = s.db.Exec("INSERT INTO deleted_operations(id,session_id) VALUES(?, '')", id); err != nil {
		return op, err
	}
	return operation(s.db, id)
}

func (s *Store) BindPreparedInput(id string, session domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := operation(s.db, id)
	if err != nil {
		return err
	}
	if op.State != domain.OperationAccepted || op.SessionID != session.ID {
		return ErrInputHandoffRevoked
	}
	_, err = s.applyLocked(session.ID, domain.Event{Type: "session", Session: &session})
	return err
}

// The durable claim and Stop serialize under the same store lock.
func (s *Store) BeginInputHandoff(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	op, err := operation(tx, id)
	if err != nil {
		return err
	}
	if op.State != domain.OperationAccepted {
		return ErrInputHandoffRevoked
	}
	if err = setOperation(tx, op, domain.OperationDispatched, ""); err != nil {
		return err
	}
	return s.commit(tx)
}

// CompleteInput commits the original message, queue and receipt together.
func (s *Store) CompleteInput(id string, outcome domain.Delivery, message, turn string) error {
	return s.completeInput(id, outcome, message, turn, false)
}

// RejectChangedConversationInput invalidates the snapshot in the same
// transaction that records definite non-delivery of the first input.
func (s *Store) RejectChangedConversationInput(id, message string) error {
	return s.completeInput(id, domain.DeliveryOutcome(true, false), message, "", true)
}

func (s *Store) completeInput(id string, outcome domain.Delivery, message, turn string, changedImport bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := outcome.Operation
	message = clipEventText(message, 32<<10)
	op, err := operation(tx, id)
	if err != nil {
		return err
	}
	if !op.State.Unsettled() || op.State == domain.OperationIndeterminate {
		return ErrInputHandoffRevoked
	}
	if changedImport && !unhanded(op) {
		return ErrInputHandoffRevoked
	}
	item, err := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", op.SessionID, "input:"+id))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if item.Kind == "queued" && state == domain.OperationConfirmed && turn != "" {
		item.TurnID = turn
		if err = putItem(tx, op.SessionID, &item); err != nil {
			return err
		}
	}
	if err = setOperation(tx, op, state, message); err != nil {
		return err
	}
	if state == domain.OperationRejected && op.Kind != steerSubmissionKind {
		session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", op.SessionID))
		if err != nil {
			return err
		}
		if session.ExecutionGuard == "" {
			session.Status, session.Error = outcome.Execution, message
			if changedImport && session.Origin != nil && session.ThreadID == "" {
				session.Origin.ResumeRef, session.Origin.Reason = "", message
			}
			if err := saveCompletionSession(tx, &session); err != nil {
				return err
			}
		}
	}
	if state == domain.OperationIndeterminate || state == domain.OperationRejected && op.Kind != steerSubmissionKind {
		if _, _, err := pauseQueue(tx, op.SessionID, message); err != nil {
			return err
		}
	}
	if item.Kind == "user" {
		if turn != "" {
			item.TurnID = turn
		}
		if err = putInputStatus(tx, op.SessionID, &item, outcome.Input); err != nil {
			return err
		}
	}
	return s.commit(tx)
}

func saveInputStatus(tx *sql.Tx, session, id, turn, status string) error {
	item, err := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", session, "input:"+id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Old journals may lack the display row.
	}
	if err != nil {
		return err
	}
	if item.Kind != "user" || item.Status == status && (turn == "" || item.TurnID == turn) {
		return nil
	}
	if turn != "" {
		item.TurnID = turn
	}
	if err := putInputStatus(tx, session, &item, status); err != nil {
		return err
	}
	return nil
}

func putInputStatus(tx *sql.Tx, session string, item *domain.Item, status string) error {
	item.Status = status
	if err := putItem(tx, session, item); err != nil {
		return err
	}
	_, err := journal(tx, domain.Event{Type: "item.upsert", SessionID: session, Item: item})
	return err
}

func (s *Store) RejectUnhandedInput(id, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	op, err := operation(tx, id)
	if err != nil {
		return err
	}
	if !unhanded(op) {
		return ErrInputHandoffRevoked
	}
	message = clipEventText(message, 32<<10)
	if err := rejectUnhandedInput(tx, op, message, domain.ExecutionFailed); err != nil {
		return err
	}
	if op.Kind == ordinarySubmissionKind {
		if _, _, err := pauseQueue(tx, op.SessionID, message); err != nil {
			return err
		}
	}
	return s.commit(tx)
}

func rejectUnhandedInput(tx *sql.Tx, op domain.Operation, message string, status domain.ExecutionState) error {
	if err := setOperation(tx, op, domain.OperationRejected, message); err != nil {
		return err
	}
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", op.SessionID))
	if err != nil {
		return err
	}
	if session.Status == domain.ExecutionStarting && session.ExecutionGuard == "" {
		session.Status, session.Error = status, message
		if err := saveCompletionSession(tx, &session); err != nil {
			return err
		}
	}
	return saveInputStatus(tx, op.SessionID, op.ID, "", "failed")
}

// CancelPreparedInputs revokes only submissions which have not crossed the
// native claim. Queued originals keep their row ID and become explicitly sendable.
func (s *Store) CancelPreparedInputs(session, exactID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT id,kind FROM operations WHERE session_id=? AND state='accepted' AND kind IN ('submit','steer') AND (?='' OR id=?)", session, exactID, exactID)
	if err != nil {
		return nil, err
	}
	var ops []domain.Operation
	for rows.Next() {
		op := domain.Operation{SessionID: session, State: domain.OperationAccepted}
		if err = rows.Scan(&op.ID, &op.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		ops = append(ops, op)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(ops) == 0 {
		return nil, err
	}
	const message = "已停止，消息未交给 Agent"
	var ids []string
	for _, op := range ops {
		if err = rejectUnhandedInput(tx, op, message, domain.ExecutionInterrupted); err != nil {
			return nil, err
		}
		ids = append(ids, op.ID)
	}
	queue, err := readQueue(tx, session)
	if err == nil {
		for i := range queue.Items {
			for _, op := range ops {
				if queue.Items[i].Input.OperationID == op.ID {
					if _, err := tx.Exec("DELETE FROM items WHERE session_id=? AND id=? AND json_extract(body,'$.kind')='queued'", session, "input:"+op.ID); err != nil {
						return nil, err
					}
					queue.Items[i].State = domain.QueueItemWaiting
					queue.Items[i].Input.OperationID = domain.NewID()
				}
			}
		}
		queue.Paused, queue.Reason = true, "stopped"
		queue.Revision++
		err = putQueue(tx, queue)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return ids, s.commit(tx)
}

func (s *Store) repairConfirmedInputItems() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT o.session_id,o.id FROM operations o JOIN items i ON i.session_id=o.session_id AND i.id='input:'||o.id WHERE o.state='confirmed' AND json_extract(i.body,'$.kind')='user' AND json_extract(i.body,'$.status')='dispatching'`)
	if err != nil {
		return err
	}
	var missing []domain.Operation
	for rows.Next() {
		var op domain.Operation
		if err := rows.Scan(&op.SessionID, &op.ID); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, op)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, op := range missing {
		if err := saveInputStatus(tx, op.SessionID, op.ID, "", "sent"); err != nil {
			return err
		}
	}
	return s.commit(tx)
}
