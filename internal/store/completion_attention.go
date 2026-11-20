package store

import (
	"database/sql"
	"errors"
	"time"

	"shioji/internal/domain"
)

func (s *Store) observeReplyCompletion(tx *sql.Tx, session *domain.Session, previous, turn domain.Turn) error {
	if s.completionStopping.Load() {
		withdrawReplyCompletion(session)
		return nil
	}
	if turn.Status.Active() && session.Completion != nil && turn.Order > session.Completion.Order {
		withdrawReplyCompletion(session)
	}
	if turn.Status != "completed" || turn.Presentation == "compaction" || previous.Status == "completed" ||
		session.Completion != nil && session.Completion.Order >= turn.Order {
		return nil
	}
	var archived bool
	if err := tx.QueryRow("SELECT COALESCE(json_extract(body,'$.archived'),0) FROM workspaces WHERE id=?", session.WorkspaceID).Scan(&archived); err != nil {
		return err
	}
	if session.Archived || archived {
		return nil
	}
	busy, err := hasActiveWork(tx, session.ID)
	if err != nil {
		return err
	}
	at := time.Now().UTC()
	if turn.CompletedAt != nil {
		at = *turn.CompletedAt
	}
	session.Completion = &domain.ReplyCompletion{HistoryRef: session.ThreadID, TurnID: turn.ID, Order: turn.Order, CompletedAt: at, Pending: busy}
	if !busy {
		continues, err := replyContinuesAutomatically(tx, session)
		if err != nil {
			return err
		}
		session.Completion.AutomaticContinuation = continues
		session.Completion.ID = domain.NewID()
		session.Completion.Unread = true
	}
	return nil
}

func replyContinuesAutomatically(tx *sql.Tx, session *domain.Session) (bool, error) {
	if session.Goal.Active() {
		return true, nil
	}
	if session.ContextChanged || session.ExecutionGuard != "" {
		return false, nil
	}
	// The finishing turn's input receipt can arrive after its completion event.
	// An indeterminate receipt blocks dispatch until the user reconciles it.
	var ready bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM queues q WHERE q.session_id=? AND `+automaticQueueHead+`
		AND NOT EXISTS(SELECT 1 FROM operations o WHERE o.session_id=q.session_id AND o.state='indeterminate')
		AND NOT EXISTS(SELECT 1 FROM requests r WHERE r.session_id=q.session_id AND json_extract(r.body,'$.state') IN ('pending','submitting')))`, session.ID).Scan(&ready)
	return ready, err
}

func withdrawReplyCompletion(session *domain.Session) {
	if session.Completion != nil && session.Completion.Pending {
		// Retain the turn watermark so delayed source events cannot republish it.
		session.Completion.Pending = false
		session.Completion.Unread = false
	}
}

// No waiter holds admission. The same SQLite transaction that observes the last
// native work transition also publishes its completion identity.
func (s *Store) settleReplyCompletion(tx *sql.Tx, session *domain.Session, interrupted bool) (bool, error) {
	if s.completionStopping.Load() {
		changed := session.Completion != nil && session.Completion.Pending
		withdrawReplyCompletion(session)
		return changed, nil
	}
	c := session.Completion
	if c == nil || !c.Pending || c.HistoryRef != session.ThreadID {
		return false, nil
	}
	if !interrupted {
		busy, err := hasActiveWork(tx, session.ID)
		if err != nil || busy {
			return false, err
		}
		continues, err := replyContinuesAutomatically(tx, session)
		if err != nil {
			return false, err
		}
		c.AutomaticContinuation = continues
	}
	c.Pending, c.Unread = false, true
	c.ID, c.CompletedAt = domain.NewID(), time.Now().UTC()
	if interrupted {
		c.Outcome = "interrupted"
		item := domain.Item{ID: "native-work-interrupted:" + c.ID, TurnID: c.TurnID, Kind: "warning", Text: "Agent 已退出，仍在运行的后台工作已中断，执行结果未确认。", Status: "interrupted", Final: true}
		if err := putItem(tx, session.ID, &item); err != nil {
			return false, err
		}
		if _, err := journal(tx, domain.Event{Type: "item.upsert", SessionID: session.ID, Item: &item}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// InterruptCompletion is called only for the captured runtime's exit/retirement.
// Explicit Stop, archive, replacement input and worker shutdown withdraw first.
func (s *Store) InterruptCompletion(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	// A committed tombstone already withdrew completion and owns physical cleanup.
	// A truly missing live session remains an error rather than hiding corruption.
	if errors.Is(err, sql.ErrNoRows) {
		var deleted bool
		if readErr := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM deleted_sessions WHERE id=?)", id).Scan(&deleted); readErr != nil {
			return readErr
		}
		if deleted {
			return nil
		}
	}
	if err != nil {
		return err
	}
	changed, err := s.settleReplyCompletion(tx, &session, true)
	if err != nil || !changed {
		return err
	}
	if err = saveCompletionSession(tx, &session); err != nil {
		return err
	}
	return s.commit(tx)
}

func saveCompletionSession(tx *sql.Tx, session *domain.Session) error {
	session.Revision++
	if _, err := tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
		return err
	}
	_, err := journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: session})
	return err
}

// Empty id withdraws all waits at worker shutdown/recovery, before producers end.
func (s *Store) WithdrawCompletion(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sessions, err := rowsOf[domain.Session](tx, "SELECT body FROM sessions WHERE json_extract(body,'$.completion.pending')=1 AND (?='' OR id=?)", id, id)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		withdrawReplyCompletion(&session)
		if err = saveCompletionSession(tx, &session); err != nil {
			return err
		}
	}
	return s.commit(tx)
}

// ReadCompletion is idempotent. A delayed receipt for an older completion must
// never consume a reply that finished while the client was opening the session.
func (s *Store) ReadCompletion(sessionID, completionID string) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID == "" || completionID == "" {
		return domain.Session{}, errors.New("missing session or completion identity")
	}
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", sessionID))
	if err != nil {
		return session, err
	}
	if c := session.Completion; c != nil && c.ID == completionID && c.Unread {
		c.Unread = false
		session.Revision++
		return s.saveSessionMetadataLocked(session)
	}
	return session, nil
}

// SuspendCompletionPublication is nonblocking even when a producer is stuck.
// Final shutdown/recovery clears durable waits after that producer settles.
func (s *Store) SuspendCompletionPublication() { s.completionStopping.Store(true) }
