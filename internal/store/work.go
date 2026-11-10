package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"unicode/utf8"
)

func applyWork(tx *sql.Tx, sid string, e *domain.Event) error {
	var seq int64
	if err := tx.QueryRow("SELECT COALESCE(MAX(sequence),0)+1 FROM journal").Scan(&seq); err != nil {
		return err
	}
	if e.Type == "work.upsert" {
		w := e.Work
		if w == nil || w.ID == "" || len(w.ID) > 512 || (w.Kind != "agent" && w.Kind != "background") || w.TurnID == "" || len(w.Title) > 512 || len(w.Brief) > 8192 || len(w.Latest) > 4096 {
			return errors.New("invalid work metadata")
		}
		switch w.State {
		case "starting", "running", "waiting", "completed", "stopped", "failed", "unknown":
		default:
			return errors.New("invalid work state")
		}
		old, err := load[domain.WorkActivity](tx.QueryRow("SELECT body FROM works WHERE session_id=? AND id=?", sid, w.ID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (old.Kind != w.Kind || old.TurnID != w.TurnID || old.ParentID != w.ParentID || old.Reference != w.Reference || old.OriginRef != w.OriginRef) {
			return errors.New("work ownership changed")
		}

		w.Revision = seq
		_, err = tx.Exec("INSERT INTO works VALUES(?,?,?) ON CONFLICT(session_id,id) DO UPDATE SET body=excluded.body", sid, w.ID, blob(w))
		return err
	}
	if e.WorkID == "" || e.WorkItem == nil || e.WorkItem.ID == "" || len(e.WorkItem.ID) > 1024 {
		return errors.New("invalid work item")
	}
	var known bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM works WHERE session_id=? AND id=?)", sid, e.WorkID).Scan(&known); err != nil {
		return err
	}
	if !known {
		return errors.New("work does not belong to this session")
	}
	item := e.WorkItem
	body, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if len(body) > 192<<10 || !utf8.Valid(body) {
		return errors.New("work item exceeds content limit")
	}
	var position int64
	err = tx.QueryRow("SELECT position FROM work_items WHERE session_id=? AND work_id=? AND id=?", sid, e.WorkID, item.ID).Scan(&position)
	if errors.Is(err, sql.ErrNoRows) {
		position = seq
	} else if err != nil {
		return err
	}
	item.Order = position
	_, err = tx.Exec("INSERT INTO work_items VALUES(?,?,?,?,?,?) ON CONFLICT(session_id,work_id,id) DO UPDATE SET revision=excluded.revision,body=excluded.body", sid, e.WorkID, item.ID, position, seq, blob(item))
	return err
}

// Work reads only previously accepted session-owned observations. It never
// touches the adapter or starts a child while reconstructing its detail view.
func (s *Store) Work(sid, id string) (domain.WorkDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := domain.WorkDetail{SessionID: sid}
	var err error
	out.Work, err = load[domain.WorkActivity](s.db.QueryRow("SELECT body FROM works WHERE session_id=? AND id=?", sid, id))
	if err != nil {
		return out, fmt.Errorf("work not found in session: %w", err)
	}
	if err = s.db.QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&out.Sequence); err != nil {
		return out, err
	}
	rows, err := s.db.Query("SELECT body,revision FROM work_items WHERE session_id=? AND work_id=? ORDER BY position DESC LIMIT 65", sid, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var raw []byte
		var i domain.WorkItem
		if err = rows.Scan(&raw, &i.Revision); err != nil {
			return out, err
		}
		if len(out.Items) == 64 || size+len(raw) > 384<<10 {
			out.Truncated = true
			break
		}
		if err = json.Unmarshal(raw, &i.Item); err != nil {
			return out, err
		}
		size += len(raw)
		out.Items = append(out.Items, i)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	pending, lookup := load[domain.WorkInputOperation](s.db.QueryRow("SELECT body FROM work_inputs WHERE session_id=? AND work_id=? AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate') ORDER BY rowid DESC LIMIT 1", sid, id))
	if lookup == nil {
		out.PendingInput = &pending
	} else if !errors.Is(lookup, sql.ErrNoRows) {
		return out, lookup
	}
	for i, j := 0, len(out.Items)-1; i < j; i, j = i+1, j-1 {
		out.Items[i], out.Items[j] = out.Items[j], out.Items[i]
	}
	return out, nil
}

// InvalidateWork is called by a new owning worker after process recovery, not by
// database readers. Lost observation is not a task completion.
func (s *Store) InvalidateWork() error {
	s.mu.Lock()
	if _, err := s.db.Exec("UPDATE work_inputs SET body=json_set(body,'$.state','indeterminate') WHERE json_extract(body,'$.state') IN ('accepted','dispatched')"); err != nil {
		s.mu.Unlock()
		return err
	}
	rows, err := s.db.Query("SELECT session_id,body FROM works WHERE json_extract(body,'$.state') IN ('running','starting','waiting') OR json_extract(body,'$.canSteer')=1 OR json_extract(body,'$.canContinue')=1")
	if err != nil {
		s.mu.Unlock()
		return err
	}
	type record struct {
		session string
		work    domain.WorkActivity
	}
	all := []record{}
	for rows.Next() {
		var raw []byte
		var r record
		if err = rows.Scan(&r.session, &raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &r.work); err != nil {
			break
		}
		all = append(all, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, r := range all {
		if r.work.Active() {
			r.work.State = "unknown"
		}
		r.work.CanStop = false
		r.work.CanSteer, r.work.CanContinue = false, false
		if _, err = s.Apply(r.session, domain.Event{Type: "work.upsert", Work: &r.work}); err != nil {
			return err
		}
	}
	return nil
}

// Activity is execution ownership, including child inference with no OS tool.
// Unknown observations are quarantined by the session's managed execution guard;
// after a verified process stop they remain readable history, not live work.
func hasActiveWork(q interface{ QueryRow(string, ...any) *sql.Row }, sid string) (bool, error) {
	var busy bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM works w JOIN sessions s ON w.session_id=s.id WHERE s.id=? AND json_extract(w.body,'$.state') IN ('starting','running','waiting') AND (COALESCE(json_extract(w.body,'$.originRef'),'')='' OR json_extract(w.body,'$.originRef')=json_extract(s.body,'$.threadId'))) OR EXISTS(SELECT 1 FROM work_inputs WHERE session_id=? AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate') AND COALESCE(json_extract(body,'$.executionStopped'),0)=0)`, sid, sid).Scan(&busy)
	return busy, err
}
func (s *Store) HasActiveWork(sid string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return hasActiveWork(s.db, sid)
}
