package worker

import (
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (s *Server) handleSchedule(method string, body []byte) (any, error) {
	switch method {
	case "schedule.zone":
		return scheduleZone().String(), nil
	case "schedule.list":
		tasks, err := s.store.Schedules()
		zone := scheduleZone().String()
		for i := range tasks {
			tasks[i].Zone = zone
		}
		return tasks, err
	case "schedule.get":
		q, err := decode[protocol.ScheduleCommand](body)
		if err != nil {
			return nil, err
		}
		detail, err := s.store.ScheduleDetail(q.ID)
		detail.Schedule.Zone = scheduleZone().String()
		return detail, err
	}

	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.updateAdmission(); err != nil {
		return nil, err
	}
	defer s.wakeSchedules()
	now := time.Now()
	if method == "schedule.save" {
		task, err := decode[domain.Schedule](body)
		if err != nil {
			return nil, err
		}
		zone := scheduleZone()
		task.Zone = zone.String()
		return s.store.SaveSchedule(task, now, zone)
	}
	q, err := decode[protocol.ScheduleCommand](body)
	if err != nil {
		return nil, err
	}
	if method == "schedule.delete" {
		return nil, s.store.DeleteSchedule(q.ID, q.Revision)
	}
	if q.RunID == "" || len(q.RunID) > 128 {
		return nil, errors.New("立即运行需要有效的运行标识")
	}
	return s.store.QueueScheduleRun(q.ID, q.Revision, q.RunID, now)
}

func scheduleZone() *time.Location {
	if name := os.Getenv("TZ"); name != "" && name != "Local" {
		if loc, err := time.LoadLocation(strings.TrimPrefix(name, ":")); err == nil {
			return loc
		}
	}
	if path, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(path, "zoneinfo/"); ok {
			if loc, err := time.LoadLocation(name); err == nil {
				return loc
			}
		}
	}
	return time.Local
}

func (s *Server) wakeSchedules() {
	select {
	case s.scheduleWake <- struct{}{}:
	default:
	}
}

func (s *Server) scheduleLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	recovering := true
	for {
		if !s.updating.Load() && !s.backgroundStopping.Load() && s.ctx.Err() == nil {
			if err := s.processSchedules(time.Now(), recovering); err != nil {
				if s.ctx.Err() == nil {
					log.Printf("scheduled tasks: %v", err)
				}
			} else {
				recovering = false
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.scheduleWake:
		case <-ticker.C:
		}
	}
}

func (s *Server) processSchedules(now time.Time, recovering bool) error {
	if err := s.store.ReconcileScheduledRuns(now); err != nil {
		return err
	}
	if err := s.store.AdvanceSchedules(now, scheduleZone(), recovering); err != nil {
		return err
	}
	runs, err := s.store.ScheduledQueue()
	if err != nil {
		return err
	}
	for _, run := range runs {
		s.dispatchMu.Lock()
		if s.ctx.Err() != nil || s.updateAdmission() != nil {
			s.dispatchMu.Unlock()
			return nil
		}
		input, err := s.store.ClaimScheduleRun(run, now)
		if err != nil {
			s.dispatchMu.Unlock()
			return err
		}
		run.State, run.StartedAt = domain.ScheduleStarting, now.UTC()
		op, launchErr := s.submitLocked(input, 0, false)
		run.SessionID = op.SessionID
		if launchErr != nil {
			var remote *protocol.RemoteError
			if errors.Is(launchErr, errCatalogRefreshing) || errors.As(launchErr, &remote) && remote.Code == "notSent" {
				run.State, run.StartedAt = domain.ScheduleQueued, time.Time{}
			} else {
				run.State, run.Error, run.FinishedAt = domain.ScheduleFailed, launchErr.Error(), time.Now().UTC()
			}
		}
		err = s.store.UpdateScheduleRun(run)
		s.dispatchMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
