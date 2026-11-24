package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"shioji/internal/domain"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Store) SessionHistory(q domain.HistoryQuery) (domain.SessionDetail, error) {
	reader, close, err := s.historySnapshot()
	if err != nil {
		return domain.SessionDetail{}, err
	}
	defer close()
	return reader.sessionHistorySnapshot(q)
}

func (s *Store) sessionHistorySnapshot(q domain.HistoryQuery) (domain.SessionDetail, error) {
	var err error
	if q.Generation != "" && q.Generation != s.generation {
		return domain.SessionDetail{}, errors.New("历史快照已改变，请重新连接")
	}
	if q.SessionID == "" || q.Before < 0 || q.After < 0 || q.Before > 0 && q.After > 0 || q.AroundID != "" && (q.Before > 0 || q.After > 0) {
		return domain.SessionDetail{}, errors.New("invalid history range")
	}
	if q.Rounds < 0 || q.Rounds > domain.MaxHistoryRounds || q.Rounds == 0 && (q.WindowFirst != 0 || q.WindowLast != 0) {
		return domain.SessionDetail{}, errors.New("invalid history round range")
	}
	if q.SummaryOnly {
		out := domain.SessionDetail{}
		out.Session, err = load[domain.Session](s.historyQueries().QueryRow("SELECT body FROM sessions WHERE id=?", q.SessionID))
		if err != nil {
			return out, err
		}
		if q.ThreadID != "" && q.ThreadID != out.Session.ThreadID {
			return out, errors.New("会话历史已改变")
		}
		item, e := load[domain.Item](s.historyQueries().QueryRow("SELECT body FROM items WHERE session_id=? AND id=?", q.SessionID, q.AroundID))
		if e != nil {
			return out, e
		}
		if item.Kind != "tool" {
			return out, errors.New("invalid tool group")
		}
		group, e := s.historyToolGroupLocked(q.SessionID, item)
		if e != nil {
			return out, e
		}
		out.History = &domain.HistoryWindow{Groups: []domain.HistoryToolGroup{group}}
		err = s.historyQueries().QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&out.Sequence)
		return out, err
	}
	return s.sessionLocked(q.SessionID, 0, q.Limit, &q)
}

func (s *Store) HistoryMatches(q domain.HistorySearchQuery) (domain.HistoryMatches, error) {
	out := domain.HistoryMatches{SessionID: q.SessionID, Query: strings.TrimSpace(q.Query)}
	if !utf8.ValidString(q.Query) || len(q.Query) > 256 {
		return out, errors.New("search query exceeds 256 bytes")
	}
	reader, close, err := s.historySnapshot()
	if err != nil {
		return out, err
	}
	defer close()
	s = reader
	if q.Generation != "" && q.Generation != s.generation {
		return out, errors.New("历史快照已改变，请重新连接")
	}
	session, err := load[domain.Session](s.historyQueries().QueryRow("SELECT body FROM sessions WHERE id=?", q.SessionID))
	if err != nil {
		return out, err
	}
	if q.ThreadID != "" && q.ThreadID != session.ThreadID {
		return out, errors.New("会话历史已改变，请重新查找")
	}
	out.ThreadID = session.ThreadID
	out.Revision = session.Revision
	if out.Query == "" {
		return out, nil
	}
	rows, err := s.historyQueries().Query("SELECT id,position,COALESCE(json_extract(body,'$.text'),'') FROM items WHERE session_id=? AND position>? AND json_extract(body,'$.kind') IN ('user','assistant','tool','activity') ORDER BY position", q.SessionID, q.After)
	if err != nil {
		return out, err
	}
	needle := strings.ToLower(out.Query)
	metadataBytes := 0
	for rows.Next() {
		var id, text string
		var order int64
		if err = rows.Scan(&id, &order, &text); err != nil {
			break
		}
		count := strings.Count(strings.ToLower(text), needle)
		if count == 0 {
			continue
		}
		if len(out.Matches) == 500 {
			out.More = true
			break
		}
		start, end := domain.FindLiteral(text, out.Query, 0)
		match := domain.HistoryMatch{ID: id, Order: order, Count: count, Start: start, End: end}
		size := len(blob(match)) + 1
		if metadataBytes+size > (768<<10)-4096 {
			if len(out.Matches) == 0 {
				return out, errors.New("history match exceeds page budget")
			}
			out.More = true
			break
		}
		metadataBytes += size
		out.Matches = append(out.Matches, match)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	err = s.historyQueries().QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&out.Sequence)
	return out, err
}

func (s *Store) historyItemsQueryLocked(q domain.HistoryQuery, limit int) (string, []any, bool, error) {
	base := "SELECT body FROM items WHERE session_id=? AND json_extract(body,'$.kind')!='queued'"
	if q.AroundID != "" {
		var order int64
		if err := s.historyQueries().QueryRow("SELECT position FROM items WHERE session_id=? AND id=?", q.SessionID, q.AroundID).Scan(&order); err != nil {
			return "", nil, false, err
		}
		return base + " AND position>=? ORDER BY position LIMIT ?", []any{q.SessionID, order, limit + 1}, false, nil
	}
	if q.After > 0 {
		return base + " AND position>? ORDER BY position LIMIT ?", []any{q.SessionID, q.After, limit + 1}, false, nil
	}
	if q.Before > 0 {
		return base + " AND position<? ORDER BY position DESC LIMIT ?", []any{q.SessionID, q.Before, limit + 1}, true, nil
	}
	return base + " ORDER BY position DESC LIMIT ?", []any{q.SessionID, limit + 1}, true, nil
}

func (s *Store) historyWindowLocked(id string, items []domain.Item, contextID string) (*domain.HistoryWindow, error) {
	h := &domain.HistoryWindow{ContextID: contextID}
	var members []domain.Item
	for _, item := range items {
		if item.ID == contextID {
			continue
		}
		if h.First == 0 {
			h.First = item.Order
		}
		h.Last = item.Order
		if domain.ToolGroupMember(item) {
			members = append(members, item)
		}
	}
	if h.First == 0 {
		return h, nil
	}
	if err := s.historyQueries().QueryRow("SELECT EXISTS(SELECT 1 FROM items WHERE session_id=? AND position<? AND json_extract(body,'$.kind')!='queued'), EXISTS(SELECT 1 FROM items WHERE session_id=? AND position>? AND json_extract(body,'$.kind')!='queued')", id, h.First, id, h.Last).Scan(&h.Older, &h.Newer); err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return h, nil
	}
	// Only the two edge groups can extend beyond this contiguous window.
	for _, item := range []domain.Item{members[0], members[len(members)-1]} {
		if len(h.Groups) > 0 {
			g := h.Groups[0]
			if item.TurnID == g.TurnID && item.Order >= g.First && item.Order <= g.Last {
				continue
			}
		}
		g, err := s.historyToolGroupLocked(id, item)
		if err != nil {
			return nil, err
		}
		if g.Count > 0 && (len(h.Groups) == 0 || h.Groups[0].ID != g.ID) {
			h.Groups = append(h.Groups, g)
		}
	}
	return h, nil
}

const toolHistoryMemberSQL = `(json_extract(body,'$.kind')='tool' OR (json_extract(body,'$.kind') IN ('activity','reasoning') AND json_extract(body,'$.presentation')='reasoning'))`

// Tool and summary bodies stay in SQLite; only potential boundaries need decoding.
func (s *Store) historyToolBoundaryLocked(id string, item domain.Item, before bool) (int64, error) {
	comparison, direction, boundary := ">", "ASC", int64(9223372036854775807)
	if before {
		comparison, direction, boundary = "<", "DESC", 0
	}
	rows, err := s.historyQueries().Query("SELECT body FROM items WHERE session_id=? AND position"+comparison+"? AND json_extract(body,'$.kind')!='queued' AND (NOT ("+toolHistoryMemberSQL+") OR COALESCE(json_extract(body,'$.turnId'),'')!=?) ORDER BY position "+direction, id, item.Order, item.TurnID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			return 0, err
		}
		var candidate domain.Item
		if err = json.Unmarshal(body, &candidate); err != nil {
			return 0, err
		}
		if candidate.TurnID != item.TurnID || !domain.ToolGroupMember(candidate) && !domain.HistorySpacer(candidate) {
			return candidate.Order, nil
		}
	}
	return boundary, rows.Err()
}

func (s *Store) historyToolGroupLocked(id string, item domain.Item) (domain.HistoryToolGroup, error) {
	before, err := s.historyToolBoundaryLocked(id, item, true)
	if err != nil {
		return domain.HistoryToolGroup{}, err
	}
	after, err := s.historyToolBoundaryLocked(id, item, false)
	if err != nil {
		return domain.HistoryToolGroup{}, err
	}
	g := domain.HistoryToolGroup{TurnID: item.TurnID}
	var firstTool, last sql.NullInt64
	err = s.historyQueries().QueryRow(`SELECT
		MIN(CASE WHEN json_extract(body,'$.kind')='tool' THEN position END),
		MAX(position),
		COALESCE(SUM(CASE WHEN json_extract(body,'$.kind')='tool' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN json_extract(body,'$.kind')='tool' AND json_extract(body,'$.final') AND (COALESCE(json_extract(body,'$.exitCode'),0)!=0 OR json_extract(body,'$.status') IN ('failed','declined','interrupted')) THEN 1 ELSE 0 END),0)
		FROM items WHERE session_id=? AND position>? AND position<? AND `+toolHistoryMemberSQL, id, before, after).Scan(&firstTool, &last, &g.Count, &g.Issues)
	if err != nil || !firstTool.Valid || item.Order < firstTool.Int64 {
		return domain.HistoryToolGroup{}, err
	}
	g.First, g.Last = firstTool.Int64, last.Int64
	err = s.historyQueries().QueryRow("SELECT id FROM items WHERE session_id=? AND position=?", id, g.First).Scan(&g.ID)
	return g, err
}

func (s *Store) HistoryOutline(q domain.HistoryQuery) (domain.HistoryOutline, error) {
	reader, close, err := s.historySnapshot()
	out := domain.HistoryOutline{SessionID: q.SessionID}
	if err != nil {
		return out, err
	}
	defer close()
	s = reader
	if q.Generation != "" && q.Generation != s.generation {
		return out, errors.New("历史快照已改变，请重新连接")
	}
	session, err := load[domain.Session](s.historyQueries().QueryRow("SELECT body FROM sessions WHERE id=?", q.SessionID))
	if err != nil {
		return out, err
	}
	if q.ThreadID != "" && q.ThreadID != session.ThreadID {
		return out, errors.New("会话历史已改变，请重新加载")
	}
	out.ThreadID = session.ThreadID
	limit := q.Limit
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	before := q.Before
	if before <= 0 {
		before = 9223372036854775807
	}
	rows, err := s.historyQueries().Query("SELECT id,position,COALESCE(json_extract(body,'$.turnId'),''),substr(COALESCE(json_extract(body,'$.text'),''),1,120) FROM items WHERE session_id=? AND position<? AND json_extract(body,'$.kind')='user' ORDER BY position DESC LIMIT ?", q.SessionID, before, limit+1)
	if err != nil {
		return out, err
	}
	metadataBytes := 0
	for rows.Next() {
		var p domain.HistoryPrompt
		if err = rows.Scan(&p.ID, &p.Order, &p.TurnID, &p.Text); err != nil {
			rows.Close()
			return out, err
		}
		size := len(blob(p)) + 1
		if metadataBytes+size > (768<<10)-4096 {
			if len(out.Prompts) == 0 {
				rows.Close()
				return out, errors.New("history prompt exceeds page budget")
			}
			out.Older = true
			break
		}
		metadataBytes += size
		out.Prompts = append(out.Prompts, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Prompts) > limit {
		out.Older = true
		out.Prompts = out.Prompts[:limit]
	}
	if err = s.historyQueries().QueryRow("SELECT COALESCE(MAX(sequence),0) FROM journal").Scan(&out.Sequence); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	return out, nil
}

// All display history reads use a cancellable WAL snapshot on a separate
// connection, leaving acceptance, event persistence and Stop on the writer.
type historyQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}
type historyTransaction struct {
	context context.Context
	tx      *sql.Tx
}

func (r historyTransaction) Query(q string, args ...any) (*sql.Rows, error) {
	return r.tx.QueryContext(r.context, q, args...)
}
func (r historyTransaction) QueryRow(q string, args ...any) *sql.Row {
	return r.tx.QueryRowContext(r.context, q, args...)
}
func (s *Store) historyQueries() historyQuerier {
	if s.historyReader != nil {
		return s.historyReader
	}
	return s.db
}
func (s *Store) historySnapshot() (*Store, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	reader, close, err := s.openHistorySnapshot(ctx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return reader, func() { close(); cancel() }, nil
}

func (s *Store) openHistorySnapshot(ctx context.Context) (*Store, func(), error) {
	uri := url.URL{Scheme: "file", Path: s.path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, nil, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return &Store{deviceID: s.deviceID, generation: s.generation, historyReader: historyTransaction{ctx, tx}}, func() { tx.Rollback(); db.Close() }, nil
}
