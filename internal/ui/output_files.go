package ui

import (
	"context"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

var saveAttachmentIcon = svg("M10 3v9M6 8l4 4 4-4M4 13v4h12v-4")

func (v *View) outputFile(c *n.Context, t *n.Theme, a domain.Attachment) {
	button := n.ButtonBase(c.Key("file:"+a.ID)).FillWidth().MaxWidth(360).Padding(8, 10).Radius(6).Label("预览文件：" + a.Name)
	background := codeSurface(t)
	if button.Hovered() || button.FocusVisible() {
		background = t.SurfaceHover
	}
	surface := button.AnimateColor("hover", background, motionAnimation(c, 120*time.Millisecond))
	button.Background(surface)
	button.Children(func() {
		fileResultContents(c, t, a)
	})
	if p := v.attachmentPreview; p != nil && p.Attachment.ID == a.ID && p.Origin != nil {
		p.Origin.Surface = surface
	}
	if button.Clicked() {
		theme := *t
		v.openAttachment(a, v.Model.WorkspaceID, button.Bounds(), attachmentPreviewOrigin{Surface: surface, Radius: 6, Content: func(c *n.Context) {
			n.Column(c).Fill().Padding(8, 10).Children(func() { fileResultContents(c, &theme, a) })
		}})
	}
}

func fileResultContents(c *n.Context, t *n.Theme, a domain.Attachment) {
	n.Row(c).FillWidth().Gap(8).AlignItems(n.Baseline).Children(func() {
		font := n.Font{Family: t.Font, Size: 12, Weight: 400}
		inlineSymbol(c, fileAttachmentIcon, a.Name, font, 14, t.TextMuted)
		n.Text(c, a.Name).FontSize(12).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).TooltipWhenTruncated(a.Name)
		n.Text(c, attachmentSize(a.Size)).FontSize(11).TextColor(t.TextMuted)
	})
}

func (v *View) saveAttachment(p *attachmentPreview) {
	if p.Saving || v.Controller == nil || v.StatePath == "" {
		return
	}
	p.Saving, p.SaveError = true, ""
	target, targetErr := v.Model.AttachmentTarget(v.Controller.Directory, p.WorkspaceID)
	store, parent := v.staging(), v.nativeWindow
	localPath := p.LocalPath
	cache := v.displayCache()
	v.Controller.AttachmentTask(func() app.Update {
		name := p.Attachment.Name
		if filepath.Ext(name) == "" {
			name += map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif"}[p.Attachment.MIME]
		}
		destination, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: parent, Title: "另存到本机", DefaultPath: name, CreateDirectories: true})
		if err == nil && destination != "" && localPath != "" {
			err = attachments.ExportFile(p.Attachment, localPath, destination)
		} else if err == nil && destination != "" {
			err = targetErr
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				err = cache.Read(ctx, target, store, p.Attachment, func(path string) error {
					return attachments.ExportFile(p.Attachment, path, destination)
				})
			}
		}
		return func(*app.Model) {
			p.Saving = false
			if err != nil {
				p.SaveError = err.Error()
			}
		}
	})
}
