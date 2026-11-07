package store

import (
	"encoding/json"
	"errors"
	"shioji/internal/domain"
	"strings"
	"unicode/utf8"
)

func (s *Store) Search(query string) ([]domain.SearchResult, error) {
	if !utf8.ValidString(query) || len(query) > 256 {
		return nil, errors.New("search query exceeds 256 bytes")
	}
	query = strings.TrimSpace(query)
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, json_extract(body,'$.workspaceId'), json_extract(body,'$.title'), COALESCE((SELECT json_object('id',i.id,'text',json_extract(i.body,'$.text')) FROM items i WHERE i.session_id=sessions.id AND json_extract(i.body,'$.kind')!='queued' AND ?!='' AND instr(lower(json_extract(i.body,'$.text')),lower(?))>0 ORDER BY position LIMIT 1),''), COALESCE(json_extract(body,'$.archived'),0) OR COALESCE((SELECT json_extract(w.body,'$.archived') FROM workspaces w WHERE w.id=json_extract(sessions.body,'$.workspaceId')),0) FROM sessions WHERE ?='' OR instr(lower(json_extract(body,'$.title')),lower(?))>0 OR EXISTS(SELECT 1 FROM items i WHERE i.session_id=sessions.id AND json_extract(i.body,'$.kind')!='queued' AND instr(lower(json_extract(i.body,'$.text')),lower(?))>0) ORDER BY rowid DESC LIMIT 50`, query, query, query, query, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SearchResult{}
	for rows.Next() {
		var r domain.SearchResult
		var source string
		if err = rows.Scan(&r.SessionID, &r.WorkspaceID, &r.Title, &source, &r.Archived); err != nil {
			return nil, err
		}
		if source != "" {
			var item domain.Item
			if err = json.Unmarshal([]byte(source), &item); err != nil {
				return nil, err
			}
			if startByte, endByte := domain.FindLiteral(item.Text, query, 0); startByte >= 0 {
				r.ItemID, r.MatchStart, r.MatchEnd = item.ID, startByte, endByte
				text := []rune(item.Text)
				start := max(0, utf8.RuneCountInString(item.Text[:startByte])-24)
				end := min(len(text), start+158)
				r.Snippet = strings.Join(strings.Fields(string(text[start:end])), " ")
				if start > 0 {
					r.Snippet = "…" + r.Snippet
				}
				if end < len(text) {
					r.Snippet += "…"
				}
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) HasUnsettledOperation(session string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM operations WHERE session_id=? AND state IN ('accepted','dispatched','indeterminate'))`, session).Scan(&found)
	return found, err
}
