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
const historyRoundMarkerSQL = historyMainItemSQL + ` AND (json_extract(body,'$.kind')='user' OR
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

// A Goal turn can record compaction or context before the adapter confirms
// that it is a continuation. Include its prefix without crossing the previous
// user message or continuation marker. The underlying journal stays ordered.
func (s *Store) historyRoundStartPosition(sid string, marker int64) (int64, error) {
	var start int64
	err := s.historyQueries().QueryRow(`
		SELECT CASE WHEN json_extract(mark.body,'$.kind')='activity'
			AND json_extract(mark.body,'$.presentation')='`+domain.GoalContinuationPresentation+`'
			AND COALESCE(json_extract(mark.body,'$.turnId'),'')!=''
		THEN COALESCE((
			SELECT MIN(prefix.position) FROM items AS prefix
			WHERE prefix.session_id=mark.session_id AND prefix.position<mark.position
				AND prefix.position>COALESCE((
					SELECT MAX(prior.position) FROM items AS prior
					WHERE prior.session_id=mark.session_id AND prior.position<mark.position
						AND `+historyRoundMarkerSQL+`
				),0)
				AND json_extract(prefix.body,'$.kind')!='queued' AND `+historyMainItemSQL+`
				AND json_extract(prefix.body,'$.turnId')=json_extract(mark.body,'$.turnId')
		),mark.position)
		ELSE mark.position END
		FROM items AS mark WHERE mark.session_id=? AND mark.position=?`, sid, marker).Scan(&start)
	return start, err
}

func (s *Store) historyRoundMarkerAfter(sid string, position int64, skip int) (int64, error) {
	var marker int64
	err := s.historyQueries().QueryRow("SELECT position FROM items WHERE session_id=? AND "+
		historyRoundMarkerSQL+" AND position>? ORDER BY position LIMIT 1 OFFSET ?", sid, position, skip).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return marker, err
}

func (s *Store) historyRoundAt(sid string, position int64) (marker, start int64, err error) {
	// Only the next marker can move across this position. Every continuation
	// prefix is bounded by the raw position of the preceding marker.
	next, err := s.historyRoundMarkerAfter(sid, position, 0)
	if err != nil {
		return 0, 0, err
	}
	if next != 0 {
		nextStart, err := s.historyRoundStartPosition(sid, next)
		if err != nil {
			return 0, 0, err
		}
		if nextStart <= position {
			return next, nextStart, nil
		}
	}
	err = s.historyQueries().QueryRow("SELECT COALESCE(MAX(position),0) FROM items WHERE session_id=? AND "+
		historyRoundMarkerSQL+" AND position<=?", sid, position).Scan(&marker)
	if err == nil && marker != 0 {
		start, err = s.historyRoundStartPosition(sid, marker)
	}
	return
}

// A display round starts at a main-conversation user message (including a
// steering input) or the beginning of a marked Goal continuation.
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
	// Separate seeks use items_order to find both ends without parsing every body.
	lastQuery := "SELECT COALESCE(MAX(position),0) FROM items WHERE " + historyVisibleSQL
	lastArgs := []any{q.SessionID}
	if q.Before > 0 && q.After == 0 && q.AroundID == "" {
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
		} else {
			err = s.historyQueries().QueryRow("SELECT COALESCE(MIN(position),0) FROM items WHERE "+historyVisibleSQL+" AND position>?", q.SessionID, q.After).Scan(&first)
			if err != nil || first == 0 {
				return 0, 0, err
			}
			target = first
		}
		marker, start, err := s.historyRoundAt(q.SessionID, target)
		if err != nil {
			return 0, 0, err
		}
		if q.AroundID != "" && start != 0 {
			first = start
		}
		next, err := s.historyRoundMarkerAfter(q.SessionID, max(first, marker), q.Rounds-1)
		if err != nil || next == 0 {
			return first, last, err
		}
		end, err := s.historyRoundStartPosition(q.SessionID, next)
		if err != nil {
			return 0, 0, err
		}
		err = s.historyQueries().QueryRow("SELECT COALESCE(MAX(position),0) FROM items WHERE "+historyVisibleSQL+" AND position<?", q.SessionID, end).Scan(&last)
		return first, last, err
	}
	rows, err := s.historyQueries().Query("SELECT position FROM items WHERE session_id=? AND "+historyRoundMarkerSQL+" AND position<=? ORDER BY position DESC LIMIT ?", q.SessionID, last, q.Rounds+1)
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
	if err = rows.Err(); err != nil {
		return
	}
	if len(starts) > q.Rounds {
		first, err = s.historyRoundStartPosition(q.SessionID, starts[q.Rounds-1])
	}
	return
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
