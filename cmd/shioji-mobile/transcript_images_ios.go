package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

type mobileImageKey struct {
	workspace              string
	attachment             domain.Attachment
	session, target, scope string
}

type mobileImageEntry struct {
	image  *ui.Bitmap
	file   app.LinkedFile
	epoch  string
	cancel context.CancelFunc
	used   time.Time
}

type mobileImageCache struct {
	directory       string
	selection       uint64
	files           *app.AttachmentCache
	originals       *attachments.Repository
	entries         map[mobileImageKey]*mobileImageEntry
	inFlight        int
	frame, previous time.Time
}

type previewImageResources struct {
	context context.Context
	dial    func(context.Context) (net.Conn, error)
}

func (a *mobileApp) transcriptAssetDial() func(context.Context) (net.Conn, error) {
	if !a.model.Connected {
		return nil
	}
	if a.previewResources != nil {
		return a.previewResources.dial
	}
	if a.controller != nil {
		return a.controller.DialDeviceStream
	}
	return nil
}

func (a *mobileApp) imageCache() *mobileImageCache {
	cache := a.transcriptImages
	if cache == nil || cache.directory != a.directory || cache.selection != a.deviceSelection {
		cache = &mobileImageCache{
			directory: a.directory, selection: a.deviceSelection,
			files:     a.displayAttachmentCache(a.directory),
			originals: attachments.New(filepath.Join(a.directory, "attachments")),
			entries:   make(map[mobileImageKey]*mobileImageEntry),
		}
		a.transcriptImages = cache
	}
	return cache
}

func (a *mobileApp) displayAttachmentCache(directory string) *app.AttachmentCache {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		cacheRoot = filepath.Join(directory, "cache")
	}
	key := filepath.Join(cacheRoot, "ShiojiMobile", "attachments", fmt.Sprintf("%x", sha256.Sum256([]byte(directory))))
	root := a.splitRoot()
	if root.displayAttachmentCaches == nil {
		root.displayAttachmentCaches = make(map[string]*app.AttachmentCache)
	}
	cache := root.displayAttachmentCaches[key]
	if cache == nil {
		cache = app.NewAttachmentCache(key)
		root.displayAttachmentCaches[key] = cache
	}
	return cache
}

func (a *mobileApp) requestImage(c *ui.Context, cache *mobileImageCache, key mobileImageKey,
	load func(context.Context) (*ui.Bitmap, app.LinkedFile)) (*mobileImageEntry, string) {
	now := c.Now()
	if cache.frame != now {
		cache.previous, cache.frame = cache.frame, now
	}
	entry := cache.entries[key]
	if entry != nil {
		entry.used = now
		if entry.image != nil {
			return entry, "图片已加载"
		}
		if entry.cancel != nil {
			return entry, "正在读取图片"
		}
		if entry.epoch == a.model.ConnectionEpoch {
			return entry, "图片暂不可用"
		}
	}
	if cache.inFlight >= 2 {
		return nil, "正在读取图片"
	}
	if entry == nil && len(cache.entries) >= 24 {
		var oldestKey mobileImageKey
		oldest := cache.previous
		revisit := false
		for k, candidate := range cache.entries {
			revisit = revisit || candidate.used.Before(now)
			if candidate.cancel == nil && candidate.used.Before(oldest) {
				oldestKey, oldest = k, candidate.used
			}
		}
		// Keep this and the previous frame's entries so an image near the
		// start of a long reply is not repeatedly evicted by later images.
		if oldestKey.workspace == "" {
			if revisit {
				c.After(50 * time.Millisecond)
			}
			return nil, "图片暂不可用"
		}
		delete(cache.entries, oldestKey)
	}
	parent := context.Background()
	if a.previewResources != nil {
		parent = a.previewResources.context
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	entry = &mobileImageEntry{epoch: a.model.ConnectionEpoch, cancel: cancel, used: now}
	cache.entries[key] = entry
	cache.inFlight++
	go func() {
		defer cancel()
		image, file := load(ctx)
		mygo.RunOnMain(func() {
			cache.inFlight--
			if cache.entries[key] == entry {
				entry.cancel, entry.image, entry.file = nil, image, file
			}
			if a.transcriptImages == cache && a.directory == cache.directory && a.deviceSelection == cache.selection {
				a.refresh()
			}
		})
	}()
	return entry, "正在读取图片"
}

func (a *mobileApp) loadTranscriptImage(c *ui.Context, session domain.Session, item domain.Attachment) (*ui.Bitmap, string) {
	if a.directory == "" || session.WorkspaceID == "" || item.Size <= 0 {
		return nil, "图片暂不可用"
	}
	cache := a.imageCache()
	dial, device := a.transcriptAssetDial(), a.model.Snapshot.DeviceID
	target := app.RemoteAttachmentTarget(device, session.WorkspaceID, func(ctx context.Context) (net.Conn, error) {
		if dial == nil || device == "" {
			return nil, errors.New("连接执行设备后读取图片")
		}
		return dial(ctx)
	})
	key := mobileImageKey{workspace: session.WorkspaceID, attachment: item}
	entry, state := a.requestImage(c, cache, key, func(ctx context.Context) (*ui.Bitmap, app.LinkedFile) {
		var image *ui.Bitmap
		_ = cache.files.Read(ctx, target, cache.originals, item, func(path string) error {
			var err error
			image, err = readMobileThumbnail(path)
			return err
		})
		return image, app.LinkedFile{}
	})
	if entry == nil {
		return nil, state
	}
	return entry.image, state
}

func imageAttachment(item domain.Attachment) bool {
	return item.Size > 0 && strings.HasPrefix(item.MIME, "image/")
}

func (a *mobileApp) transcriptImageAttachment(c *ui.Context, parent *mygo.Window, session domain.Session, item domain.Attachment, compact bool) {
	image, state := a.loadTranscriptImage(c, session, item)
	name := item.Name
	if name == "" {
		name = "图片"
	}
	mobileImageCard(c, name, state, image, compact, func() {
		if parent != nil && a.pageCurrent(parent) {
			a.attachmentPage(parent, session, item, false)
		}
	})
}

func mobileImageCard(c *ui.Context, name, state string, image *ui.Bitmap, compact bool, open func()) {
	p := palette(c)
	button := mobileButton(c, "预览 "+name, open).AlignSelf(ui.Start).MaxWidthPercent(100).MinWidth(0).Value(state).Disabled(open == nil)
	if compact {
		button.Gap(8).Padding(4, 8, 4, 4).Radius(8).Background(p.canvas).Children(func() {
			box := ui.Box(c).Size(48, 48).Shrink(0).Center().Clip().Radius(6).Background(p.inset)
			box.Children(func() {
				transcriptPicture(c, box, image)
			})
			oneLine(c, name, captionTextSize, p.ink).MinWidth(0).Shrink(1)
		})
		return
	}
	button.Width(224).Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Gap(5).Children(func() {
			box := ui.Box(c).FillWidth().Height(128).Center().Clip().Radius(8).Background(p.inset)
			box.Children(func() {
				transcriptPicture(c, box, image)
				if image == nil {
					label(c, state, captionTextSize, p.secondary).Padding(6).Absolute().Bottom(8)
				}
			})
			oneLine(c, name, captionTextSize, p.secondary).MinWidth(0).MaxWidthPercent(100)
		})
	})
}

func transcriptPicture(c *ui.Context, container ui.Element, image *ui.Bitmap) {
	waiting := ui.Local(container, "waiting", func() bool { return image == nil })
	if image == nil {
		*waiting = true
		ui.Icon(c, filesSymbol).Size(17, 17).TextColor(palette(c).secondary)
		return
	}
	bitmap := ui.Image(c.Key("bitmap"), image).Fill().Fit(ui.Contain)
	if *waiting && !c.Preferences().ReduceMotion {
		bitmap.Transition(ui.ElementTransition{Duration: 140 * time.Millisecond, Enter: &ui.Motion{Opacity: 0}})
	}
	*waiting = false
}
