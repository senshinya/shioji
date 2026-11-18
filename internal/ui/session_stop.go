package ui

import (
	"encoding/json"
	"slices"

	"shioji/internal/app"
	"shioji/internal/domain"
)

// The presentation ends the turn immediately. The model continues receiving
// execution events and the worker still governs what can run next.
type sessionStop struct {
	connection, epoch, turn string
	sessionRevision         int64
	items                   map[string]domain.Item
}

func (v *View) interrupt() {
	m, root := v.Model, v.splitRoot()
	id := m.SessionID
	if id == "" || !m.Connected || !m.RunReadiness().Capabilities.Interrupt || root.sessionStops[id] != nil {
		return
	}
	turn := m.ActiveTurn()
	if turn == "" {
		starting := false
		for _, session := range m.Snapshot.Sessions {
			if session.ID == id {
				starting = session.Status == domain.ExecutionStarting
				break
			}
		}
		if !starting {
			return
		}
	}
	connection := m.ConnectionID()
	detail := m.Details[id]
	stop := &sessionStop{connection: connection, epoch: root.deviceState(connection).ConnectionEpoch, turn: turn, sessionRevision: detail.Session.Revision}
	if turn != "" {
		stop.items = make(map[string]domain.Item)
		for _, item := range detail.Items {
			if item.TurnID == turn {
				stop.items[item.ID] = item
			}
		}
	}
	if !v.do(app.Job{Method: "turn.interrupt", Input: map[string]string{"sessionId": id, "turnId": turn}, Apply: func(m *app.Model, _ json.RawMessage, err error) {
		if err != nil && root.sessionStops[id] == stop && !stop.observedEnd(m.Details[id]) {
			delete(root.sessionStops, id)
			m.Error = err.Error()
		}
	}}) {
		m.Error = "未能发送停止请求，请重试"
		return
	}
	if root.sessionStops == nil {
		root.sessionStops = make(map[string]*sessionStop)
	}
	root.sessionStops[id] = stop
	root.services.Invalidate()
}

func (stop *sessionStop) observedEnd(detail domain.SessionDetail) bool {
	if detail.Session.Revision > stop.sessionRevision && detail.Session.Status.CanSubmit() {
		return true
	}
	return stop.turn != "" && slices.ContainsFunc(detail.Turns, func(turn domain.Turn) bool {
		return turn.ID == stop.turn && turn.Status.CanSubmit()
	})
}

func (v *View) syncSessionStops() {
	for id, stop := range v.sessionStops {
		state := v.deviceState(stop.connection)
		awaiting := false
		if state.Connected && state.ConnectionEpoch == stop.epoch {
			for _, session := range v.Model.Snapshot.Sessions {
				if session.ID == id {
					awaiting = stop.awaitingSettlement(session, v.Model.Details[id].Turns)
					break
				}
			}
		}
		if !awaiting {
			delete(v.sessionStops, id)
		}
	}
}

func (stop *sessionStop) awaitingSettlement(session domain.Session, turns []domain.Turn) bool {
	if session.Archived {
		return false
	}
	awaiting := session.Status.Active()
	if stop.turn == "" {
		return awaiting
	}
	// Runtime retirement journals the turn and session separately. Keep the
	// local stop across the split, unless another turn has already started.
	for _, turn := range turns {
		if turn.Status.Active() {
			if turn.ID != stop.turn {
				return false
			}
			awaiting = true
		}
	}
	return awaiting
}

func (v *View) stoppingTurn(turn string) bool {
	stop := v.splitRoot().sessionStops[v.Model.SessionID]
	return stop != nil && (stop.turn == turn || stop.turn == "")
}

func (v *View) stoppingReplyDetail(detail domain.SessionDetail) domain.SessionDetail {
	stop := v.splitRoot().sessionStops[v.Model.SessionID]
	if stop == nil || stop.turn == "" {
		return detail
	}
	items := make([]domain.Item, 0, len(detail.Items))
	for _, item := range detail.Items {
		if item.TurnID != stop.turn {
			items = append(items, item)
		} else if frozen, ok := stop.items[item.ID]; ok {
			items = append(items, frozen)
		}
	}
	detail.Items = items
	for i, turn := range detail.Turns {
		if turn.ID == stop.turn && turn.Status.Active() {
			detail.Turns = slices.Clone(detail.Turns)
			detail.Turns[i].Status = domain.ExecutionInterrupted
			break
		}
	}
	return detail
}
