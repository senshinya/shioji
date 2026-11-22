package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"shioji/internal/domain"
)

// WorkHistory reads accepted observations, independently of the execution writer.
func (s *Store) WorkHistory(q domain.WorkHistoryQuery) (domain.WorkDetail, error) {
	r, close, err := s.historySnapshot()
	if err != nil {
		return domain.WorkDetail{}, err
	}
	defer close()
	return r.workHistory(q)
}

func (h *HistorySnapshot) Work(q domain.WorkHistoryQuery) (domain.WorkDetail, error) {
	return h.reader.workHistory(q)
}

func (s *Store) workHistory(q domain.WorkHistoryQuery) (domain.WorkDetail, error) {
	out := domain.WorkDetail{SessionID: q.SessionID}
	if q.SessionID == "" || q.ID == "" || len(q.SessionID) > 256 || len(q.ID) > 1024 || q.Rounds < 1 || q.Rounds > domain.MaxHistoryRounds || q.Before < 0 || q.After < 0 || q.Before > 0 && q.After > 0 || q.AroundID != "" || q.SummaryOnly || q.Generation != "" && q.Generation != s.generation {
		return out, errors.New("invalid child history query")
	}
	db := s.historyQueries()
	session, err := load[domain.Session](db.QueryRow("SELECT body FROM sessions WHERE id=?", q.SessionID))
	if err != nil {
		return out, err
	}
	if q.ThreadID != "" && q.ThreadID != session.ThreadID {
		return out, errors.New("会话历史已改变")
	}
	out.ThreadID = session.ThreadID
	out.Work, err = load[domain.WorkActivity](db.QueryRow("SELECT body FROM works WHERE session_id=? AND id=?", q.SessionID, q.ID))
	if err != nil {
		return out, err
	}
	if out.Work.Kind != "agent" {
		return out, errors.New("only child agents have conversation history")
	}
	if err = db.QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&out.Sequence); err != nil {
		return out, err
	}
	pending, err := load[domain.WorkInputOperation](db.QueryRow("SELECT body FROM work_inputs WHERE session_id=? AND work_id=? AND json_extract(body,'$.state') IN ('accepted','dispatched','indeterminate') ORDER BY rowid DESC LIMIT 1", q.SessionID, q.ID))
	if err == nil {
		out.PendingInput = &pending
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	const scope = "session_id=? AND work_id=?"
	var first, last int64
	if q.WindowFirst != 0 || q.WindowLast != 0 {
		if q.WindowFirst < 1 || q.WindowLast < q.WindowFirst || q.After < q.WindowFirst-1 || q.After >= q.WindowLast || q.Before != 0 {
			return out, errors.New("invalid child history continuation")
		}
		first, last = q.WindowFirst, q.WindowLast
	} else {
		if q.After > 0 {
			return out, errors.New("child history requires a window for continuation")
		}
		if err = db.QueryRow("SELECT COALESCE(MIN(position),0),COALESCE(MAX(position),0) FROM work_items WHERE "+scope, q.SessionID, q.ID).Scan(&first, &last); err != nil {
			return out, err
		}
		if q.Before > 0 {
			if err = db.QueryRow("SELECT COALESCE(MAX(position),0) FROM work_items WHERE "+scope+" AND position<?", q.SessionID, q.ID, q.Before).Scan(&last); err != nil {
				return out, err
			}
		}
		// Each child user message starts a round, including directed steer.
		var start int64
		err = db.QueryRow("SELECT position FROM work_items WHERE "+scope+" AND json_extract(body,'$.kind')='user' AND position<=? ORDER BY position DESC LIMIT 1 OFFSET ?", q.SessionID, q.ID, last, q.Rounds).Scan(&start)
		if err == nil {
			if err = db.QueryRow("SELECT MIN(position) FROM work_items WHERE "+scope+" AND json_extract(body,'$.kind')='user' AND position>?", q.SessionID, q.ID, start).Scan(&first); err != nil {
				return out, err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	}
	h := &domain.HistoryWindow{WindowFirst: first, WindowLast: last}
	out.History = h
	if last == 0 {
		h.WindowFirst = 0
		return out, nil
	}
	if err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM work_items WHERE "+scope+" AND position<?),EXISTS(SELECT 1 FROM work_items WHERE "+scope+" AND position>?)", q.SessionID, q.ID, first, q.SessionID, q.ID, last).Scan(&h.Older, &h.Newer); err != nil {
		return out, err
	}
	rows, err := db.Query(`SELECT i.body, i.revision, COALESCE(json_extract(r.body,'$.kind'),''), json_extract(r.body,'$.receipt')
		FROM work_items i LEFT JOIN requests r ON r.session_id=i.session_id
		AND i.id LIKE 'request:%' AND r.id=substr(i.id,9)
		AND json_extract(r.body,'$.workId')=i.work_id
		AND json_extract(r.body,'$.kind') IN ('questions','approval','permissions') AND json_extract(r.body,'$.state')='answered'
		WHERE i.session_id=? AND i.work_id=? AND i.position>=? AND i.position<=? AND i.position>?
		ORDER BY i.position`, q.SessionID, q.ID, first, last, q.After)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var raw, receipt []byte
		var receiptKind string
		var item domain.WorkItem
		if err = rows.Scan(&raw, &item.Revision, &receiptKind, &receipt); err != nil {
			return out, err
		}
		itemSize := len(raw)
		if len(receipt) > 0 {
			itemSize += len(receipt) + 32
		}
		if len(out.Items) > 0 && size+itemSize > 384<<10 {
			break
		}
		if err = json.Unmarshal(raw, &item.Item); err != nil {
			return out, err
		}
		if item.Item.Kind == "request" && item.Item.Status == string(domain.RequestAnswered) && len(receipt) > 0 {
			if err = json.Unmarshal(receipt, &item.Item.Parts); err != nil {
				return out, err
			}
			item.Item.Presentation = receiptKind
		}
		size += itemSize
		out.Items = append(out.Items, item)
		if h.First == 0 {
			h.First = item.Item.Order
		}
		h.Last = item.Item.Order
	}
	return out, rows.Err()
}
