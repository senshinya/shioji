package main

import (
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

const mobileScheduleDetailRefresh = 4 * time.Second

type scheduleObservation struct {
	request mobileRequest
	next    time.Time
	epoch   string
	fresh   bool
}

func sameSchedule(source, actual domain.Schedule) bool {
	return source.ID == actual.ID && source.DeviceID == actual.DeviceID && source.WorkspaceID == actual.WorkspaceID
}

func (a *mobileApp) scheduleConnected(task domain.Schedule) bool {
	return a.model.Connected && a.model.ConnectionEpoch != "" && a.model.WorkerMinor >= protocol.ScheduleMinor &&
		a.model.Snapshot.DeviceID == task.DeviceID
}

func (a *mobileApp) observeSchedule(c *ui.Context, w *mygo.Window, read *scheduleObservation, source domain.Schedule, apply func(domain.ScheduleDetail) error) {
	if a.preview || w == nil || !c.WindowActive() || !mygo.App.IsActive() || read.request.busy || !a.scheduleConnected(source) {
		return
	}
	epoch := a.model.ConnectionEpoch
	if remaining := read.next.Sub(c.Now()); read.epoch == epoch && remaining > 0 {
		c.After(remaining)
		return
	}
	read.fresh = read.fresh && read.epoch == epoch
	read.epoch, read.next = epoch, c.Now().Add(mobileScheduleDetailRefresh)
	c.After(mobileScheduleDetailRefresh)
	accept := decodePage(func(in domain.ScheduleDetail) error {
		if !sameSchedule(source, in.Schedule) || slices.ContainsFunc(in.Runs, func(run domain.ScheduleRun) bool {
			return run.ID == "" || run.ScheduleID != source.ID
		}) {
			return errors.New("定时任务或运行记录的归属已改变")
		}
		return apply(in)
	})
	a.runPageJobResult(nil, w, &read.request, "schedule.get", protocol.ScheduleCommand{ID: source.ID}, nil,
		func(raw json.RawMessage, err error) error {
			if a.model.ConnectionEpoch != epoch || !a.scheduleConnected(source) {
				return nil
			}
			if err == nil {
				err = accept(raw)
			}
			read.fresh = err == nil
			return err
		})
}
