package ui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
)

type markdownImageEntry struct {
	id        string
	thumbnail attachmentImage
	preview   *attachmentPreview
	result    app.LinkedFile
}

// UI-owned, scoped to the accepted session and connection. File reads always
// go through the owning worker; only verified downloaded copies are decoded.
type markdownImageCache struct {
	scope       string
	ctx         context.Context
	cancel      context.CancelFunc
	heightDelta float32
	slots       chan struct{}
	entries     map[string]*markdownImageEntry
}

func (v *View) closeMarkdownImages() {
	if v.markdownImages != nil {
		v.markdownImages.cancel()
		clear(v.markdownImages.entries)
		v.markdownImages = nil
	}
}

func (v *View) bindMarkdownImages(options *markdown.Options, source domain.Session) {
	scope := source.ID + "\x00" + source.WorkspaceID + "\x00" + v.Model.ConnectionEpoch + "\x00" + source.Context.CWD + "\x00" + strings.Join(source.Context.AdditionalDirectories, "\x00") + "\x00" + strconv.FormatBool(v.Model.Connected)
	if v.markdownImages == nil || v.markdownImages.scope != scope {
		v.closeMarkdownImages()
		ctx, cancel := context.WithCancel(context.Background())
		v.markdownImages = &markdownImageCache{scope: scope, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 2), entries: map[string]*markdownImageEntry{}}
	}
	cache := v.markdownImages
	options.Image = func(c *n.Context, dest, label string) {
		if label == "" {
			label = "图片"
		}
		n.Column(c).Width(224).MaxWidthPercent(100).Gap(6).Children(func() {
			entry := v.requestMarkdownImage(cache, source, dest)
			if entry == nil {
				c.After(50 * time.Millisecond)
			}
			a := domain.Attachment{ID: "markdown:" + dest, Name: label, MIME: "image/png"}
			var thumbnail *attachmentImage
			if entry != nil {
				thumbnail = &entry.thumbnail
				a.ID = entry.id
			}
			cache.heightDelta += v.outputImageCard(c, c.Theme(), a, thumbnail, func(rect n.Rect) {
				if entry == nil || entry.preview == nil || v.markdownImages != cache {
					return
				}
				p := *entry.preview
				p.Source, p.Image, p.Thumbnail, p.presence = rect, entry.thumbnail.Image, entry.thumbnail.Image, previewOpening
				p.Origin = &attachmentPreviewOrigin{Radius: 8}
				v.attachmentPreview = &p
				// Keep only 512px thumbnails in the gallery. Verify and
				// decode the cached snapshot at viewer size on demand.
				v.Controller.AttachmentTask(func() app.Update {
					full, err := prepareLinkedPreview(entry.result, source.WorkspaceID, source.ID, p.SourceEpoch)
					return func(*app.Model) {
						if cache.ctx.Err() != nil || v.attachmentPreview != &p || !p.open() {
							return
						}
						if err != nil {
							p.Error = err.Error()
						} else if full != nil {
							p.LoadedImage = full.LoadedImage
						}
					}
				})
			}, func() { delete(cache.entries, dest) })
		})
	}
}

func (v *View) requestMarkdownImage(cache *markdownImageCache, source domain.Session, dest string) *markdownImageEntry {
	if err := cache.ctx.Err(); err != nil {
		return &markdownImageEntry{thumbnail: attachmentImage{phase: attachmentImageFailed, Error: err.Error()}}
	}
	if entry := cache.entries[dest]; entry != nil {
		entry.thumbnail.Used = v.replyFrame
		return entry
	}
	if len(cache.entries) >= 32 {
		oldest, old := ^uint64(0), ""
		allVisible := true
		for path, entry := range cache.entries {
			allVisible = allVisible && entry.thumbnail.Used == v.replyFrame
			if entry.thumbnail.phase != attachmentImageLoading && entry.thumbnail.Used+1 < v.replyFrame && entry.thumbnail.Used < oldest {
				oldest, old = entry.thumbnail.Used, path
			}
		}
		if old == "" {
			if allVisible {
				return &markdownImageEntry{id: "markdown-limit:" + dest, thumbnail: attachmentImage{phase: attachmentImageFailed, Error: "当前可见图片超出预览数量限制"}}
			}
			return nil
		}
		delete(cache.entries, old)
	}
	entry := &markdownImageEntry{id: domain.NewID(), thumbnail: attachmentImage{phase: attachmentImageLoading, Used: v.replyFrame}}
	cache.entries[dest] = entry
	path, err := fileLinkPath(dest)
	if err == nil && (v.Controller == nil || v.StatePath == "" || !v.Model.Connected || source.ID == "") {
		err = errors.New("设备未连接，无法读取图片")
	}
	if err != nil {
		entry.thumbnail.phase, entry.thumbnail.Error = attachmentImageFailed, err.Error()
		return entry
	}
	target, err := v.Model.AttachmentTarget(v.Controller.Directory, source.WorkspaceID)
	controller, directory, epoch := v.Controller, v.linkedFileDirectory(), v.Model.ConnectionEpoch
	controller.AttachmentTask(func() app.Update {
		select {
		case cache.slots <- struct{}{}:
			defer func() { <-cache.slots }()
		case <-cache.ctx.Done():
			return nil
		}
		ctx, cancel := context.WithTimeout(cache.ctx, time.Minute)
		defer cancel()
		var result app.LinkedFile
		var preview *attachmentPreview
		var thumbnail n.ImageSource
		if err == nil && target.SSH != nil {
			routed, e := controller.TailnetSSH(*target.SSH)
			target.SSH, err = &routed, e
		}
		if err == nil {
			result, err = target.FetchFile(ctx, directory, source.ID, path, func(meta domain.FileResource, _ int64) {
				if meta.Size > 8<<20 || !strings.HasPrefix(meta.MIME, "image/") {
					cancel()
				}
			})
		}
		if err == nil {
			preview, err = prepareLinkedPreviewSize(result, source.WorkspaceID, source.ID, epoch, 512)
			if err == nil && (preview == nil || preview.LoadedImage == nil) {
				err = errors.New("此文件不能作为图片预览")
			}
		}
		if err == nil {
			thumbnail = preview.LoadedImage
		}
		return func(*app.Model) {
			if cache.ctx.Err() != nil || v.markdownImages != cache {
				return
			}
			entry.thumbnail.phase = attachmentImageReady
			if preview != nil {
				preview.Attachment.ID = entry.id
			}
			entry.thumbnail.Image, entry.preview, entry.result = thumbnail, preview, result
			if err != nil {
				entry.thumbnail.phase, entry.thumbnail.Error = attachmentImageFailed, err.Error()
			}
		}
	})
	return entry
}
