package store

import (
	"database/sql"
	"errors"
	"shioji/internal/domain"
	"strings"
	"unicode/utf8"
)

// TitleJob captures an immutable input and the title revision at acceptance.
type TitleJob struct {
	Session domain.Session
	Prompt  string
	InputID string
}

type titleJobState string

const (
	titlePending   titleJobState = "pending"
	titleRunning   titleJobState = "running"
	titleCompleted titleJobState = "completed"
	titleFailed    titleJobState = "failed"
)

func (s *Store) ClaimTitleGeneration() (TitleJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := load[TitleJob](s.db.QueryRow("SELECT body FROM title_jobs WHERE state=? ORDER BY rowid LIMIT 1", titlePending))
	if err != nil {
		return job, err
	}
	_, err = s.db.Exec("UPDATE title_jobs SET state=? WHERE session_id=? AND state=?", titleRunning, job.Session.ID, titlePending)
	return job, err
}

// FinishTitleGeneration changes metadata only. The captured input and revision
// prevent a late answer from renaming a revised conversation or a user title.
func (s *Store) FinishTitleGeneration(job TitleJob, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	title = strings.TrimSpace(title)
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) > 80 || strings.ContainsAny(title, "\n\r\x00") {
		title = ""
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state titleJobState
	if err = tx.QueryRow("SELECT state FROM title_jobs WHERE session_id=?", job.Session.ID).Scan(&state); err != nil {
		return err
	}
	if state != titleRunning {
		return nil
	}
	state = titleCompleted
	if title == "" {
		state = titleFailed
	}
	if _, err = tx.Exec("UPDATE title_jobs SET state=? WHERE session_id=?", state, job.Session.ID); err != nil {
		return err
	}
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", job.Session.ID))
	if err != nil {
		return err
	}
	item, inputErr := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", session.ID, job.InputID))
	if inputErr != nil && !errors.Is(inputErr, sql.ErrNoRows) {
		return inputErr
	}
	if title != "" && !session.TitleOverride && session.SourceTitle == "" && session.TitleRevision == job.Session.TitleRevision && inputErr == nil && item.Text == job.Prompt {
		session.Title, session.SourceTitle = title, title
		session.TitleRevision++
		if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "session.title", SessionID: session.ID, Session: &session, Text: title}); err != nil {
			return err
		}
	}
	return s.commit(tx)
}
