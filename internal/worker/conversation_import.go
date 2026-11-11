package worker

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

func (s *Server) conversationScope(in protocol.ConversationQuery) (domain.Workspace, error) {
	var zero domain.Workspace
	if in.WorkspaceID == "" || in.HarnessID == "" || len(in.SourceRef) > 512 || len(in.Cursor) > 4096 {
		return zero, errors.New("历史查询范围无效")
	}
	snap, err := s.store.Snapshot()
	if err != nil {
		return zero, err
	}
	for _, w := range snap.Workspaces {
		if w.ID == in.WorkspaceID {
			if w.Revision != in.Revision || w.Archived || in.Directory != "" && !slices.Contains(w.Folders, in.Directory) {
				return zero, errors.New("工作区已变化，请重新打开导入")
			}
			return w, nil
		}
	}
	return zero, errors.New("工作区不可用")
}
func (s *Server) listConversations(in protocol.ConversationQuery) (domain.ConversationCatalog, error) {
	w, err := s.conversationScope(in)
	if err != nil {
		return domain.ConversationCatalog{}, err
	}
	r, release, err := s.borrowCatalog(in.HarnessID)
	if err != nil {
		return domain.ConversationCatalog{}, err
	}
	defer release()
	reader, ok := r.(harness.ConversationReader)
	if !ok {
		return domain.ConversationCatalog{}, harness.Unsupported("conversation-import")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	out, err := reader.Conversations(ctx, w.Folders, in.Cursor)
	if err != nil {
		return out, err
	}
	if len(out.Items) > 50 || len(out.Cursor) > 4096 {
		return domain.ConversationCatalog{}, errors.New("Agent 返回的历史目录超过上限")
	}
	for i, c := range out.Items {
		if c.SourceRef == "" || c.HarnessID != in.HarnessID || !slices.Contains(w.Folders, c.Directory) {
			return domain.ConversationCatalog{}, errors.New("Agent 返回了范围外的历史")
		}
		if old, e := s.store.ImportedConversation(w.ID, in.HarnessID, c.SourceRef); e == nil {
			out.Items[i].ImportedSessionID = old.ID
		} else if !errors.Is(e, sql.ErrNoRows) {
			return domain.ConversationCatalog{}, e
		}
	}
	if _, err = s.conversationScope(in); err != nil {
		return domain.ConversationCatalog{}, err
	}
	return out, nil
}
func (s *Server) previewConversation(in protocol.ConversationQuery) (domain.ConversationPreview, error) {
	w, err := s.conversationScope(in)
	if err != nil {
		return domain.ConversationPreview{}, err
	}
	if in.SourceRef == "" || in.Directory == "" {
		return domain.ConversationPreview{}, errors.New("缺少会话来源或目录")
	}
	r, release, err := s.borrowCatalog(in.HarnessID)
	if err != nil {
		return domain.ConversationPreview{}, err
	}
	defer release()
	reader, ok := r.(harness.ConversationReader)
	if !ok {
		return domain.ConversationPreview{}, harness.Unsupported("conversation-import")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	p, err := reader.ReadConversation(ctx, domain.LaunchContext{DeviceID: s.store.DeviceID(), CWD: in.Directory}, in.SourceRef)
	if err != nil {
		return p, err
	}
	if p.Candidate.SourceRef != in.SourceRef || p.Candidate.HarnessID != in.HarnessID || p.Candidate.Directory != in.Directory || !slices.Contains(w.Folders, p.Candidate.Directory) {
		return domain.ConversationPreview{}, errors.New("历史来源与请求不一致")
	}
	if len(p.Items) > 200 || len(p.Turns) > 50 || len(p.ResumeRef) > 4096 {
		return domain.ConversationPreview{}, errors.New("历史内容超过导入上限")
	}
	if _, err = s.conversationScope(in); err != nil {
		return domain.ConversationPreview{}, err
	}
	return p, nil
}
func (s *Server) importConversation(in protocol.ConversationImportInput) (domain.Session, error) {
	w, err := s.conversationScope(in.ConversationQuery)
	if err != nil {
		return domain.Session{}, err
	}
	if in.SourceRef == "" || in.Directory == "" {
		return domain.Session{}, errors.New("缺少导入来源")
	}
	if old, e := s.store.ImportedConversation(w.ID, in.HarnessID, in.SourceRef); e == nil {
		return old, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return domain.Session{}, e
	}
	p, err := s.previewConversation(in.ConversationQuery)
	if err != nil {
		return domain.Session{}, err
	}
	return s.store.ImportConversation(w, in.HarnessID, p, in.Config)
}
