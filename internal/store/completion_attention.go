package store

import (
	"errors"
	"time"

	"shioji/internal/domain"
)

func observeReplyCompletion(session *domain.Session, previous, turn domain.Turn) {
	if turn.Status != "completed" || turn.Presentation == "compaction" || previous.Status == "completed" ||
		session.Completion != nil && session.Completion.Order >= turn.Order {
		return
	}
	at := time.Now().UTC()
	if turn.CompletedAt != nil {
		at = *turn.CompletedAt
	}
	session.Completion = &domain.ReplyCompletion{ID: domain.NewID(), HistoryRef: session.ThreadID, TurnID: turn.ID, Order: turn.Order, CompletedAt: at, Unread: true}
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
