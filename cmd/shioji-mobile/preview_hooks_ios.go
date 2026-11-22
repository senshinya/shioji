package main

import (
	"errors"
	"fmt"
	"path"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

type mobilePreviewHooks struct {
	entries  []domain.HookEntry
	receipts map[string]domain.HookOperation
	revision int
	loseNext bool
}

func (a *mobileApp) seedPreviewHooks(loseNext bool) {
	a.model.WorkerMinor = protocol.Minor
	if len(a.model.Readiness.Harnesses) == 0 {
		a.model.Readiness.Harnesses = []domain.HarnessInfo{{ID: "codex", Name: "Codex", Configured: true,
			Capabilities: domain.HarnessCapabilities{HookManagement: true, HookInspection: true}}}
	}
	a.previewHooks = &mobilePreviewHooks{
		receipts: make(map[string]domain.HookOperation), revision: 1, loseNext: loseNext,
		entries: []domain.HookEntry{
			{ID: "format", DefinitionRevision: "format-v1", Event: "PostToolUse", Kind: "command", Command: "gofmt -w",
				SourcePath: ".codex/hooks.json", Matcher: "apply_patch", Scope: domain.HookProject,
				Fingerprint: "format-preview-fingerprint", Enabled: true, CanEnable: true, CanTrust: true, Trust: domain.HookUntrusted},
			{ID: "validate", DefinitionRevision: "validate-v2", Event: "PreToolUse", Kind: "command", Command: "go test ./internal/…",
				SourcePath: ".codex/hooks.json", Matcher: "shell", Scope: domain.HookProject, TimeoutSeconds: 10,
				Enabled: true, CanEnable: true, CanTrust: true, Trust: domain.HookModified},
			{ID: "check", DefinitionRevision: "check-v1", Event: "Stop", Kind: "command", Command: "go test ./…",
				SourcePath: "/Users/developer/.codex/config.toml", Scope: domain.HookUser,
				Enabled: true, CanEnable: true, CanTrust: true, Trust: domain.HookUntrusted},
		},
	}
}

func (a *mobileApp) previewHookCatalog(in domain.HookRequest) domain.HookCatalog {
	catalog := domain.HookCatalog{Revision: fmt.Sprint(a.previewHooks.revision)}
	if in.SessionID != "" {
		session := a.sessionByID(in.SessionID)
		catalog.Directory = session.Context.CWD
		catalog.RuntimeKnown = !session.Archived
	} else if ws := a.workspace(in.WorkspaceID); len(ws.Folders) > 0 {
		catalog.Directory = ws.Folders[0]
	}
	for _, h := range a.previewHooks.entries {
		if h.Scope == domain.HookProject {
			if in.WorkspaceID == "" {
				continue
			}
			h.SourcePath = path.Join(catalog.Directory, h.SourcePath)
		} else if in.WorkspaceID != "" {
			h.CanEnable, h.CanTrust = false, false
		}
		catalog.Entries = append(catalog.Entries, h)
	}
	return catalog
}

func (a *mobileApp) previewHookCall(method string, in domain.HookRequest) (any, error) {
	if a.previewHooks == nil {
		a.seedPreviewHooks(false)
	}
	catalog := a.previewHookCatalog(in)
	switch method {
	case "runtime.hooks.list":
		return catalog, nil
	case "runtime.hooks.operation":
		op, ok := a.previewHooks.receipts[in.OperationID]
		if !ok || !op.Input.Equal(in) {
			return nil, &protocol.RemoteError{Code: "notFound", Message: "操作未保存"}
		}
		return op, nil
	}
	selected := in.MatchingEntries(catalog)
	if catalog.Revision != in.Revision || len(selected) == 0 {
		return nil, errors.New("定义已变化")
	}
	for _, target := range selected {
		if !in.Allowed(target) {
			return nil, errors.New("当前来源只读")
		}
	}
	for _, target := range selected {
		for i, h := range a.previewHooks.entries {
			if h.ID != target.ID {
				continue
			}
			if in.Action == domain.HookSetEnabled {
				a.previewHooks.entries[i].Enabled = in.Enabled
			} else {
				a.previewHooks.entries[i].CanTrust = false
				a.previewHooks.entries[i].Trust = domain.HookTrusted
			}
		}
	}
	a.previewHooks.revision++
	result := domain.HookOperation{Input: in, Saved: true, State: domain.OperationConfirmed}
	a.previewHooks.receipts[in.OperationID] = result
	if a.previewHooks.loseNext {
		a.previewHooks.loseNext = false
		return nil, errors.New("模拟回执丢失")
	}
	next := a.previewHookCatalog(in)
	result.Catalog = &next
	return result, nil
}
