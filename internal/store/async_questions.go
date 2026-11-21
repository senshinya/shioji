package store

import (
	"database/sql"
	"errors"
	"slices"

	"shioji/internal/domain"
)

func (s *Store) Question(session, item, question string) (domain.Question, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return findQuestion(s.db, session, item, question)
}

func findQuestion(q interface{ QueryRow(string, ...any) *sql.Row }, session, item, question string) (domain.Question, error) {
	entry, err := load[domain.Item](q.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", session, item))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Question{}, err
	}
	if entry.Kind == "assistant" && entry.Final {
		if at := slices.IndexFunc(entry.Questions, func(v domain.Question) bool { return v.ID == question }); at >= 0 {
			return entry.Questions[at], nil
		}
	}
	return domain.Question{}, errors.New("问题已失效或不属于此会话")
}

func verifyQuestionReply(tx *sql.Tx, in domain.Submission) error {
	r := in.QuestionReply
	if r == nil {
		return nil
	}
	q, err := findQuestion(tx, in.SessionID, r.ItemID, r.QuestionID)
	if err != nil {
		return err
	}
	if q.Text != r.Title {
		return errors.New("问题已改变")
	}
	var answered bool
	err = tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM items WHERE session_id=?
		AND json_extract(body,'$.replyTo')=? AND json_extract(body,'$.status') IN ('dispatching','sent','unknown'))`,
		in.SessionID, r.QuestionID).Scan(&answered)
	if err != nil {
		return err
	}
	if answered {
		return errors.New("此问题已有回答或正在确认提交")
	}
	return nil
}

// RestoreTranscriptQuestions upgrades older text-only rows from the owning
// harness's history. An item outside the current history cannot be inserted.
func (s *Store) RestoreTranscriptQuestions(session domain.Session, source domain.RecoveryHistory) error {
	if !source.Available {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", session.ID))
	if err != nil {
		return err
	}
	if current.ThreadID == "" || current.ThreadID != session.ThreadID || current.HarnessID != session.HarnessID {
		return nil
	}
	updated := false
	for _, item := range source.Items {
		if len(item.Questions) == 0 || item.Kind != "assistant" || !item.Final {
			continue
		}
		old, err := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", session.ID, item.ID))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if old.Kind != "assistant" || !old.Final || old.TurnID != item.TurnID ||
			old.Text != item.Text || len(old.Questions) > 0 {
			continue
		}
		old.Questions = item.Questions
		if err := putItem(tx, session.ID, &old); err != nil {
			return err
		}
		if _, err := journal(tx, domain.Event{Type: "item.upsert", SessionID: session.ID, Item: &old}); err != nil {
			return err
		}
		updated = true
	}
	if !updated {
		return nil
	}
	return s.commit(tx)
}
