package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"strings"
	"time"
)

func (s *Store) HistoryOperation(id string) (domain.HistoryOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return load[domain.HistoryOperation](s.db.QueryRow("SELECT body FROM history_operations WHERE id=?", id))
}
func (s *Store) historyAvailable(q interface{ QueryRow(string, ...any) *sql.Row }, source domain.Session) error {
	if source.ThreadID == "" || source.Archived || source.ExecutionGuard != "" || source.Goal.Active() {
		return errors.New("会话尚不能修改历史")
	}
	switch source.Status {
	case "idle", "completed", "interrupted", "failed":
	default:
		return errors.New("请先停止当前轮次并核对执行状态")
	}
	var blocked bool
	if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM requests WHERE session_id=? AND json_extract(body,'$.state') IN ('pending','submitting')) OR EXISTS(SELECT 1 FROM operations WHERE session_id=? AND state IN ('accepted','dispatched','indeterminate')) OR EXISTS(SELECT 1 FROM workspaces WHERE id=? AND json_extract(body,'$.archived')=1)`, source.ID, source.ID, source.WorkspaceID).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return errors.New("请先处理待回答问题、授权或未确认输入")
	}
	return nil
}
func (s *Store) PrepareHistory(in domain.HistoryChange) (domain.HistoryOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero domain.HistoryOperation
	if in.OperationID == "" || len(in.OperationID) > 128 {
		return zero, errors.New("历史操作需要稳定标识")
	}
	old, err := load[domain.HistoryOperation](s.db.QueryRow("SELECT body FROM history_operations WHERE id=?", in.OperationID))
	if err == nil {
		if string(blob(old.Input)) != string(blob(in)) {
			return zero, errors.New("操作标识已被其他输入使用")
		}
		return old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	if _, err = operation(s.db, in.OperationID); !errors.Is(err, sql.ErrNoRows) {
		return zero, errors.New("操作标识已被使用")
	}
	source, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", in.SessionID))
	if err != nil {
		return zero, err
	}
	if source.Revision != in.Revision {
		return zero, errors.New("会话已改变，请重新载入后再试")
	}
	if err = s.historyAvailable(s.db, source); err != nil {
		return zero, err
	}
	if in.Restart {
		if busy, e := hasActiveWork(s.db, source.ID); e != nil {
			return zero, e
		} else if busy {
			return zero, errors.New("请先停止仍在运行的子 Agent 或后台任务")
		}
	}
	var first int64
	turn := in.TurnID
	if !in.Restart && turn == "" {
		if err = s.db.QueryRow(`SELECT t.id FROM turns t WHERE t.session_id=? AND json_extract(t.body,'$.status')='completed' ORDER BY (SELECT MIN(position) FROM items i WHERE i.session_id=t.session_id AND json_extract(i.body,'$.turnId')=t.id AND json_extract(i.body,'$.kind')='user') DESC LIMIT 1`, source.ID).Scan(&turn); err != nil {
			return zero, errors.New("没有可分叉的已完成轮次")
		}
	}
	var selected domain.Item
	if in.Restart {
		selected, err = load[domain.Item](s.db.QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", source.ID, in.ItemID))
		if err != nil {
			return zero, err
		}
		if selected.Kind != "user" || selected.Status != "sent" || selected.TurnID == "" {
			return zero, errors.New("只能修改已确认发送的历史消息")
		}
		turn = selected.TurnID
	}
	if err = s.db.QueryRow(`SELECT MIN(position) FROM items WHERE session_id=? AND json_extract(body,'$.turnId')=? AND json_extract(body,'$.kind')='user'`, source.ID, turn).Scan(&first); err != nil {
		return zero, errors.New("找不到完整的轮次起点")
	}
	if in.Restart && first != selected.Order {
		return zero, errors.New("轮次内的追加消息暂不能单独重开")
	}
	boundary, err := load[domain.Turn](s.db.QueryRow("SELECT body FROM turns WHERE session_id=? AND id=?", source.ID, turn))
	if err != nil {
		return zero, err
	}
	if boundary.Status == "running" || boundary.Status == "unknown" || !in.Restart && boundary.Status != "completed" {
		return zero, errors.New("请选择已经完成的轮次")
	}
	keepOrder := boundary.Order
	if in.Restart {
		keepOrder--
	}

	target := source
	if !in.Restart {
		// An application fork has its own history provenance; only the original
		// imported record owns the external-source deduplication anchor.
		target.Origin = nil
		target.ID = domain.NewID()
		target.ForkedFrom = source.ID
		target.ForkedTurn = turn
		target.Title = source.Title + " · 分叉"
		target.TitleOverride = true
		target.TitleRevision = 1
		target.Revision = 1
	}
	target.ThreadID = ""
	target.Completion = nil
	target.Status = "idle"
	if !in.Restart {
		target.Status = "completed"
	}
	target.Error = ""
	target.Recovery = nil
	target.ExecutionGuard = ""
	op := domain.HistoryOperation{KeepOrder: keepOrder, ID: in.OperationID, Input: in, Source: source, Session: target, TurnID: turn, State: "prepared"}
	if in.Restart {
		op.Submission = domain.Submission{OperationID: in.OperationID, WorkspaceID: source.WorkspaceID, SessionID: source.ID, Text: in.Text, Config: in.Config, Attachments: selected.Attachments, Skills: selected.Skills}
		if in.References != nil {
			op.Submission.Attachments = in.References.Attachments
			op.Submission.Skills = in.References.Skills
			op.Submission.ReferencePositions = in.References.ReferencePositions
		} else if in.Text == selected.Text {
			op.Submission.ReferencePositions = selected.ReferencePositions
		}
		if err = op.Submission.ValidateContent(); err != nil {
			return zero, err
		}
	}
	_, err = s.db.Exec("INSERT INTO history_operations VALUES(?,?)", op.ID, blob(op))
	return op, err
}
func (s *Store) SetHistoryThread(id, thread string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := load[domain.HistoryOperation](s.db.QueryRow("SELECT body FROM history_operations WHERE id=?", id))
	if err != nil {
		return err
	}
	if op.State != "prepared" || thread == "" || (!op.Input.Restart && thread == op.Source.ThreadID) {
		return errors.New("来源结果与历史操作不一致")
	}
	op.ThreadID = thread
	op.State = "forked"
	_, err = s.db.Exec("UPDATE history_operations SET body=? WHERE id=?", blob(op), id)
	return err
}
func (s *Store) FailHistory(id string, cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := load[domain.HistoryOperation](s.db.QueryRow("SELECT body FROM history_operations WHERE id=?", id))
	if err != nil {
		return err
	}
	if op.State == "complete" {
		return nil
	}
	op.State = "failed"
	op.Error = cause.Error()
	_, err = s.db.Exec("UPDATE history_operations SET body=? WHERE id=?", blob(op), id)
	return err
}
func (s *Store) CommitHistory(id string) (domain.HistoryOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := load[domain.HistoryOperation](s.db.QueryRow("SELECT body FROM history_operations WHERE id=?", id))
	if err != nil {
		return op, err
	}
	if op.State == "complete" {
		return op, nil
	}
	if op.State != "forked" {
		return op, errors.New("分叉结果尚未确认，原会话保持不变")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return op, err
	}
	defer tx.Rollback()
	source, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", op.Source.ID))
	if err != nil {
		return op, err
	}
	if source.Revision != op.Source.Revision || source.ThreadID != op.Source.ThreadID {
		return op, errors.New("会话已改变，未替换原历史")
	}
	if err = s.historyAvailable(tx, source); err != nil {
		return op, err
	}
	if op.Input.Restart {
		if busy, e := hasActiveWork(tx, source.ID); e != nil {
			return op, e
		} else if busy {
			return op, errors.New("请先停止仍在运行的子 Agent 或后台任务")
		}
	}
	target := op.Session
	retitle := false
	target.ContextUsage = nil
	target.ContextUsageAfterTurnOrder = 0
	target.ThreadID = op.ThreadID
	target.Completion = nil
	if op.Input.Restart {
		// Keep metadata edits and accepted launch context authoritative.
		if err = preserveScheduledRunOutcome(tx, source.ID, time.Now().UTC(), "会话历史已替换"); err != nil {
			return op, err
		}
		target = source
		target.Completion = nil
		target.ContextUsage = nil
		target.ContextUsageAfterTurnOrder = 0
		target.ThreadID = op.ThreadID
		target.Status = "starting"
		target.Error = ""
		target.Recovery = nil
		target.Config = op.Input.Config
		target.Revision++
		if _, err = tx.Exec(`DELETE FROM items WHERE session_id=? AND json_extract(body,'$.kind')!='queued' AND COALESCE(json_extract(body,'$.turnId'),'') NOT IN(SELECT id FROM turns WHERE session_id=? AND json_extract(body,'$.order')<=?)`, source.ID, source.ID, op.KeepOrder); err != nil {
			return op, err
		}
		if _, err = tx.Exec(`DELETE FROM works WHERE session_id=? AND COALESCE(json_extract(body,'$.turnId'),'') NOT IN(SELECT id FROM turns WHERE session_id=? AND json_extract(body,'$.order')<=?)`, source.ID, source.ID, op.KeepOrder); err != nil {
			return op, err
		}

		if !target.TitleOverride {
			var hasEarlierInput bool
			if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM items WHERE session_id=? AND json_extract(body,'$.kind')='user')`, target.ID).Scan(&hasEarlierInput); err != nil {
				return op, err
			}
			if !hasEarlierInput {
				retitle = true
				target.SourceTitle = ""
				target.TitleRevision++
			}
		}
	} else {
		if _, err = tx.Exec("INSERT INTO sessions VALUES(?,?)", target.ID, blob(target)); err != nil {
			return op, err
		}
		if _, err = tx.Exec(`INSERT INTO items SELECT ?,id,position,body FROM items WHERE session_id=? AND json_extract(body,'$.kind')!='queued' AND json_extract(body,'$.turnId') IN(SELECT id FROM turns WHERE session_id=? AND json_extract(body,'$.order')<=?)`, target.ID, source.ID, source.ID, op.KeepOrder); err != nil {
			return op, err
		}
	}
	if op.Input.Restart {
		for _, table := range []string{"turns", "requests"} {
			col := "id"
			if table == "requests" {
				col = "json_extract(body,'$.turnId')"
			}
			if _, err = tx.Exec("DELETE FROM "+table+" WHERE session_id=? AND "+col+" NOT IN (SELECT COALESCE(json_extract(body,'$.turnId'),'') FROM items WHERE session_id=?)", target.ID, target.ID); err != nil {
				return op, err
			}
		}
		if _, err = tx.Exec(`UPDATE works SET body=json_set(body,'$.canStop',json('false'),'$.canSteer',json('false'),'$.canContinue',json('false')) WHERE session_id=?`, target.ID); err != nil {
			return op, err
		}
		if _, err = tx.Exec("DELETE FROM plans WHERE session_id=?", target.ID); err != nil {
			return op, err
		}
	} else {
		if _, err = tx.Exec(`INSERT INTO turns SELECT ?,id,body FROM turns WHERE session_id=? AND id IN(SELECT json_extract(body,'$.turnId') FROM items WHERE session_id=?)`, target.ID, source.ID, target.ID); err != nil {
			return op, err
		}
		if _, err = tx.Exec(`INSERT INTO works SELECT ?,id,json_set(body,'$.canStop',json('false'),'$.canSteer',json('false'),'$.canContinue',json('false'),'$.state',CASE WHEN json_extract(body,'$.state') IN ('running','starting','waiting') THEN 'unknown' ELSE json_extract(body,'$.state') END) FROM works WHERE session_id=? AND json_extract(body,'$.turnId') IN(SELECT id FROM turns WHERE session_id=?)`, target.ID, source.ID, target.ID); err != nil {
			return op, err
		}
		if _, err = tx.Exec(`INSERT INTO work_items SELECT ?,work_id,id,position,revision,body FROM work_items WHERE session_id=? AND work_id IN(SELECT id FROM works WHERE session_id=?)`, target.ID, source.ID, target.ID); err != nil {
			return op, err
		}
		if _, err = tx.Exec(`INSERT INTO requests SELECT ?,id,json_set(body,'$.sessionId',?) FROM requests WHERE session_id=? AND json_extract(body,'$.turnId') IN(SELECT json_extract(body,'$.turnId') FROM items WHERE session_id=?) AND json_extract(body,'$.state') NOT IN('pending','submitting')`, target.ID, target.ID, source.ID, target.ID); err != nil {
			return op, err
		}
	}
	if target.ThreadID != source.ThreadID {
		target.Goal = nil
	}
	if _, err = tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(target), target.ID); err != nil {
		return op, err
	}
	if op.Input.Restart {
		if _, err = journal(tx, domain.Event{Type: "history.reset", SessionID: target.ID, Session: &target, HistoryTurnOrder: &op.KeepOrder}); err != nil {
			return op, err
		}
		q, err := load[domain.QueueState](tx.QueryRow("SELECT body FROM queues WHERE session_id=?", target.ID))
		if errors.Is(err, sql.ErrNoRows) {
			q = domain.QueueState{SessionID: target.ID}
		} else if err != nil {
			return op, err
		}
		q.Paused = true
		q.Reason = "历史消息已修改"
		q.Revision++
		if err = putQueue(tx, q); err != nil {
			return op, err
		}
		h := sha256.Sum256(blob(op.Submission))
		accepted := domain.Operation{ID: op.ID, SessionID: target.ID, State: "accepted", Digest: hex.EncodeToString(h[:])}
		if _, err = tx.Exec("INSERT INTO operations(id,session_id,state,digest) VALUES(?,?,?,?)", accepted.ID, target.ID, accepted.State, accepted.Digest); err != nil {
			return op, err
		}
		now := time.Now()
		item := domain.Item{ID: "input:" + op.ID, Kind: "user", Text: op.Submission.Text, ReferencePositions: op.Submission.ReferencePositions, Skills: op.Submission.Skills, Attachments: op.Submission.Attachments, Status: "dispatching", Final: true, SentAt: &now}
		if err = putItem(tx, target.ID, &item); err != nil {
			return op, err
		}
		if retitle {
			job := TitleJob{Session: target, Prompt: item.Text, InputID: item.ID}
			if _, err = tx.Exec("INSERT INTO title_jobs VALUES(?,?,?) ON CONFLICT(session_id) DO UPDATE SET state=excluded.state,body=excluded.body", target.ID, titlePending, blob(job)); err != nil {
				return op, err
			}
		}
		if _, err = journal(tx, domain.Event{Type: "submission", SessionID: target.ID, Session: &target, Operation: &accepted, Item: &item}); err != nil {
			return op, err
		}
	} else {
		if _, err = journal(tx, domain.Event{Type: "session", SessionID: target.ID, Session: &target}); err != nil {
			return op, err
		}
	}
	op.Session = target
	op.State = "complete"
	if _, err = tx.Exec("UPDATE history_operations SET body=? WHERE id=?", blob(op), id); err != nil {
		return op, err
	}
	return op, s.commit(tx)
}

// HistoryPending rejects automatic retries after a process died between fork RPC
// and its durable result. No model input has been submitted at that point.
func HistoryPending(op domain.HistoryOperation) error {
	if op.State == "failed" {
		return errors.New(op.Error)
	}
	return fmt.Errorf("历史操作%s，原输入不会自动重发", strings.NewReplacer("prepared", "结果待核对", "forked", "等待完成").Replace(string(op.State)))
}

func putTurn(tx *sql.Tx, sid string, turn *domain.Turn) error {
	var ordinal int64
	err := tx.QueryRow("SELECT COALESCE(json_extract(body,'$.order'),0) FROM turns WHERE session_id=? AND id=?", sid, turn.ID).Scan(&ordinal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if ordinal == 0 {
		if err = tx.QueryRow("SELECT COALESCE(MAX(json_extract(body,'$.order')),0)+1 FROM turns WHERE session_id=?", sid).Scan(&ordinal); err != nil {
			return err
		}
	}
	turn.Order = ordinal
	_, err = tx.Exec("INSERT INTO turns VALUES(?,?,?) ON CONFLICT(session_id,id) DO UPDATE SET body=excluded.body", sid, turn.ID, blob(turn))
	return err
}

func (s *Store) CancelHistory(id, sid string) (domain.HistoryOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, err := load[domain.HistoryOperation](s.db.QueryRow("SELECT body FROM history_operations WHERE id=?", id))
	if err != nil {
		return op, err
	}
	if op.Input.SessionID != sid {
		return op, errors.New("历史操作不属于此会话")
	}
	if op.State == "complete" {
		return op, nil
	}
	op.State = "failed"
	op.Error = "已取消本次操作，原会话保持不变"
	_, err = s.db.Exec("UPDATE history_operations SET body=? WHERE id=?", blob(op), id)
	return op, err
}
