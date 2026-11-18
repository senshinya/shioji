package domain

import (
	"errors"
	"slices"
	"time"
)

type ScheduleRule struct {
	Kind    string         `json:"kind"`
	Date    string         `json:"date,omitempty"`
	Time    string         `json:"time,omitempty"`
	Days    []time.Weekday `json:"days,omitempty"`
	Minutes int            `json:"minutes,omitempty"`
}

func (r ScheduleRule) Valid() bool {
	if r.Kind == "interval" {
		return r.Minutes >= 1 && r.Minutes <= 43200
	}
	if _, err := time.Parse("15:04", r.Time); err != nil || len(r.Time) != 5 {
		return false
	}
	switch r.Kind {
	case "once":
		_, err := time.Parse(time.DateOnly, r.Date)
		return err == nil
	case "daily":
		return true
	case "weekly":
		if len(r.Days) == 0 || len(r.Days) > 7 {
			return false
		}
		for _, d := range r.Days {
			if d < time.Sunday || d > time.Saturday {
				return false
			}
		}
		return true
	}
	return false
}

func (r ScheduleRule) Equal(other ScheduleRule) bool {
	return r.Kind == other.Kind && r.Date == other.Date && r.Time == other.Time &&
		r.Minutes == other.Minutes && slices.Equal(r.Days, other.Days)
}

// Next uses the device's civil clock for calendar rules and elapsed time for intervals.
func (r ScheduleRule) Next(after time.Time, zone *time.Location) time.Time {
	if !r.Valid() {
		return time.Time{}
	}
	if r.Kind == "interval" {
		return after.Add(time.Duration(r.Minutes) * time.Minute).UTC()
	}
	if r.Kind == "once" {
		at, _ := time.ParseInLocation(time.DateOnly+" 15:04", r.Date+" "+r.Time, zone)
		if at.After(after) && at.Format(time.DateOnly+" 15:04") == r.Date+" "+r.Time {
			return at.UTC()
		}
		return time.Time{}
	}
	local := after.In(zone)
	day := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, zone)
	for i := 0; i <= 14; i++ {
		if r.Kind == "daily" || slices.Contains(r.Days, day.Weekday()) {
			stamp := day.Format(time.DateOnly) + " " + r.Time
			at, _ := time.ParseInLocation(time.DateOnly+" 15:04", stamp, zone)
			if at.After(after) && at.Format(time.DateOnly+" 15:04") == stamp {
				return at.UTC()
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}
}

// Advance returns the latest elapsed occurrence and the first future occurrence.
func (r ScheduleRule) Advance(first, now time.Time, zone *time.Location) (time.Time, time.Time) {
	if first.IsZero() || first.After(now) {
		return time.Time{}, first
	}
	switch r.Kind {
	case "once":
		return first, time.Time{}
	case "interval":
		interval := time.Duration(r.Minutes) * time.Minute
		latest := first.Add(now.Sub(first) / interval * interval)
		return latest, latest.Add(interval)
	}
	latest := first
	cursor := first
	if floor := now.In(zone).AddDate(0, 0, -8); cursor.Before(floor) {
		cursor = floor
	}
	for {
		next := r.Next(cursor, zone)
		if next.IsZero() || next.After(now) {
			return latest, next
		}
		latest, cursor = next, next
	}
}

type Schedule struct {
	ID          string       `json:"id"`
	DeviceID    string       `json:"deviceId"`
	WorkspaceID string       `json:"workspaceId"`
	Name        string       `json:"name"`
	Text        string       `json:"text,omitempty"`
	HarnessID   string       `json:"harnessId,omitempty"`
	Config      RunConfig    `json:"config"`
	Git         GitChoice    `json:"git,omitempty,omitzero"`
	Rule        ScheduleRule `json:"rule"`
	Zone        string       `json:"zone,omitempty"`
	Enabled     bool         `json:"enabled"`
	NextAt      time.Time    `json:"nextAt,omitzero"`
	Revision    int64        `json:"revision"`
	Latest      *ScheduleRun `json:"latest,omitempty"`
}

func (s Schedule) Submission(runID string) Submission {
	return Submission{OperationID: ScheduleOperationID(runID), WorkspaceID: s.WorkspaceID,
		HarnessID: s.HarnessID, Text: s.Text, Config: s.Config, Git: s.Git}
}

func ScheduleOperationID(runID string) string { return "scheduled:" + runID }

type ScheduleRunState string

const (
	ScheduleQueued      ScheduleRunState = "queued"
	ScheduleStarting    ScheduleRunState = "starting"
	ScheduleRunning     ScheduleRunState = "running"
	ScheduleApproval    ScheduleRunState = "approval"
	ScheduleCompleted   ScheduleRunState = "completed"
	ScheduleFailed      ScheduleRunState = "failed"
	ScheduleInterrupted ScheduleRunState = "interrupted"
	ScheduleMissed      ScheduleRunState = "missed"
	ScheduleUnknown     ScheduleRunState = "unknown"
)

type ScheduleRun struct {
	ID          string           `json:"id"`
	ScheduleID  string           `json:"scheduleId"`
	At          time.Time        `json:"at"`
	StartedAt   time.Time        `json:"startedAt,omitzero"`
	FinishedAt  time.Time        `json:"finishedAt,omitzero"`
	Manual      bool             `json:"manual,omitempty"`
	State       ScheduleRunState `json:"state"`
	SessionID   string           `json:"sessionId,omitempty"`
	SessionLink string           `json:"sessionLink,omitempty"`
	Error       string           `json:"error,omitempty"`
}

type ScheduleDetail struct {
	Schedule Schedule      `json:"schedule"`
	Runs     []ScheduleRun `json:"runs"`
}

var ErrScheduleConflict = errors.New("定时任务已在其他客户端更新，请重新加载")
