package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"shioji/internal/domain"
)

const inFlightScheduleStates = "('starting','running','approval','unknown')"

func (s *Store) SaveSchedule(task domain.Schedule, now time.Time, zone *time.Location) (domain.Schedule, error) {
	task.Name = strings.TrimSpace(task.Name)
	if task.ID == "" || len(task.ID) > 128 || task.Name == "" || utf8.RuneCountInString(task.Name) > 90 ||
		!task.Rule.Valid() || task.Submission("validate").ValidateContent() != nil {
		return domain.Schedule{}, errors.New("请输入有效的任务名称、触发时间和提示词")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", task.WorkspaceID))
	if err != nil {
		return domain.Schedule{}, err
	}
	if w.Archived || task.DeviceID != "" && task.DeviceID != s.deviceID {
		return domain.Schedule{}, errors.New("定时任务需要此设备上的未归档工作区")
	}
	old, err := load[domain.Schedule](s.db.QueryRow("SELECT body FROM schedules WHERE id=?", task.ID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.Schedule{}, err
	}
	if task.Revision != old.Revision || old.ID != "" && old.WorkspaceID != task.WorkspaceID {
		return domain.Schedule{}, domain.ErrScheduleConflict
	}
	task.DeviceID, task.Revision, task.Latest, task.Zone = s.deviceID, old.Revision+1, nil, zone.String()
	task.NextAt = time.Time{}
	if task.Enabled {
		if old.Enabled && old.Rule.Equal(task.Rule) && (old.Zone == task.Zone || task.Rule.Kind == "interval") {
			task.NextAt = old.NextAt
		} else {
			task.NextAt = task.Rule.Next(now, zone)
			if task.NextAt.IsZero() {
				return domain.Schedule{}, errors.New("请选择将来的有效触发时间")
			}
		}
	}
	_, err = s.db.Exec("INSERT INTO schedules(id,workspace_id,body) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", task.ID, task.WorkspaceID, blob(task))
	return task, err
}

func (s *Store) DeleteSchedule(id string, revision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec("DELETE FROM schedules WHERE id=? AND json_extract(body,'$.revision')=?", id, revision)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 1 {
		return err
	}
	return domain.ErrScheduleConflict
}

func (s *Store) Schedules() ([]domain.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rowsOf[domain.Schedule](s.db, `
		SELECT json_set(json_remove(s.body,'$.text'),'$.latest',
		  json((SELECT r.body FROM schedule_runs r WHERE r.schedule_id=s.id ORDER BY r.rowid DESC LIMIT 1)))
		FROM schedules s ORDER BY s.rowid`)
}

func (s *Store) ScheduleDetail(id string) (domain.ScheduleDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out domain.ScheduleDetail
	task, err := load[domain.Schedule](s.db.QueryRow("SELECT body FROM schedules WHERE id=?", id))
	if err != nil {
		return out, err
	}
	runs, err := rowsOf[domain.ScheduleRun](s.db, `
	 SELECT json_set(r.body,'$.sessionLink',CASE
	   WHEN json_extract(r.body,'$.sessionId') IS NULL THEN ''
	   WHEN s.id IS NULL THEN 'deleted'
	   WHEN json_extract(s.body,'$.archived')=1 OR json_extract(w.body,'$.archived')=1 THEN 'archived'
	   ELSE 'open' END)
	 FROM schedule_runs r
	 LEFT JOIN sessions s ON s.id=json_extract(r.body,'$.sessionId')
	 LEFT JOIN workspaces w ON w.id=json_extract(s.body,'$.workspaceId')
	 WHERE r.schedule_id=? ORDER BY r.rowid DESC LIMIT 100`, id)
	return domain.ScheduleDetail{Schedule: task, Runs: runs}, err
}

// QueueScheduleRun durably acknowledges an explicit click before the scheduler acts.
func (s *Store) QueueScheduleRun(id string, revision int64, runID string, now time.Time) (domain.ScheduleRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := load[domain.ScheduleRun](s.db.QueryRow("SELECT body FROM schedule_runs WHERE id=?", runID))
	if err == nil {
		if existing.ScheduleID != id || !existing.Manual {
			return domain.ScheduleRun{}, errors.New("运行标识已被占用")
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.ScheduleRun{}, err
	}
	task, err := load[domain.Schedule](s.db.QueryRow(`
	 SELECT s.body FROM schedules s JOIN workspaces w ON w.id=s.workspace_id
	 WHERE s.id=? AND COALESCE(json_extract(w.body,'$.archived'),0)=0`, id))
	if err != nil {
		return domain.ScheduleRun{}, err
	}
	if task.Revision != revision {
		return domain.ScheduleRun{}, domain.ErrScheduleConflict
	}
	run := domain.ScheduleRun{ID: runID, ScheduleID: id, At: now.UTC(), Manual: true, State: domain.ScheduleQueued}
	written, err := insertScheduleRun(s.db, run, task)
	if err == nil && !written {
		err = errors.New("已有一次运行在等待")
	}
	return run, err
}

func insertScheduleRun(q interface {
	Exec(string, ...any) (sql.Result, error)
}, run domain.ScheduleRun, task domain.Schedule) (bool, error) {
	var input []byte
	if run.State == domain.ScheduleQueued {
		input = blob(task.Submission(run.ID))
	}
	result, err := q.Exec("INSERT INTO schedule_runs(id,schedule_id,state,body,input) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING", run.ID, run.ScheduleID, run.State, blob(run), input)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// AdvanceSchedules commits the next trigger and its run record together.
func (s *Store) AdvanceSchedules(now time.Time, zone *time.Location, recovering bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	waiting, err := rowsOf[domain.ScheduleRun](tx, "SELECT body FROM schedule_runs WHERE state='queued' AND COALESCE(json_extract(body,'$.manual'),0)=0")
	if err != nil {
		return err
	}
	automatic := make(map[string]domain.ScheduleRun, len(waiting))
	for _, run := range waiting {
		if now.Sub(run.At) > 24*time.Hour {
			if err := missScheduledRun(tx, run, now, "超过 24 小时补跑窗口"); err != nil {
				return err
			}
			continue
		}
		automatic[run.ScheduleID] = run
	}
	tasks, err := rowsOf[domain.Schedule](tx, "SELECT body FROM schedules WHERE json_extract(body,'$.nextAt') IS NOT NULL")
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Zone != zone.String() {
			task.Zone = zone.String()
			switch task.Rule.Kind {
			case "once":
				task.NextAt = task.Rule.Next(time.Time{}, zone)
			case "daily", "weekly":
				task.NextAt = task.Rule.Next(now, zone)
			}
			if _, err = tx.Exec("UPDATE schedules SET body=? WHERE id=?", blob(task), task.ID); err != nil {
				return err
			}
		}
		if task.NextAt.IsZero() || task.NextAt.After(now) {
			continue
		}
		w, err := load[domain.Workspace](tx.QueryRow("SELECT body FROM workspaces WHERE id=?", task.WorkspaceID))
		if err != nil {
			return err
		}
		at, next := task.Rule.Advance(task.NextAt, now, zone)
		task.NextAt = next
		if _, err = tx.Exec("UPDATE schedules SET body=? WHERE id=?", blob(task), task.ID); err != nil {
			return err
		}
		run := domain.ScheduleRun{ID: domain.NewID(), ScheduleID: task.ID, At: at, State: domain.ScheduleQueued}
		switch {
		case w.Archived:
			run.State, run.Error = domain.ScheduleMissed, "工作区已归档"
		case now.Sub(at) > 24*time.Hour:
			run.State, run.Error = domain.ScheduleMissed, "超过 24 小时补跑窗口"
		}
		if run.State == domain.ScheduleMissed {
			run.FinishedAt = now.UTC()
		} else if queued := automatic[task.ID]; recovering && queued.ID != "" && at.After(queued.At) {
			if err := missScheduledRun(tx, queued, now, "恢复后仅补最近一次"); err != nil {
				return err
			}
		}
		if _, err = insertScheduleRun(tx, run, task); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ScheduledQueue() ([]domain.ScheduleRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rowsOf[domain.ScheduleRun](s.db, "SELECT body FROM schedule_runs WHERE state='queued' AND NOT EXISTS(SELECT 1 FROM schedule_runs a WHERE a.schedule_id=schedule_runs.schedule_id AND a.state IN "+inFlightScheduleStates+") ORDER BY rowid")
}

func (s *Store) ClaimScheduleRun(run domain.ScheduleRun, now time.Time) (domain.Submission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input, err := load[domain.Submission](s.db.QueryRow("SELECT input FROM schedule_runs WHERE id=? AND state='queued'", run.ID))
	if err != nil {
		return input, err
	}
	run.State, run.StartedAt = domain.ScheduleStarting, now.UTC()
	result, err := s.db.Exec("UPDATE schedule_runs SET state=?,body=? WHERE id=? AND state='queued'", run.State, blob(run), run.ID)
	if err != nil {
		return input, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return input, errors.New("定时运行已改变")
	}
	return input, nil
}

func (s *Store) UpdateScheduleRun(run domain.ScheduleRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return updateScheduleRun(s.db, run)
}

func updateScheduleRun(q interface {
	Exec(string, ...any) (sql.Result, error)
}, run domain.ScheduleRun) error {
	keepInput := run.State == domain.ScheduleQueued || run.State == domain.ScheduleStarting && run.SessionID == ""
	_, err := q.Exec("UPDATE schedule_runs SET state=?,body=?,input=CASE WHEN ? THEN input ELSE NULL END WHERE id=?", run.State, blob(run), keepInput, run.ID)
	return err
}

func missScheduledRun(tx *sql.Tx, run domain.ScheduleRun, now time.Time, reason string) error {
	run.State, run.Error, run.FinishedAt = domain.ScheduleMissed, reason, now.UTC()
	return updateScheduleRun(tx, run)
}

func preserveScheduledRunOutcome(tx *sql.Tx, sessionID string, now time.Time, reason string) error {
	runs, err := rowsOf[domain.ScheduleRun](tx, `
		SELECT r.body FROM operations o JOIN schedule_runs r ON r.id=substr(o.id,length('scheduled:')+1)
		WHERE o.session_id=? AND o.id LIKE 'scheduled:%' AND r.state IN `+inFlightScheduleStates, sessionID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		run.SessionID = sessionID
		if err := observeScheduledTurn(tx, &run); err != nil {
			return err
		}
		switch run.State {
		case domain.ScheduleCompleted, domain.ScheduleFailed, domain.ScheduleInterrupted:
		default:
			run.State, run.Error = domain.ScheduleInterrupted, reason
		}
		if run.FinishedAt.IsZero() {
			run.FinishedAt = now
		}
		if err := updateScheduleRun(tx, run); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileScheduledRuns reads only the accepted input and its first turn.
// Terminal run results remain durable even after the conversation is removed.
func (s *Store) ReconcileScheduledRuns(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs, err := rowsOf[domain.ScheduleRun](s.db, "SELECT body FROM schedule_runs WHERE state IN "+inFlightScheduleStates)
	if err != nil {
		return err
	}
	for _, run := range runs {
		state := run.State
		op, err := operation(s.db, domain.ScheduleOperationID(run.ID))
		switch {
		case errors.Is(err, sql.ErrNoRows):
			run.State, run.Error = domain.ScheduleInterrupted, "执行服务在接纳前停止"
		case err != nil:
			return err
		case op.Deleted:
			run.State, run.Error = domain.ScheduleInterrupted, "会话已删除"
		case op.State == domain.OperationRejected || op.State == domain.OperationAbandoned:
			run.State, run.Error = domain.ScheduleFailed, op.Error
		default:
			run.SessionID = op.SessionID
			if err = observeScheduledTurn(s.db, &run); err != nil {
				return err
			}
		}
		if op.SessionID != "" {
			run.SessionID = op.SessionID
		}
		if run.State == state {
			continue
		}
		switch run.State {
		case domain.ScheduleInterrupted, domain.ScheduleFailed, domain.ScheduleCompleted:
			if run.FinishedAt.IsZero() {
				run.FinishedAt = now.UTC()
			}
		}
		if err = updateScheduleRun(s.db, run); err != nil {
			return err
		}
	}
	return nil
}

func observeScheduledTurn(q historyQuerier, run *domain.ScheduleRun) error {
	first, err := load[domain.Turn](q.QueryRow("SELECT body FROM turns WHERE session_id=? ORDER BY json_extract(body,'$.order'),rowid LIMIT 1", run.SessionID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	status := first.Status
	if errors.Is(err, sql.ErrNoRows) {
		session, err := load[domain.Session](q.QueryRow("SELECT body FROM sessions WHERE id=?", run.SessionID))
		if err != nil {
			return err
		}
		status = session.Status
	}
	switch status {
	case domain.ExecutionCompleted:
		run.State = domain.ScheduleCompleted
	case domain.ExecutionFailed:
		run.State, run.Error = domain.ScheduleFailed, first.Error
	case domain.ExecutionInterrupted, domain.ExecutionAbandoned:
		run.State = domain.ScheduleInterrupted
	case domain.ExecutionUnknown:
		run.State = domain.ScheduleUnknown
	case domain.ExecutionRunning:
		var pending bool
		if err := q.QueryRow("SELECT EXISTS(SELECT 1 FROM requests WHERE session_id=? AND json_extract(body,'$.state') IN ('pending','submitting'))", run.SessionID).Scan(&pending); err != nil {
			return err
		}
		run.State = domain.ScheduleRunning
		if pending {
			run.State = domain.ScheduleApproval
		}
	default:
		run.State = domain.ScheduleStarting
	}
	if first.CompletedAt != nil {
		run.FinishedAt = first.CompletedAt.UTC()
	}
	return nil
}
