package store

import (
	"path/filepath"
	"testing"
	"time"

	"shioji/internal/domain"
)

func TestScheduleCatchupAndDefinitionSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWorkspace("Owned", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("Taipei", 8*60*60)
	start := time.Date(2026, 11, 15, 8, 0, 0, 0, zone)
	task, err := s.SaveSchedule(domain.Schedule{ID: "daily", WorkspaceID: w.ID, Name: "daily", Text: "work", Enabled: true,
		Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"}}, start, zone)
	if err != nil {
		t.Fatal(err)
	}
	once, err := s.SaveSchedule(domain.Schedule{ID: "once", WorkspaceID: w.ID, Name: "once", Text: "work", Enabled: true,
		Rule: domain.ScheduleRule{Kind: "once", Date: "2026-11-15", Time: "09:30"}}, start, zone)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := start.AddDate(0, 0, 3).Add(2 * time.Hour)
	for range 2 {
		if err := s.AdvanceSchedules(now, zone, true); err != nil {
			t.Fatal(err)
		}
	}
	detail, err := s.ScheduleDetail(task.ID)
	if err != nil || len(detail.Runs) != 1 || detail.Runs[0].State != domain.ScheduleQueued ||
		!detail.Runs[0].At.Equal(now.Add(-time.Hour)) || detail.Schedule.Text != task.Text ||
		!detail.Schedule.NextAt.Equal(now.Add(23*time.Hour)) {
		t.Fatal(detail, err)
	}
	detail, err = s.ScheduleDetail(once.ID)
	if err != nil || len(detail.Runs) != 1 || detail.Runs[0].State != domain.ScheduleMissed || !detail.Schedule.NextAt.IsZero() {
		t.Fatal(detail, err)
	}
}

func TestScheduleResultSurvivesDeletionBeforeReconciliation(t *testing.T) {
	s, w, _ := archiveFixture(t)
	now := time.Now()
	task, err := s.SaveSchedule(domain.Schedule{ID: "keep-outcome", WorkspaceID: w.ID, Name: "test", Text: "work",
		Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"}}, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.QueueScheduleRun(task.ID, task.Revision, "fast-run", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimScheduleRun(run, now); err != nil {
		t.Fatal(err)
	}
	session := archiveCompleted(t, s, w.ID, domain.ScheduleOperationID(run.ID))
	archived, err := s.SetSessionArchived(session.ID, session.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeleteArchivedSession(session.ID, archived.Revision); err != nil {
		t.Fatal(err)
	}
	detail, err := s.ScheduleDetail(task.ID)
	if err != nil || len(detail.Runs) != 1 || detail.Runs[0].State != domain.ScheduleCompleted || detail.Runs[0].SessionLink != "deleted" {
		t.Fatal("session deletion discarded a completed run's outcome", detail, err)
	}
}

func TestScheduleRecoveryPrefersNewestOccurrenceOverOlderAutomaticWait(t *testing.T) {
	s, w, _ := archiveFixture(t)
	zone := time.UTC
	start := time.Date(2026, 11, 18, 8, 0, 0, 0, zone)
	task, err := s.SaveSchedule(domain.Schedule{ID: "waiting", WorkspaceID: w.ID, Name: "waiting", Text: "work", Enabled: true,
		Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"}}, start, zone)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSchedules(start.Add(2*time.Hour), zone, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSchedules(start.Add(26*time.Hour), zone, true); err != nil {
		t.Fatal(err)
	}
	queue, err := s.ScheduledQueue()
	if err != nil || len(queue) != 1 || !queue[0].At.Equal(task.NextAt.AddDate(0, 0, 1)) {
		t.Fatal("recovery selected a stale queued slot", queue, err)
	}
}

func TestScheduleRebasesCivilTimeAfterDeviceZoneChanges(t *testing.T) {
	s, w, _ := archiveFixture(t)
	taipei, _ := time.LoadLocation("Asia/Taipei")
	la, _ := time.LoadLocation("America/Los_Angeles")
	start := time.Date(2026, 11, 18, 0, 30, 0, 0, time.UTC)
	task, err := s.SaveSchedule(domain.Schedule{ID: "zone", WorkspaceID: w.ID, Name: "clock", Text: "work", Enabled: true,
		Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"}}, start, taipei)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSchedules(start.Add(time.Hour), la, true); err != nil {
		t.Fatal(err)
	}
	detail, err := s.ScheduleDetail(task.ID)
	want := time.Date(2026, 11, 18, 9, 0, 0, 0, la)
	if err != nil || len(detail.Runs) != 0 || !detail.Schedule.NextAt.Equal(want) {
		t.Fatal("calendar schedule remained in previous timezone", detail, err)
	}
}

func TestScheduleAllowsOneWaitingRunWhileActive(t *testing.T) {
	s, w, _ := archiveFixture(t)
	now := time.Now()
	task, err := s.SaveSchedule(domain.Schedule{ID: "bounded", WorkspaceID: w.ID, Name: "test", Text: "work",
		Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"}}, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.QueueScheduleRun(task.ID, task.Revision, "active", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimScheduleRun(active, now); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.QueueScheduleRun(task.ID, task.Revision, "waiting", now)
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := s.QueueScheduleRun(task.ID, task.Revision, waiting.ID, now.Add(time.Minute)); err != nil || receipt != waiting {
		t.Fatal("retry did not reuse the first receipt", receipt, err)
	}
	if _, err := s.QueueScheduleRun(task.ID, task.Revision, "excess", now); err == nil {
		t.Fatal("accepted a second waiting run")
	}
	if queue, err := s.ScheduledQueue(); err != nil || len(queue) != 0 {
		t.Fatal("waiting run was made dispatchable during an active run", queue, err)
	}
}

func TestScheduleKeepsOriginalResultWhenFirstPromptIsReplaced(t *testing.T) {
	s, w, _ := archiveFixture(t)
	now := time.Now()
	task, err := s.SaveSchedule(domain.Schedule{ID: "original", WorkspaceID: w.ID, Name: "test", Text: "work",
		Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"}}, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.QueueScheduleRun(task.ID, task.Revision, "replace", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimScheduleRun(run, now); err != nil {
		t.Fatal(err)
	}
	session := archiveCompleted(t, s, w.ID, domain.ScheduleOperationID(run.ID))
	session.ThreadID = "original-thread"
	if _, err = s.Apply(session.ID, domain.Event{Type: "session", Session: &session}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(session.ID, domain.Event{Type: "item.upsert", Item: &domain.Item{ID: "input:" + domain.ScheduleOperationID(run.ID),
		Kind: "user", TurnID: "t", Text: task.Text, Status: "sent", Final: true}}); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Session(session.ID, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	change, err := s.PrepareHistory(domain.HistoryChange{OperationID: "replace-first", SessionID: session.ID, Revision: detail.Session.Revision,
		Restart: true, Text: "edited", ItemID: "input:" + domain.ScheduleOperationID(run.ID), Config: detail.Session.Config})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetHistoryThread(change.ID, "replacement-thread"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitHistory(change.ID); err != nil {
		t.Fatal(err)
	}
	result, err := s.ScheduleDetail(task.ID)
	if err != nil || len(result.Runs) != 1 || result.Runs[0].State != domain.ScheduleCompleted || result.Runs[0].SessionID != session.ID {
		t.Fatal("replacing history altered the original run", result, err)
	}
}

func TestAcceptedScheduledRunKeepsItsOriginalInputAfterTaskEdit(t *testing.T) {
	s, w, _ := archiveFixture(t)
	now := time.Now()
	task, err := s.SaveSchedule(domain.Schedule{ID: "run-draft", WorkspaceID: w.ID, Name: "test", Text: "original prompt",
		Config: domain.RunConfig{Permission: "read-only"}, Rule: domain.ScheduleRule{Kind: "daily", Time: "09:00"}}, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.QueueScheduleRun(task.ID, task.Revision, "preserve", now)
	if err != nil {
		t.Fatal(err)
	}
	task.Text, task.Config.Permission = "edited prompt", "default"
	if _, err := s.SaveSchedule(task, now, time.UTC); err != nil {
		t.Fatal(err)
	}
	input, err := s.ClaimScheduleRun(run, now)
	if err != nil || input.Text != "original prompt" || input.Config.Permission != "read-only" {
		t.Fatal("an accepted run picked up later edits", input, err)
	}
}
