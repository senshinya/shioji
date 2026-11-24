package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/domain"
	"shioji/internal/protocol"
)

func (a *mobileApp) canChangeWorkspaceArchive(workspace domain.Workspace) bool {
	m := a.model
	return workspace.ID != "" && workspace.DeviceID == m.Snapshot.DeviceID && m.Connected &&
		(a.preview || m.WorkerMinor >= 5 && m.ConnectionEpoch != "" && a.endpoint.DeviceID == workspace.DeviceID)
}

func (a *mobileApp) confirmWorkspaceArchive(w *mygo.Window, req *mobileRequest, source domain.Workspace, done func()) {
	if req.busy || a.archiveConfirmation != nil || !a.pageCurrent(w) || source.Archived || !a.canChangeWorkspaceArchive(source) {
		return
	}
	m := a.model
	epoch, generation := m.ConnectionEpoch, m.Snapshot.Generation
	device := a.deviceName()
	args := map[string]any{
		"window": w.NativeHandle(),
		"title":  "归档工作区？",
		"detail": fmt.Sprintf("“%s” · %s\n\n归档会结束其中会话的运行环境，并保留历史、草稿和文件。", source.Name, device),
	}
	a.archiveConfirmation = w
	ctx, cancel := context.WithCancel(context.Background())
	unregister := w.OnClosed(cancel)
	go func() {
		defer cancel()
		var confirmed bool
		err := uikit.Call(ctx, "confirmWorkspaceArchive", args, &confirmed)
		mygo.RunOnMain(func() {
			unregister()
			if a.archiveConfirmation == w {
				a.archiveConfirmation = nil
			}
			if !a.pageCurrent(w) || a.model != m || m.ConnectionEpoch != epoch || m.Snapshot.Generation != generation {
				return
			}
			if err != nil {
				req.err = err.Error()
				w.Invalidate()
				return
			}
			if confirmed {
				a.changeWorkspaceArchive(w, req, source, true, done)
			}
		})
	}()
}

func (a *mobileApp) changeWorkspaceArchive(w *mygo.Window, req *mobileRequest, source domain.Workspace, archived bool, done func()) {
	if req.busy || !a.pageCurrent(w) || !a.canChangeWorkspaceArchive(source) {
		return
	}
	current := a.workspace(source.ID)
	if current.ID == "" || current.DeviceID != source.DeviceID || current.Revision != source.Revision || current.Archived == archived {
		req.err = "工作区已改变，请重新载入后再试"
		w.Invalidate()
		return
	}
	if a.preview {
		for i := range a.model.Snapshot.Workspaces {
			workspace := &a.model.Snapshot.Workspaces[i]
			if workspace.ID != current.ID || workspace.DeviceID != current.DeviceID {
				continue
			}
			workspace.Archived = archived
			workspace.ArchivedAt = nil
			workspace.Revision++
			if archived {
				now := time.Now().UTC()
				workspace.ArchivedAt = &now
			}
			break
		}
		req.err = ""
		a.updateArchivedBrowseScope(a.model.Snapshot)
		if done != nil {
			done()
		}
		a.refresh()
		return
	}
	input := protocol.WorkspaceArchiveInput{WorkspaceID: current.ID, Revision: current.Revision, Archived: archived}
	a.pageJob(w, req, "workspace.archive", input, decodePage(func(result domain.Workspace) error {
		if result.ID != current.ID || result.DeviceID != current.DeviceID || result.Revision <= current.Revision || result.Archived != archived {
			return errors.New("归档状态回执不属于当前工作区")
		}
		if done != nil {
			done()
		}
		return nil
	}))
}

func (a *mobileApp) archivedConversationFooter(c *ui.Context, parent *mygo.Window, session domain.Session, workspace domain.Workspace) {
	p := palette(c)
	kind := "会话"
	if workspace.Archived {
		kind = "工作区"
	}
	req := ui.Local(c.Root(), a.model.WorkKey(session.ID, "restore-archive"), func() mobileRequest { return mobileRequest{} })
	mobileContent(c, conversationComposerWidth).Padding(4, pageInset, 6).Children(func() {
		ui.Row(c).Padding(0, 10).Gap(8).AlignItems(ui.Center).Background(p.panel).Radius(panelRadius).Children(func() {
			label(c, kind+"已归档 · 只读", captionTextSize, p.secondary).Grow(1).MinWidth(0).Role(ui.RoleStatus)
			action(c, "恢复"+kind, func() {
				if !a.pageCurrent(parent) {
					return
				}
				if workspace.Archived {
					a.changeWorkspaceArchive(parent, req, workspace, false, nil)
				} else {
					a.confirmSessionArchive(parent, session)
				}
			}).Disabled(!a.model.Connected || req.busy || workspace.Archived && !a.canChangeWorkspaceArchive(workspace))
		})
		req.status(c)
	})
}
