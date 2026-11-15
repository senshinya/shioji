package worker

import (
	"errors"

	"shioji/internal/domain"
	"shioji/internal/store"
)

// A large display window belongs to its authenticated read connection. A
// disconnect, another command, or the final chunk releases the WAL snapshot.
type connectionHistory struct {
	snapshot     *store.HistorySnapshot
	query        domain.HistoryQuery
	work, method string
	after        int64
}

func (h *connectionHistory) close() {
	if h.snapshot != nil {
		h.snapshot.Close()
		h.snapshot = nil
	}
}

func (h *connectionHistory) handle(s *Server, method string, body []byte) (any, error) {
	if method != "session.history" && method != "session.work.history" {
		h.close()
		return s.handle(method, body)
	}
	q, err := decode[domain.HistoryQuery](body)
	work := ""
	if method == "session.work.history" {
		var w domain.WorkHistoryQuery
		w, err = decode[domain.WorkHistoryQuery](body)
		q, work = w.HistoryQuery, w.ID
	}
	if err != nil {
		h.close()
		return nil, err
	}
	if q.Rounds == 0 || q.SummaryOnly {
		h.close()
		return s.handle(method, body)
	}
	if q.WindowFirst == 0 && q.WindowLast == 0 {
		h.close()
		h.snapshot, err = s.store.OpenHistorySnapshot(s.ctx)
		if err != nil {
			return nil, err
		}
		h.query = q
		h.work, h.method = work, method
	} else if h.snapshot == nil || work != h.work || method != h.method || q.SessionID != h.query.SessionID || q.ThreadID != h.query.ThreadID || q.Generation != h.query.Generation || q.Rounds != h.query.Rounds || q.WindowFirst != h.query.WindowFirst || q.WindowLast != h.query.WindowLast || q.After != h.after {
		h.close()
		return nil, errors.New("历史分页已失效，请重新加载")
	}
	out, err := s.handleWithHistory(method, body, h.snapshot)
	if err != nil {
		h.close()
		return nil, err
	}
	var window *domain.HistoryWindow
	switch d := out.(type) {
	case domain.SessionDetail:
		h.query.ThreadID, window = d.Session.ThreadID, d.History
	case domain.WorkDetail:
		h.query.ThreadID, window = d.ThreadID, d.History
	}
	h.query.WindowFirst, h.query.WindowLast = window.WindowFirst, window.WindowLast
	h.after = window.Last
	if h.after >= h.query.WindowLast || h.after == 0 {
		h.close()
	}
	return out, nil
}
