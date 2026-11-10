package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"shioji/internal/domain"
	"time"
)

func contextDigest(in domain.ContextActionRequest) string {
	h := sha256.Sum256(blob(in))
	return hex.EncodeToString(h[:])
}
func (s *Store) ContextOperation(in domain.ContextActionRequest) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := operation(s.db, in.OperationID)
	if err == nil && (op.SessionID != in.SessionID || op.Kind != "compact" || op.Digest != contextDigest(in)) {
		return domain.Operation{}, errors.New("压缩操作与原请求不一致")
	}
	return op, err
}
func (s *Store) ValidateContextAction(in domain.ContextActionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.contextActionAvailable(s.db, in, in.Revision)
	return err
}

func (s *Store) contextActionAvailable(q interface{ QueryRow(string, ...any) *sql.Row }, in domain.ContextActionRequest, revision int64) (domain.Session, error) {
	session, err := load[domain.Session](q.QueryRow("SELECT body FROM sessions WHERE id=?", in.SessionID))
	if err != nil {
		return session, err
	}
	if session.Revision != revision || session.ContextChanged {
		return session, errors.New("会话状态已改变，请重试")
	}
	if err = s.historyAvailable(q, session); err != nil {
		return session, err
	}
	busy, err := hasActiveWork(q, session.ID)
	if err != nil {
		return session, err
	}
	if busy {
		return session, errors.New("请等待子 Agent 和后台任务结束")
	}
	return session, nil
}

func (s *Store) AcceptContextAction(in domain.ContextActionRequest, revision int64) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var none domain.Operation
	if in.OperationID == "" || len(in.OperationID) > 256 || in.SessionID == "" || in.Revision <= 0 {
		return none, errors.New("无效的压缩请求")
	}
	if op, err := operation(s.db, in.OperationID); err == nil {
		if op.Kind != "compact" || op.SessionID != in.SessionID || op.Digest != contextDigest(in) {
			return none, errors.New("操作标识已用于其他请求")
		}
		return op, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return none, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return none, err
	}
	defer tx.Rollback()
	session, err := s.contextActionAvailable(tx, in, revision)
	if err != nil {
		return none, err
	}
	action := &domain.ContextAction{RequestRevision: in.Revision, OperationID: in.OperationID, SourceRef: session.ThreadID, State: "starting", PreviousStatus: session.Status}
	err = tx.QueryRow("SELECT id FROM turns WHERE session_id=? ORDER BY json_extract(body,'$.order') DESC LIMIT 1", session.ID).Scan(&action.BeforeTurnID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return none, err
	}
	session.ContextAction = action
	session.Status = "starting"
	session.Revision++
	op := domain.Operation{ID: in.OperationID, SessionID: session.ID, Kind: "compact", State: "accepted", Digest: contextDigest(in)}
	if _, err = tx.Exec("INSERT INTO operations(id,session_id,state,digest,kind) VALUES(?,?,?,?,?)", op.ID, op.SessionID, op.State, op.Digest, op.Kind); err != nil {
		return none, err
	}
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
		return none, err
	}
	now := time.Now()
	item := domain.Item{ID: "context-command:" + op.ID, Kind: "command", Presentation: "compact", Name: "压缩上下文", Status: "accepted", Final: true, SentAt: &now}
	if err = putItem(tx, session.ID, &item); err != nil {
		return none, err
	}
	if _, err = journal(tx, domain.Event{Type: "item.upsert", SessionID: session.ID, Item: &item}); err != nil {
		return none, err
	}
	if _, err = journal(tx, domain.Event{Type: "operation", SessionID: session.ID, Operation: &op, Session: &session}); err != nil {
		return none, err
	}
	return op, s.commit(tx)
}

// A source turn is stronger evidence than a late RPC error or timeout.
func settleContextAction(tx *sql.Tx, op *domain.Operation) error {
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", op.SessionID))
	if err != nil {
		return err
	}
	a := session.ContextAction
	if a == nil || a.OperationID != op.ID {
		return errors.New("压缩记录已改变")
	}
	if a.TurnID != "" {
		op.State = "confirmed"
		op.Error = ""
		return nil
	}
	switch op.State {
	case "indeterminate":
		a.State = "unknown"
		a.Error = "压缩结果尚未确认"
		session.Status = "unknown"
		session.Error = a.Error
		session.Recovery = nil
	case "rejected":
		a.State = "failed"
		a.Error = op.Error
		session.Status = a.PreviousStatus
	default:
		return nil
	}
	session.Revision++
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
		return err
	}
	_, err = journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session})
	return err
}

func observeContextAction(tx *sql.Tx, session *domain.Session, e *domain.Event) error {
	a := session.ContextAction
	if a == nil || a.SourceRef != session.ThreadID || (!a.State.Active() && a.State != "unknown") {
		return nil
	}
	candidate := ""
	if e.Turn != nil && e.Turn.Presentation == "compaction" {
		candidate = e.Turn.ID
	}
	if e.Item != nil && e.Item.Presentation == "compaction" {
		candidate = e.Item.TurnID
	}
	if a.TurnID == "" && candidate != "" && candidate != a.BeforeTurnID {
		var order, before int64
		if err := tx.QueryRow(`SELECT COALESCE((SELECT json_extract(body,'$.order') FROM turns WHERE session_id=? AND id=?),0),COALESCE((SELECT json_extract(body,'$.order') FROM turns WHERE session_id=? AND id=?),0)`, session.ID, candidate, session.ID, a.BeforeTurnID).Scan(&order, &before); err != nil {
			return err
		}
		if order > before {
			a.TurnID = candidate
		}
	}
	if a.TurnID == "" || e.Turn != nil && e.Turn.ID != a.TurnID {
		if e.Turn != nil && session.ExecutionGuard == "" {
			session.Status = a.State
		}
		return nil
	}
	if err := attachContextCommand(tx, session.ID, *a); err != nil {
		return err
	}
	if e.Turn != nil && e.Turn.ID == a.TurnID {
		a.State = e.Turn.Status
		a.Error = e.Turn.Error
		if !a.State.Active() && a.State != "unknown" && session.ExecutionGuard == "" {
			session.Status = a.PreviousStatus
			session.Error = ""
			session.Recovery = nil
		}
	} else if a.State == "starting" || a.State == "unknown" {
		a.State = "running"
		a.Error = ""
		if session.ExecutionGuard == "" {
			session.Status = "running"
			session.Error = ""
		}
	}
	op, err := operation(tx, a.OperationID)
	if err != nil {
		return err
	}
	if op.State != "confirmed" {
		op.State = "confirmed"
		op.Error = ""
		if _, err = tx.Exec("UPDATE operations SET state='confirmed',error='' WHERE id=?", op.ID); err != nil {
			return err
		}
		if _, err = journal(tx, domain.Event{Type: "operation", SessionID: session.ID, Operation: &op}); err != nil {
			return err
		}
	}
	return nil
}

// The command is a worker-owned record, not harness input. Attach its history
// boundary only when the adapter has identified the actual maintenance turn.
func attachContextCommand(tx *sql.Tx, sid string, action domain.ContextAction) error {
	if action.TurnID == "" {
		return nil
	}
	item, err := load[domain.Item](tx.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", sid, "context-command:"+action.OperationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Older accepted operations did not have a command record.
	}
	if err != nil || item.TurnID == action.TurnID {
		return err
	}
	item.TurnID = action.TurnID
	if err = putItem(tx, sid, &item); err != nil {
		return err
	}
	_, err = journal(tx, domain.Event{Type: "item.upsert", SessionID: sid, Item: &item})
	return err
}

// Explicit evidence from the adapter settles uncertainty without issuing work.
func (s *Store) ResolveContextAction(id string, expected domain.ContextAction, observed domain.ContextObservation) error {
	if observed.TurnID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return err
	}
	a := session.ContextAction
	if a == nil || a.OperationID != expected.OperationID || a.SourceRef != expected.SourceRef || session.ThreadID != a.SourceRef {
		return errors.New("压缩目标已改变")
	}
	if !a.State.Active() && a.State != "unknown" {
		return nil
	}
	turn := domain.Turn{ID: observed.TurnID, Presentation: "compaction", Status: observed.State}
	event := domain.Event{Type: "turn", SessionID: id, Turn: &turn}
	if err = putTurn(tx, id, &turn); err != nil {
		return err
	}
	if err = observeContextAction(tx, &session, &event); err != nil {
		return err
	}
	if session.ExecutionGuard != "" {
		session.Status = "unknown"
	}
	session.Revision++
	event.Session = &session
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), id); err != nil {
		return err
	}
	if _, err = journal(tx, event); err != nil {
		return err
	}
	return s.commit(tx)
}
