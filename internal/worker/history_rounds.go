package worker

import (
	"errors"

	"shioji/internal/domain"
	"shioji/internal/store"
)

// A large display window belongs to its authenticated read connection. A
// disconnect, another command, or the final chunk releases the WAL snapshot.
type connectionHistory struct {
	snapshot *store.HistorySnapshot
	query    domain.HistoryQuery
	after    int64
}

func (h *connectionHistory) close() {
	if h.snapshot != nil {
		h.snapshot.Close()
		h.snapshot = nil
	}
}

func (h *connectionHistory) handle(s *Server, method string, body []byte) (any, error) {
	if method != "session.history" {
		h.close()
		return s.handle(method, body)
	}
	q, err := decode[domain.HistoryQuery](body)
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
	} else if h.snapshot == nil || q.SessionID != h.query.SessionID || q.ThreadID != h.query.ThreadID || q.Generation != h.query.Generation || q.Rounds != h.query.Rounds || q.WindowFirst != h.query.WindowFirst || q.WindowLast != h.query.WindowLast || q.After != h.after {
		h.close()
		return nil, errors.New("历史分页已失效，请重新加载")
	}
	out, err := s.handleWithHistory(method, body, h.snapshot)
	if err != nil {
		h.close()
		return nil, err
	}
	d := out.(domain.SessionDetail)
	h.query.ThreadID = d.Session.ThreadID
	h.query.WindowFirst, h.query.WindowLast = d.History.WindowFirst, d.History.WindowLast
	h.after = d.History.Last
	if h.after >= h.query.WindowLast || h.after == 0 {
		h.close()
	}
	return d, nil
}
