package store

import (
	"database/sql"
	"errors"
	"slices"
	"strings"

	"shioji/internal/domain"
)

func (s *Store) ImportedConversation(workspace, harness, source string) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return importedConversation(s.db, workspace, harness, source)
}
func importedConversation(q interface{ QueryRow(string, ...any) *sql.Row }, workspace, harness, source string) (domain.Session, error) {
	return load[domain.Session](q.QueryRow(`SELECT body FROM sessions WHERE json_extract(body,'$.workspaceId')=? AND json_extract(body,'$.harnessId')=? AND (json_extract(body,'$.origin.sourceRef')=? OR json_extract(body,'$.threadId')=?) LIMIT 1`, workspace, harness, source, source))
}
func (s *Store) ImportConversation(w domain.Workspace, harness string, p domain.ConversationPreview, config domain.RunConfig) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero domain.Session
	current, err := load[domain.Workspace](s.db.QueryRow("SELECT body FROM workspaces WHERE id=?", w.ID))
	if err != nil {
		return zero, err
	}
	if current.Archived || current.DeviceID != s.deviceID || current.Revision != w.Revision || !slices.Contains(current.Folders, p.Candidate.Directory) {
		return zero, errors.New("工作区已变化或历史目录不属于此工作区")
	}
	if harness == "" || p.Candidate.HarnessID != harness || p.Candidate.SourceRef == "" || len(p.Candidate.SourceRef) > 512 {
		return zero, errors.New("导入来源无效")
	}
	if old, e := importedConversation(s.db, w.ID, harness, p.Candidate.SourceRef); e == nil {
		return old, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return zero, e
	}
	if len(p.Items) == 0 || len(p.Items) > 200 || len(p.Turns) > 50 || len(blob(p)) > 512<<10 {
		return zero, errors.New("历史为空或超过导入上限")
	}
	title := strings.TrimSpace(p.Candidate.Title)
	if p.Candidate.Model != "" {
		config.Model = p.Candidate.Model
	}
	if title == "" || len(title) > 4096 {
		return zero, errors.New("会话标题无效")
	}
	session := domain.Session{ID: domain.NewID(), WorkspaceID: w.ID, HarnessID: harness, Title: title, SourceTitle: title, TitleOverride: true, TitleRevision: 1, Revision: 1, Status: "completed", Config: config, Context: domain.LaunchContext{DeviceID: s.deviceID, CWD: p.Candidate.Directory}, Origin: &domain.ConversationOrigin{SourceRef: p.Candidate.SourceRef, ResumeRef: p.ResumeRef, Reason: p.Reason, Truncated: p.Truncated}}
	tx, err := s.db.Begin()
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO sessions VALUES(?,?)", session.ID, blob(session)); err != nil {
		return zero, err
	}
	turns := map[string]bool{}
	for index, turn := range p.Turns {
		if turn.ID == "" || turns[turn.ID] {
			return zero, errors.New("历史轮次标识重复或缺失")
		}
		turns[turn.ID] = true
		turn.Order = int64(index + 1)
		if turn.Status != "completed" && turn.Status != "failed" && turn.Status != "interrupted" {
			turn.Status = "interrupted"
		}
		if _, err = tx.Exec("INSERT INTO turns VALUES(?,?,?)", session.ID, turn.ID, blob(turn)); err != nil {
			return zero, err
		}
	}
	items := map[string]bool{}
	for index, item := range p.Items {
		if item.ID == "" || items[item.ID] || item.TurnID != "" && !turns[item.TurnID] {
			return zero, errors.New("历史消息归属或标识无效")
		}
		items[item.ID] = true
		item.Final = true
		item.Order = int64(index + 1)
		item.WorkID = ""
		item.Agents = nil
		if item.Kind == "request" {
			item.Kind = "tool"
			item.Status = "interrupted"
		}
		if item.Status == "running" || item.Status == "pending" || item.Status == "unknown" {
			item.Status = "interrupted"
		}
		if _, err = tx.Exec("INSERT INTO items VALUES(?,?,?,?)", session.ID, item.ID, item.Order, blob(item)); err != nil {
			return zero, err
		}
	}
	// A catalog addition is not a live turn completion and must not replay
	// notifications, requests or work activity into any connected client.
	if _, err = journal(tx, domain.Event{Type: "session", SessionID: session.ID, Session: &session}); err != nil {
		return zero, err
	}
	return session, s.commit(tx)
}
