package app

import (
	"context"
	"errors"
	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (t AttachmentTarget) SearchFiles(ctx context.Context, session, query string) (domain.FileSearchResult, error) {
	var out domain.FileSearchResult
	client, err := t.connect(ctx)
	if err != nil {
		return out, err
	}
	defer client.Close()
	if client.Minor < 11 {
		return out, errors.New("请先升级执行服务以引用文件")
	}
	err = client.Call(ctx, "file.search", protocol.FileSearchQuery{GitQuery: protocol.GitQuery{WorkspaceID: t.WorkspaceID, SessionID: session}, Query: query}, &out)
	return out, err
}
func (t AttachmentTarget) SnapshotFile(ctx context.Context, session string, file domain.WorkspaceFile) (domain.Attachment, error) {
	var out domain.Attachment
	client, err := t.connect(ctx)
	if err != nil {
		return out, err
	}
	defer client.Close()
	if client.Minor < 11 {
		return out, errors.New("请先升级执行服务以引用文件")
	}
	err = client.Call(ctx, "file.snapshot", protocol.FileSnapshotQuery{GitQuery: protocol.GitQuery{WorkspaceID: t.WorkspaceID, SessionID: session}, Root: file.Root, Path: file.Path}, &out)
	return out, err
}

func (t AttachmentTarget) Skills(ctx context.Context, session, harnessID string) (domain.SkillCatalog, error) {
	var out domain.SkillCatalog
	client, err := t.connect(ctx)
	if err != nil {
		return out, err
	}
	defer client.Close()
	if client.Minor < 11 {
		return out, errors.New("请先升级执行服务以使用 Skill 菜单")
	}
	err = client.Call(ctx, "skills.list", protocol.SkillQuery{GitQuery: protocol.GitQuery{WorkspaceID: t.WorkspaceID, SessionID: session}, HarnessID: harnessID}, &out)
	return out, err
}
