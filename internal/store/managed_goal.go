package store

import (
	"database/sql"
	"errors"
	"time"

	"shioji/internal/domain"
)

// BeginGoalInputHandoff serializes goal control with the durable input grant.
func (s *Store) BeginGoalInputHandoff(input, source, objective string, before *domain.Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	op, err := operation(tx, input)
	if err != nil {
		return err
	}
	if op.State != domain.OperationAccepted {
		return ErrInputHandoffRevoked
	}
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", op.SessionID))
	if err != nil {
		return err
	}
	if source == "" || session.ThreadID != source || session.Archived || session.ExecutionGuard != "" {
		return errors.New("目标所属执行环境已变化")
	}
	if before == nil {
		if objective == "" || session.Goal.Active() {
			return errors.New("目标已改变")
		}
		session.Goal = &domain.Goal{ID: input, Revision: 1, RunID: input, Objective: objective, CreatedAt: time.Now().Unix(), State: domain.GoalActive}
		if err = saveManagedGoal(tx, &session); err != nil {
			return err
		}
	} else if !session.Goal.Active() || !session.Goal.SameTarget(before) || session.Goal.RunID != input {
		return errors.New("目标已暂停或改变，本次续跑未发送")
	}
	if err = setOperation(tx, op, domain.OperationDispatched, ""); err != nil {
		return err
	}
	return s.commit(tx)
}

func (s *Store) AcceptManagedGoalContinuation(in domain.Submission, goal *domain.Goal, launch domain.LaunchContext) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acceptLocked(in, 0, false, "", false, goal, launch)
}

func (s *Store) ManagedGoalsReady() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rowsOf[string](s.db, `SELECT json_quote(id) FROM sessions WHERE
		json_extract(body,'$.goal.state')='active' AND COALESCE(json_extract(body,'$.goal.continueFrom'),'')<>''
		AND COALESCE(json_extract(body,'$.archived'),0)=0 AND COALESCE(json_extract(body,'$.contextChanged'),0)=0
		AND COALESCE(json_extract(body,'$.executionGuard'),'')='' AND json_extract(body,'$.status') IN ('idle','completed','interrupted','failed','abandoned')
		AND NOT EXISTS(SELECT 1 FROM operations WHERE session_id=sessions.id AND state IN ('accepted','dispatched','indeterminate')) ORDER BY rowid LIMIT 32`)
}

func (s *Store) ChangeManagedGoal(in domain.GoalRequest) (domain.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var op domain.Operation
	tx, err := s.db.Begin()
	if err != nil {
		return op, err
	}
	defer tx.Rollback()
	session, err := load[domain.Session](tx.QueryRow("SELECT body FROM sessions WHERE id=?", in.SessionID))
	if err != nil {
		return op, err
	}
	if !session.Goal.Managed() || !session.Goal.SameTarget(&in.Target) || session.Archived {
		return op, errors.New("目标状态已改变")
	}
	goal := *session.Goal
	goal.Revision++
	switch in.Action {
	case domain.PauseGoal:
		if !goal.Active() {
			return op, errors.New("当前目标无法暂停")
		}
		goal.State, goal.ContinueFrom = domain.GoalPaused, ""
		session.Goal = &goal
	case domain.ResumeGoal:
		if !goal.CanResume() || !session.Status.CanSubmit() || session.ExecutionGuard != "" || session.ContextChanged {
			return op, errors.New("当前目标无法恢复")
		}
		goal.State, goal.RunID, goal.ContinueFrom = domain.GoalActive, "", in.OperationID
		session.Goal = &goal
	case domain.ClearGoal:
		session.Goal = nil
	default:
		return op, errors.New("无效的目标操作")
	}
	op = domain.Operation{ID: in.OperationID, SessionID: in.SessionID, Kind: goalOperationKind, State: domain.OperationConfirmed, Digest: goalDigest(in)}
	if _, err = tx.Exec("INSERT INTO operations(id,session_id,state,digest,kind) VALUES(?,?,?,?,?)", op.ID, op.SessionID, op.State, op.Digest, op.Kind); err != nil {
		return op, err
	}
	if _, err = journal(tx, domain.Event{Type: "operation", SessionID: op.SessionID, Operation: &op}); err != nil {
		return op, err
	}
	if err = saveManagedGoal(tx, &session); err != nil {
		return op, err
	}
	return op, s.commit(tx)
}

func saveManagedGoal(tx *sql.Tx, session *domain.Session) error {
	session.Revision++
	if _, err := tx.Exec("UPDATE sessions SET body=? WHERE id=?", blob(session), session.ID); err != nil {
		return err
	}
	_, err := journal(tx, domain.Event{Type: "goal", SessionID: session.ID, Session: session, Goal: session.Goal})
	return err
}

func (s *Store) PauseManagedGoalForStop(id, goalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil || !session.Goal.Active() || session.Goal.ID != goalID || goalID == "" {
		return err
	}
	goal := *session.Goal
	goal.Revision++
	goal.State, goal.ContinueFrom = domain.GoalPaused, ""
	_, err = s.applyLocked(id, domain.Event{Type: "goal", GoalSource: session.ThreadID, Goal: &goal})
	return err
}

func (s *Store) BlockManagedGoal(id, run string) error {
	return s.blockManagedGoal(id, run, nil)
}

func (s *Store) BlockManagedGoalContinuation(id string, expected *domain.Goal) error {
	return s.blockManagedGoal(id, "", expected)
}

func (s *Store) blockManagedGoal(id, run string, expected *domain.Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := load[domain.Session](s.db.QueryRow("SELECT body FROM sessions WHERE id=?", id))
	if err != nil {
		return err
	}
	if !session.Goal.Managed() || !session.Goal.Active() || session.Goal.RunID != run ||
		expected != nil && *session.Goal != *expected {
		return nil
	}
	goal := *session.Goal
	goal.Revision++
	goal.State, goal.RunID, goal.ContinueFrom = domain.GoalBlocked, "", ""
	_, err = s.applyLocked(id, domain.Event{Type: "goal", GoalSource: session.ThreadID, Goal: &goal})
	return err
}

func settleManagedGoal(session *domain.Session, turn *domain.Turn, progress *domain.GoalProgress) {
	if !session.Goal.Managed() || !session.Goal.Active() || session.Goal.RunID == "" || turn.Status.Active() ||
		progress == nil || progress.GoalID != session.Goal.ID || progress.RunID != session.Goal.RunID {
		return
	}
	goal := *session.Goal
	goal.Revision++
	goal.State = domain.GoalBlocked
	if turn.Status == domain.ExecutionInterrupted {
		goal.State = domain.GoalPaused
	}
	if turn.Status == domain.ExecutionCompleted {
		switch progress.Outcome {
		case domain.GoalComplete, domain.GoalBlocked:
			goal.State = progress.Outcome
		case domain.GoalActive:
			goal.State, goal.ContinueFrom = domain.GoalActive, turn.ID
		}
	}
	goal.RunID = ""
	if turn.DurationMS != nil {
		goal.ElapsedSeconds += *turn.DurationMS / 1000
	}
	session.Goal = &goal
}
