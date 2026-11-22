package main

import (
	"encoding/json"
	"slices"

	"shioji/internal/app"
	"shioji/internal/domain"
)

// Only the transcript presentation stops on tap. The Worker still decides
// when the turn ends and whether the next message can run.
type mobileSessionStop struct {
	generation, thread string
	turn               string
	revision           int64
	items              map[string]domain.Item
	err                string
}

func (s *mobileSessionStop) pending() bool { return s != nil && s.err == "" }

func (a *mobileApp) sessionStop(session domain.Session) *mobileSessionStop {
	key := a.model.WorkKey(session.ID, "")
	stop := a.sessionStops[key]
	if stop != nil && !stop.matches(a.model.ConversationGeneration(), session, a.model.Details[session.ID].Turns) {
		delete(a.sessionStops, key)
		return nil
	}
	return stop
}

func (a *mobileApp) syncSessionStops() {
	for key, stop := range a.sessionStops {
		if key.DeviceID != a.model.Snapshot.DeviceID ||
			!stop.matches(a.model.ConversationGeneration(), a.sessionByID(key.SessionID), a.model.Details[key.SessionID].Turns) {
			delete(a.sessionStops, key)
		}
	}
}

func (s *mobileSessionStop) matches(generation string, session domain.Session, turns []domain.Turn) bool {
	if generation != s.generation || session.ID == "" || session.Archived || s.thread != "" && s.thread != session.ThreadID {
		return false
	}
	if s.turn == "" {
		return session.Status == domain.ExecutionStarting
	}
	awaiting := session.Status.Active()
	for _, turn := range turns {
		if turn.Status.Active() {
			if turn.ID != s.turn {
				return false
			}
			awaiting = true
		}
	}
	return awaiting
}

func (s *mobileSessionStop) transcript(detail domain.SessionDetail) domain.SessionDetail {
	if !s.pending() || s.turn == "" {
		return detail
	}
	items := make([]domain.Item, 0, len(detail.Items))
	for _, item := range detail.Items {
		if item.TurnID != s.turn {
			items = append(items, item)
		} else if frozen, ok := s.items[item.ID]; ok {
			items = append(items, frozen)
		}
	}
	detail.Items = items
	for i, turn := range detail.Turns {
		if turn.ID == s.turn {
			detail.Turns = slices.Clone(detail.Turns)
			detail.Turns[i].Status = domain.ExecutionInterrupted
			break
		}
	}
	return detail
}

func (a *mobileApp) stopSession(id string) {
	m, controller := a.model, a.controller
	session := a.sessionByID(id)
	if controller == nil || !m.Connected || m.ConnectionEpoch == "" || !session.Status.Active() ||
		!m.SessionReadiness(session).Capabilities.Interrupt || a.sessionStop(session).pending() {
		return
	}
	detail := m.Details[id]
	turn := m.ActiveTurnForSession(id)
	input := map[string]string{"sessionId": id, "turnId": turn}
	if turn == "" {
		if session.Status != domain.ExecutionStarting {
			return
		}
		for _, op := range m.Snapshot.Operations {
			if op.SessionID == id && op.Kind == "submit" &&
				(op.State == domain.OperationAccepted || op.State == domain.OperationDispatched) {
				input["operationId"] = op.ID
				break
			}
		}
	}
	stop := &mobileSessionStop{generation: m.ConversationGeneration(), thread: session.ThreadID, turn: turn, revision: session.Revision}
	if turn != "" {
		stop.items = make(map[string]domain.Item)
		for _, item := range detail.Items {
			if item.TurnID == turn {
				stop.items[item.ID] = item
			}
		}
	}
	key := m.WorkKey(id, "")
	if a.sessionStops == nil {
		a.sessionStops = make(map[app.WorkKey]*mobileSessionStop)
	}
	a.sessionStops[key] = stop
	if !controller.Do(app.Job{Method: "turn.interrupt", Input: input, ConnectionEpoch: m.ConnectionEpoch,
		Apply: func(_ *app.Model, _ json.RawMessage, err error) {
			if err == nil || a.controller != controller || a.model != m || a.sessionStop(a.sessionByID(id)) != stop {
				return
			}
			latest := m.Details[id]
			ended := latest.Session.Revision > stop.revision && latest.Session.Status.CanSubmit() ||
				turn != "" && slices.ContainsFunc(latest.Turns, func(t domain.Turn) bool { return t.ID == turn && t.Status.CanSubmit() })
			if !ended {
				stop.err = err.Error()
			}
		},
	}) {
		stop.err = "未能发送停止请求，请重试"
	}
	a.refresh()
}
