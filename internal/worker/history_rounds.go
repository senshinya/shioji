package worker

import (
	"encoding/json"
	"errors"

	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/store"
)

// A large display window belongs to its authenticated read connection. A
// disconnect, another command, or the final chunk releases the WAL snapshot.
type connectionHistory struct {
	snapshot     *store.HistorySnapshot
	query        domain.HistoryQuery
	work, method string
	after        int64
	catalog      []byte
	offset       int
	catalogRead  *store.HistorySnapshot
	cursor       domain.CatalogCursor
}

func (h *connectionHistory) close() {
	if h.snapshot != nil {
		h.snapshot.Close()
		h.snapshot = nil
	}
	if h.catalogRead != nil {
		h.catalogRead.Close()
		h.catalogRead = nil
	}
}

func (h *connectionHistory) handle(s *Server, method string, body []byte) (any, error) {
	if method == "snapshot" && len(body) != 0 {
		q, err := decode[protocol.SnapshotQuery](body)
		if err == nil && q.Cursor != nil {
			return h.catalogPage(s, *q.Cursor)
		}
		if err == nil && q.Offset != nil {
			h.close()
			return h.snapshotPage(s, *q.Offset)
		}
	}
	h.catalog, h.offset = nil, 0
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

// The serialized snapshot stays on this authenticated stream until its last
// page. Concurrent workspace and session events cannot change its barrier.
func (h *connectionHistory) snapshotPage(s *Server, offset int) (protocol.SnapshotPage, error) {
	var page protocol.SnapshotPage
	if offset != h.offset && offset != 0 {
		h.catalog, h.offset = nil, 0
		return page, errors.New("设备目录分页已失效，请重新读取")
	}
	if offset == 0 {
		h.catalog, h.offset = nil, 0
		snapshot, err := s.store.Snapshot()
		if err != nil {
			return page, err
		}
		h.catalog, err = json.Marshal(snapshot)
		if err != nil {
			return page, err
		}
	}
	end := min(h.offset+protocol.SnapshotChunkBytes, len(h.catalog))
	page.Data = h.catalog[h.offset:end]
	if end < len(h.catalog) {
		h.offset, page.Next = end, end
	} else {
		h.catalog, h.offset = nil, 0
	}
	return page, nil
}

func (h *connectionHistory) catalogPage(s *Server, cursor domain.CatalogCursor) (protocol.SnapshotPage, error) {
	var page protocol.SnapshotPage
	var err error
	if cursor == (domain.CatalogCursor{}) {
		h.close()
		h.catalog, h.offset = nil, 0
		h.catalogRead, err = s.store.OpenHistorySnapshot(s.ctx)
		h.cursor = cursor
		if err != nil {
			return page, err
		}
	}
	if h.catalogRead == nil || h.cursor != cursor {
		h.close()
		return page, errors.New("设备目录分页已失效，请重新读取")
	}
	part, next, err := h.catalogRead.CatalogPage(cursor)
	if err != nil || next == nil {
		h.close()
	} else {
		h.cursor = *next
	}
	return protocol.SnapshotPage{Catalog: &part, Cursor: next}, err
}
