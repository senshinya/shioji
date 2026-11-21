package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"shioji/internal/domain"
)

const historyVisibleSQL = "session_id=? AND json_extract(body,'$.kind')!='queued'"
const historyMainItemSQL = "COALESCE(json_extract(body,'$.workId'),'')=''"
const historyPromptSQL = historyMainItemSQL + " AND json_extract(body,'$.kind')='user'"
const historyRoundStartSQL = historyMainItemSQL + ` AND (json_extract(body,'$.kind')='user' OR
 (json_extract(body,'$.kind')='activity' AND json_extract(body,'$.presentation')='` + domain.GoalContinuationPresentation + `'))`

// HistorySnapshot keeps transport-sized chunks on one read-only WAL snapshot.
// It never takes the execution store's writer lock.
type HistorySnapshot struct {
	reader *Store
	close  func()
}

func (s *Store) OpenHistorySnapshot(parent context.Context) (*HistorySnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	reader, close, err := s.openHistorySnapshot(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	return &HistorySnapshot{reader: reader, close: func() { close(); cancel() }}, nil
}

func (h *HistorySnapshot) Read(q domain.HistoryQuery) (domain.SessionDetail, error) {
	return h.reader.sessionHistorySnapshot(q)
}

func (h *HistorySnapshot) Close() {
	if h.close != nil {
		h.close()
		h.close = nil
	}
}

// A display round starts at a main-conversation user message (including a
// steering input) or a persisted Goal continuation marker.
func (s *Store) historyRoundRangeLocked(q domain.HistoryQuery) (first, last int64, err error) {
	if q.Rounds < 1 || q.Rounds > domain.MaxHistoryRounds {
		return 0, 0, errors.New("invalid history round count")
	}
	if q.WindowFirst != 0 || q.WindowLast != 0 {
		if q.WindowFirst < 1 || q.WindowLast < q.WindowFirst || q.After < q.WindowFirst-1 || q.After > q.WindowLast || q.Before != 0 || q.AroundID != "" {
			return 0, 0, errors.New("invalid history continuation range")
		}
		return q.WindowFirst, q.WindowLast, nil
	}
	// Separate the endpoints so SQLite can seek each one with items_order.
	// A combined MIN/MAX scans the session and parses every item body.
	lastQuery := "SELECT COALESCE(MAX(position),0) FROM items WHERE " + historyVisibleSQL
	lastArgs := []any{q.SessionID}
	if q.Before > 0 {
		lastQuery += " AND position<?"
		lastArgs = append(lastArgs, q.Before)
	}
	err = s.historyQueries().QueryRow(lastQuery, lastArgs...).Scan(&last)
	if err != nil || last == 0 {
		return
	}
	err = s.historyQueries().QueryRow("SELECT COALESCE(MIN(position),0) FROM items WHERE "+historyVisibleSQL, q.SessionID).Scan(&first)
	if err != nil {
		return
	}
	if q.After > 0 || q.AroundID != "" {
		var target int64
		if q.AroundID != "" {
			err = s.historyQueries().QueryRow("SELECT position FROM items WHERE "+historyVisibleSQL+" AND id=?", q.SessionID, q.AroundID).Scan(&target)
			if err != nil {
				return
			}
			var start int64
			err = s.historyQueries().QueryRow("SELECT COALESCE(MAX(position),0) FROM items WHERE session_id=? AND "+historyRoundStartSQL+" AND position<=?", q.SessionID, target).Scan(&start)
			if err != nil {
				return
			}
			if start != 0 {
				first = start
			}
		} else {
			err = s.historyQueries().QueryRow("SELECT COALESCE(MIN(position),0) FROM items WHERE "+historyVisibleSQL+" AND position>?", q.SessionID, q.After).Scan(&first)
			if err != nil || first == 0 {
				return 0, 0, err
			}
		}
		var next int64
		err = s.historyQueries().QueryRow("SELECT position FROM items WHERE session_id=? AND "+historyRoundStartSQL+" AND position>? ORDER BY position LIMIT 1 OFFSET ?", q.SessionID, first, q.Rounds-1).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return first, last, nil
		}
		if err != nil {
			return
		}
		err = s.historyQueries().QueryRow("SELECT COALESCE(MAX(position),0) FROM items WHERE "+historyVisibleSQL+" AND position<?", q.SessionID, next).Scan(&last)
		return
	}
	rows, err := s.historyQueries().Query("SELECT position FROM items WHERE session_id=? AND "+historyRoundStartSQL+" AND position<=? ORDER BY position DESC LIMIT ?", q.SessionID, last, q.Rounds+1)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	var starts []int64
	for rows.Next() {
		var position int64
		if err = rows.Scan(&position); err != nil {
			return 0, 0, err
		}
		starts = append(starts, position)
	}
	if len(starts) > q.Rounds {
		first = starts[q.Rounds-1]
	}
	return first, last, rows.Err()
}

// Read only one transport-sized piece. A round can contain any number of
// records; the client continues to the selected window's stable end position.
func (s *Store) historyRoundChunkLocked(query string, args []any) ([]domain.Item, error) {
	rows, err := s.historyQueries().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.Item
	bytes := 0
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if len(items) > 0 && bytes+len(body) > 512<<10 {
			break
		}
		var item domain.Item
		if err = json.Unmarshal(body, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
		bytes += len(body)
	}
	return items, rows.Err()
}
