package store

import (
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
	rows, err := s.db.Query(`SELECT id, json_extract(body,'$.workspaceId'), json_extract(body,'$.title'), COALESCE((SELECT substr(json_extract(i.body,'$.text'),1,240) FROM items i WHERE i.session_id=sessions.id AND instr(lower(json_extract(i.body,'$.text')),lower(?))>0 ORDER BY position LIMIT 1),'') FROM sessions WHERE ?='' OR instr(lower(json_extract(body,'$.title')),lower(?))>0 OR EXISTS(SELECT 1 FROM items i WHERE i.session_id=sessions.id AND instr(lower(json_extract(i.body,'$.text')),lower(?))>0) ORDER BY rowid DESC LIMIT 50`, query, query, query, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.SearchResult{}
	for rows.Next() {
		var r domain.SearchResult
		if err = rows.Scan(&r.SessionID, &r.WorkspaceID, &r.Title, &r.Snippet); err != nil {
			return nil, err
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
