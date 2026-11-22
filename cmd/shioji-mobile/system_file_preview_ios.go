package main

import (
	"context"
	"errors"
	"net"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

func mobileMarkdownFile(name, mime string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]), "text/markdown")
}

func (a *mobileApp) previewAttachment(parent *mygo.Window, session domain.Session, item domain.Attachment, fallback func()) {
	if !a.visibleLinkSource(parent, session) || a.directory == "" {
		return
	}
	selection, model := a.deviceSelection, a.model
	epoch, device, dial := model.ConnectionEpoch, model.Snapshot.DeviceID, a.transcriptAssetDial()
	cache := a.displayAttachmentCache(a.directory)
	originals := attachments.New(filepath.Join(a.directory, "attachments"))
	worker := app.RemoteAttachmentTarget(device, session.WorkspaceID, func(ctx context.Context) (net.Conn, error) {
		if dial == nil {
			return nil, errors.New("连接所属设备后再打开附件")
		}
		return dial(ctx)
	})
	current := func() bool {
		return a.deviceSelection == selection && a.model == model && epoch == model.ConnectionEpoch &&
			a.visibleLinkSource(parent, session)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	unregister := parent.OnClosed(cancel)
	go func() {
		defer cancel()
		var shown *bool
		err := cache.Read(ctx, worker, originals, item, func(local string) error {
			var args map[string]any
			mygo.RunOnMain(func() {
				if current() {
					args = mobilePreviewArgs(parent, local, item.Name, item.MIME)
				}
			})
			if args == nil {
				return nil
			}
			// The cache stays pinned until Quick Look owns its temporary link.
			return uikit.Call(ctx, "quickLook", args, &shown)
		})
		mygo.RunOnMain(func() {
			unregister()
			if !current() {
				return
			}
			if err != nil {
				if a.preview && a.previewResources == nil {
					fallback()
					return
				}
				a.linkError(err)
			} else if shown != nil && !*shown {
				fallback()
			}
		})
	}()
}
