package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"shioji/internal/domain"
	"slices"
)

// ReplyReconciliation pins the source and provisional records before a read-only
// history inspection. It grants no execution or history-replacement authority.
type ReplyReconciliation struct {
	Session domain.Session
	Items   []domain.Item
}

// PendingReplies includes old records after a worker restart. Failed/interrupted
// turns retain their partial output; only successfully completed turns qualify.
func (s *Store) PendingReplies() ([]ReplyReconciliation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT s.body,i.body FROM items i
 JOIN sessions s ON s.id=i.session_id
 JOIN turns t ON t.session_id=i.session_id AND t.id=json_extract(i.body,'$.turnId')
 WHERE json_extract(i.body,'$.kind')='assistant' AND COALESCE(json_extract(i.body,'$.final'),0)=0
 AND json_extract(t.body,'$.status')='completed'
 AND COALESCE(json_extract(s.body,'$.threadId'),'')!=''
 ORDER BY i.session_id,i.position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplyReconciliation
	for rows.Next() {
		var sessionBody, itemBody []byte
		if err = rows.Scan(&sessionBody, &itemBody); err != nil {
			return nil, err
		}
		var session domain.Session
		var item domain.Item
		if err = json.Unmarshal(sessionBody, &session); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(itemBody, &item); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Session.ID != session.ID {
			out = append(out, ReplyReconciliation{Session: session})
		}
		out[len(out)-1].Items = append(out[len(out)-1].Items, item)
	}
	return out, rows.Err()
}

// ReconcileReplies uses complete adapter history, never text similarity. Missing
// provisional items become invisible tombstones; their original streamed text
// remains in the journal. Normal item events update every connected client.
func (s *Store) ReconcileReplies(before ReplyReconciliation, source domain.RecoveryHistory) error {
	if !source.Available {
		return nil
	}
	completed := map[string]bool{}
	for _, turn := range source.Turns {
		completed[turn.ID] = turn.Status == "completed" && slices.Contains(source.CompleteItemTurns, turn.ID)
	}
	items := map[string]domain.Item{}
	for _, item := range source.Items {
		if _, duplicate := items[item.ID]; duplicate {
			return errors.New("duplicate history item identity")
		}
		items[item.ID] = item
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", before.Session.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if session.ThreadID == "" || session.ThreadID != before.Session.ThreadID || session.HarnessID != before.Session.HarnessID || !reflect.DeepEqual(session.Context, before.Session.Context) {
		return nil
	}
	var updates []domain.Item
	for _, old := range before.Items {
		if old.Kind != "assistant" || old.Final || !completed[old.TurnID] {
			continue
		}
		current, err := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", session.ID, old.ID))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, old) {
			continue
		}
		turn, err := load[domain.Turn](tx.QueryRow("SELECT body FROM turns WHERE session_id=? AND id=?", session.ID, old.TurnID))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if turn.Status != "completed" {
			continue
		}
		next, exists := items[old.ID]
		if exists {
			if next.TurnID != old.TurnID || next.Kind != "assistant" || !next.Final {
				continue
			}
			next.Order = old.Order
		} else {
			next = old
			next.Text, next.Status, next.Final = "", "superseded", true
		}
		if err = putItem(tx, session.ID, &next); err != nil {
			return err
		}
		updates = append(updates, next)
	}
	if len(updates) == 0 {
		return nil
	}
	session.Revision++
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
		return err
	}
	for _, item := range updates {
		if _, err = journal(tx, domain.Event{Type: "item.upsert", SessionID: session.ID, Session: &session, Item: &item}); err != nil {
			return err
		}
	}
	return s.commit(tx)
}
