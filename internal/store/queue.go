package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"shioji/internal/domain"
	"time"
	"unicode/utf8"
)

func (s *Store) queueLocked(id string) (domain.QueueState, error) {
	return readQueue(s.historyQueries(), id)
}

func readQueue(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (domain.QueueState, error) {
	state, err := load[domain.QueueState](q.QueryRow("SELECT body FROM queues WHERE session_id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.QueueState{SessionID: id, Items: []domain.QueueItem{}}, nil
	}
	return state, err
}
func (s *Store) Queue(id string) (domain.QueueState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queueLocked(id)
}
func putQueue(tx *sql.Tx, q domain.QueueState) error {
	if _, err := tx.Exec("INSERT INTO queues VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET body=excluded.body", q.SessionID, blob(q)); err != nil {
		return err
	}
	_, err := journal(tx, domain.Event{SessionID: q.SessionID, Type: "queue", Queue: &q})
	return err
}
func inputDigest(v any) string { h := sha256.Sum256(blob(v)); return hex.EncodeToString(h[:]) }
func validInput(in domain.Submission) bool {
	return in.OperationID != "" && len(in.OperationID) <= 256 && in.SessionID != "" && in.ValidateContent() == nil
}
func (s *Store) Enqueue(in domain.Submission) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var none domain.Operation
	if !validInput(in) {
		return none, errors.New("queue requires a session and bounded nonempty text")
	}
	digest := inputDigest(struct {
		Kind  string
		Input domain.Submission
	}{"queue", in})
	op, err := operation(s.db, in.OperationID)
	if err == nil {
		if op.Digest != digest {
			return none, errors.New("operation ID already used")
		}
		return op, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return none, err
	}
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", in.SessionID))
	if err != nil {
		return none, err
	}
	if session.WorkspaceID != in.WorkspaceID || session.ContextChanged || session.Status == "unknown" {
		return none, errors.New("session context unavailable")
	}
	workspace, err := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", session.WorkspaceID))
	if err != nil {
		return none, err
	}
	if session.Archived || workspace.Archived {
		return none, errors.New("restore the session and workspace before queueing")
	}
	q, err := s.queueLocked(in.SessionID)
	if err != nil {
		return none, err
	}
	if len(q.Items) >= 32 {
		return none, errors.New("queue is full (32 messages)")
	}
	input := in
	input.OperationID = domain.NewID()
	input.Git = domain.GitChoice{}
	q.Items = append(q.Items, domain.QueueItem{ID: in.OperationID, Input: input})
	q.Revision++
	if len(blob(q)) > 384<<10 {
		return none, errors.New("queue content exceeds 384 KiB")
	}
	op = domain.Operation{ID: in.OperationID, SessionID: in.SessionID, State: "confirmed", Digest: digest}
	tx, err := s.db.Begin()
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO operations(id,session_id,state,digest) VALUES(?,?,?,?)", op.ID, op.SessionID, op.State, digest); err != nil {
		return none, err
	}
	if err = putQueue(tx, q); err != nil {
		return none, err
	}
	if _, err = journal(tx, domain.Event{Type: "operation", SessionID: op.SessionID, Operation: &op}); err != nil {
		return none, err
	}
	return op, s.commit(tx)
}
func (s *Store) changeQueue(sid string, revision int64, change func(*domain.QueueState) error) (domain.QueueState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, err := s.queueLocked(sid)
	if err != nil {
		return q, err
	}
	if q.Revision != revision {
		return q, errors.New("queue changed; refresh before editing")
	}
	if err = change(&q); err != nil {
		return q, err
	}
	q.Revision++
	if len(blob(q)) > 384<<10 {
		return q, errors.New("queue content exceeds 384 KiB")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return q, err
	}
	defer tx.Rollback()
	if err = putQueue(tx, q); err != nil {
		return q, err
	}
	return q, s.commit(tx)
}
func (s *Store) EditQueue(sid, id string, revision int64, text string, attachments ...[]domain.Attachment) (domain.QueueState, error) {
	return s.editQueue(sid, id, revision, text, attachments, nil, false, nil)
}
func (s *Store) EditQueueContent(sid, id string, revision int64, text string, attachments []domain.Attachment, skills []domain.SkillReference, positions []domain.ReferencePosition) (domain.QueueState, error) {
	return s.editQueue(sid, id, revision, text, [][]domain.Attachment{attachments}, skills, skills != nil, positions)
}
func (s *Store) editQueue(sid, id string, revision int64, text string, attachments [][]domain.Attachment, skills []domain.SkillReference, replaceSkills bool, positions []domain.ReferencePosition) (domain.QueueState, error) {
	return s.changeQueue(sid, revision, func(q *domain.QueueState) error {
		if !utf8.ValidString(text) || len(text) > 128<<10 {
			return errors.New("invalid queued text")
		}
		for i := range q.Items {
			if q.Items[i].ID == id {
				if q.Items[i].State.Reserved() {
					return errors.New("queued delivery is in flight or unknown")
				}
				input := q.Items[i].Input
				if input.GoalObjective != "" {
					objective, command := domain.GoalCommand(text)
					if command && objective == "" {
						return errors.New("请输入 /goal 和完整目标")
					}
					input.GoalObjective = ""
					if command {
						input.GoalObjective = objective
					}
				}
				input.Text = text
				input.ReferencePositions = positions
				if replaceSkills {
					input.Skills = skills
				}
				if len(attachments) > 0 {
					input.Attachments = attachments[0]
				}
				if err := input.ValidateContent(); err != nil {
					return err
				}
				q.Items[i].Input = input
				return nil
			}
		}
		return errors.New("queued message no longer exists")
	})
}
func (s *Store) RemoveQueue(sid, id string, revision int64) (domain.QueueState, error) {
	return s.changeQueue(sid, revision, func(q *domain.QueueState) error {
		for i := range q.Items {
			if q.Items[i].ID == id {
				if q.Items[i].State == "dispatching" {
					return errors.New("queued delivery is in flight")
				}
				q.Items = append(q.Items[:i], q.Items[i+1:]...)
				return nil
			}
		}
		return errors.New("queued message no longer exists")
	})
}
func (s *Store) PauseQueue(sid, reason string) (domain.QueueState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return domain.QueueState{}, err
	}
	defer tx.Rollback()
	q, changed, err := pauseQueue(tx, sid, reason)
	if err != nil || !changed {
		return q, err
	}
	return q, s.commit(tx)
}

func pauseQueue(tx *sql.Tx, sid, reason string) (domain.QueueState, bool, error) {
	q, err := readQueue(tx, sid)
	if err != nil || q.Paused {
		return q, false, err
	}
	q.Paused = true
	q.Reason = reason
	q.Revision++
	return q, true, putQueue(tx, q)
}
func (s *Store) ContinueQueue(sid string, revision int64) (domain.QueueState, error) {
	return s.changeQueue(sid, revision, func(q *domain.QueueState) error {
		for i := range q.Items {
			if q.Items[i].State.Reserved() {
				return errors.New("queued delivery needs reconciliation")
			}
			if q.Items[i].State == "failed" {
				q.Items[i].Input.OperationID = domain.NewID()
				q.Items[i].State = ""
			}
		}
		q.Paused = false
		q.Reason = ""
		return nil
	})
}
func (s *Store) AcceptQueued(in domain.Submission, revision int64) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptLocked(in, revision, false, "")
}

func (s *Store) AcceptQueuedNow(in domain.Submission, revision int64) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptLocked(in, revision, true, "")
}

// AcceptSteer journals intent before the harness pipe is touched. Reusing an ID
// queries the original result; it never writes a second same-turn input.
func (s *Store) AcceptSteer(in domain.Submission, turn string) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptSteerLocked(in, turn, 0)
}
func (s *Store) AcceptQueuedSteer(in domain.Submission, turn string, revision int64) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptSteerLocked(in, turn, revision)
}
func (s *Store) acceptSteerLocked(in domain.Submission, turn string, revision int64) (domain.Operation, error) {
	var none domain.Operation
	if !validInput(in) || turn == "" {
		return none, errors.New("invalid steer input")
	}
	digest := inputDigest(struct {
		Kind, Turn string
		Input      domain.Submission
	}{"steer", turn, in})
	op, err := operation(s.db, in.OperationID)
	if err == nil {
		if op.Digest != digest {
			return none, errors.New("operation ID already used")
		}
		return op, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return none, err
	}
	var queued *domain.QueueState
	if revision > 0 {
		q, e := s.queueLocked(in.SessionID)
		if e != nil {
			return none, e
		}
		if q.Revision != revision {
			return none, errors.New("queue changed")
		}
		found := false
		for i := range q.Items {
			if q.Items[i].Input.OperationID == in.OperationID && string(blob(q.Items[i].Input)) == string(blob(in)) && q.Items[i].State == "" {
				q.Items[i].State = "dispatching"
				found = true
				break
			}
		}
		if !found {
			return none, errors.New("queued message unavailable")
		}
		q.Revision++
		queued = &q
	}
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", in.SessionID))
	if err != nil {
		return none, err
	}
	active, err := load[domain.Turn](s.db.QueryRow("SELECT body FROM turns WHERE session_id=? AND id=?", in.SessionID, turn))
	if err != nil {
		return none, err
	}
	if session.WorkspaceID != in.WorkspaceID || session.Status != "running" || session.ContextChanged || active.Status != "running" {
		return none, errors.New("turn is no longer active")
	}
	op = domain.Operation{ID: in.OperationID, SessionID: in.SessionID, State: "accepted", Digest: digest, Kind: steerSubmissionKind}
	tx, err := s.db.Begin()
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO operations(id,session_id,state,digest,kind) VALUES(?,?,?,?,?)", op.ID, op.SessionID, op.State, digest, op.Kind); err != nil {
		return none, err
	}
	var item *domain.Item
	if queued == nil {
		now := time.Now()
		item = &domain.Item{ID: "input:" + op.ID, TurnID: turn, Kind: "user", ReferencePositions: in.ReferencePositions, Skills: in.Skills, Attachments: in.Attachments, Text: in.Text, Status: "dispatching", Final: true, SentAt: &now}
		if err = verifyQuestionReply(tx, in); err != nil {
			return none, err
		}
		if in.QuestionReply != nil {
			item.ReplyTo = in.QuestionReply.QuestionID
		}
		if err = putItem(tx, op.SessionID, item); err != nil {
			return none, err
		}
	} else {
		if err = putItem(tx, op.SessionID, &domain.Item{ID: "input:" + op.ID, TurnID: turn, Kind: "queued", ReferencePositions: in.ReferencePositions, Skills: in.Skills, Attachments: in.Attachments, Text: in.Text, Final: true}); err != nil {
			return none, err
		}
		if err = putQueue(tx, *queued); err != nil {
			return none, err
		}
	}
	if _, err = journal(tx, domain.Event{Type: "submission", SessionID: op.SessionID, Operation: &op, Item: item}); err != nil {
		return none, err
	}
	return op, s.commit(tx)
}

// queueOutcome commits delivery state and queue consumption together.
func queueOutcome(tx *sql.Tx, op domain.Operation) error {
	if op.State != "confirmed" && op.State != "rejected" && op.State != "indeterminate" {
		return nil
	}
	q, err := load[domain.QueueState](tx.QueryRow("SELECT body FROM queues WHERE session_id=?", op.SessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for i, item := range q.Items {
		if item.Input.OperationID == op.ID {
			if op.State == "confirmed" {
				history, e := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", op.SessionID, "input:"+op.ID))
				if e != nil {
					return e
				}
				history.Kind = "user"
				history.Status = "sent"
				if history.SentAt == nil {
					now := time.Now()
					history.SentAt = &now
				}
				if e = putItem(tx, op.SessionID, &history); e != nil {
					return e
				}
				// This is the queued input's first sent-message event, distinct
				// from later harness updates to the same historical user item.
				if _, e = journal(tx, domain.Event{Type: "submission", SessionID: op.SessionID, Item: &history}); e != nil {
					return e
				}
				q.Items = append(q.Items[:i], q.Items[i+1:]...)
			} else {
				q.Items[i].State = "failed"
				if op.State == "indeterminate" {
					q.Items[i].State = "unknown"
				}
				q.Paused = true
				q.Reason = op.Error
			}
			q.Revision++
			return putQueue(tx, q)
		}
	}
	return nil
}

// ReorderQueue moves one waiting input before another, or to the end.
func (s *Store) ReorderQueue(sid, id, before string, revision int64) (domain.QueueState, error) {
	return s.changeQueue(sid, revision, func(q *domain.QueueState) error {
		from, to := -1, len(q.Items)
		if before != "" {
			to = -1
		}
		for i, item := range q.Items {
			if item.State.Reserved() {
				return errors.New("queued delivery is in flight or unknown")
			}
			if item.ID == id {
				from = i
			}
			if item.ID == before {
				to = i
			}
		}
		if from < 0 || to < 0 {
			return errors.New("queued message no longer exists")
		}
		item := q.Items[from]
		q.Items = append(q.Items[:from], q.Items[from+1:]...)
		if to > from {
			to--
		}
		q.Items = append(q.Items, domain.QueueItem{})
		copy(q.Items[to+1:], q.Items[to:])
		q.Items[to] = item
		return nil
	})
}

const automaticQueueHead = `COALESCE(json_extract(q.body,'$.paused'),0)=0
	AND json_array_length(json_extract(q.body,'$.items'))>0
	AND COALESCE(json_extract(q.body,'$.items[0].state'),'')=''`

// Read only the eligible IDs. Archived history and paused queues are outside
// the automatic dispatch scan; submitLocked still checks the current revision.
func (s *Store) ReadyQueueSessions() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT s.id FROM sessions s JOIN queues q ON q.session_id=s.id
		JOIN workspaces w ON w.id=json_extract(s.body,'$.workspaceId')
		WHERE json_extract(s.body,'$.status') IN ('idle','completed','interrupted','failed','abandoned')
		AND COALESCE(json_extract(w.body,'$.archived'),0)=0
		AND COALESCE(json_extract(s.body,'$.archived'),0)=0
		AND COALESCE(json_extract(s.body,'$.goal.state'),'') <>'active'
		AND ` + automaticQueueHead + `
		AND NOT EXISTS(SELECT 1 FROM operations o WHERE o.session_id=s.id AND o.state IN ('accepted','dispatched','indeterminate'))
		AND NOT EXISTS(SELECT 1 FROM requests r WHERE r.session_id=s.id AND json_extract(r.body,'$.state') IN ('pending','submitting'))
		ORDER BY s.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
