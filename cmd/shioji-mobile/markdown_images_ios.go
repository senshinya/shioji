package main

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func (a *mobileApp) releaseMarkdownImages(session string) {
	if a.transcriptImages == nil {
		return
	}
	for key, entry := range a.transcriptImages.entries {
		if key.session != "" && (session == "" || key.session == session) {
			if entry.cancel != nil {
				entry.cancel()
			}
			delete(a.transcriptImages.entries, key)
		}
	}
}

func (a *mobileApp) bindMobileMarkdownImages(parent *mygo.Window, source domain.Session, sourcePath string, options *markdown.Options) {
	options.Image = func(c *ui.Context, dest, name string) {
		target, _, err := mobileFileLinkTarget(sourcePath, dest)
		if name == "" {
			name = "图片"
			if err == nil {
				name = path.Base(target)
			}
		}
		if err != nil {
			label(c, name, captionTextSize, palette(c).secondary).Selectable()
			return
		}
		dial := a.transcriptAssetDial()
		if dial == nil || a.directory == "" || source.ID == "" || source.WorkspaceID == "" {
			mobileImageCard(c, name, "连接设备后读取图片", nil, false, nil)
			return
		}
		scope := source.Context.CWD + "\x00" + strings.Join(source.Context.AdditionalDirectories, "\x00")
		key := mobileImageKey{workspace: source.WorkspaceID, session: source.ID, target: target, scope: scope}
		cache := a.imageCache()
		routed := app.RemoteAttachmentTarget(a.model.Snapshot.DeviceID, source.WorkspaceID, dial)
		directory := filepath.Join(a.directory, "cache", "linked-files")
		entry, state := a.requestImage(c, cache, key, func(ctx context.Context) (*ui.Bitmap, app.LinkedFile) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			file, err := routed.FetchFile(ctx, directory, source.ID, target, func(meta domain.FileResource, _ int64) {
				if meta.Size <= 0 || meta.Size > 8<<20 || !strings.HasPrefix(meta.MIME, "image/") {
					cancel()
				}
			})
			if err != nil || ctx.Err() != nil {
				return nil, app.LinkedFile{}
			}
			image, err := readMobileThumbnail(file.Path)
			if err != nil {
				return nil, app.LinkedFile{}
			}
			return image, file
		})
		var image *ui.Bitmap
		if entry != nil {
			image = entry.image
		}
		mobileImageCard(c, name, state, image, false, func() {
			if entry == nil || cache != a.transcriptImages || cache.entries[key] != entry {
				return
			}
			if entry.image != nil {
				a.openLinkedFile(parent, source, sourcePath, &mobileLinkedPreview{target: target, file: entry.file})
			} else if entry.cancel == nil {
				delete(cache.entries, key)
				a.refresh()
			}
		})
	}
}
