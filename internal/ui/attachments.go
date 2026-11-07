package ui

import (
	"context"
	"fmt"
	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
	"image"
	"io"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"strings"
	"time"
)

type attachmentPreview struct {
	Attachment             domain.Attachment
	presence               previewPresence
	Loading                bool
	Image                  n.ImageSource
	Thumbnail, LoadedImage n.ImageSource
	Text, Error            string
	NaturalHeight          float32
	Source                 n.Rect
}

// Presence owns the close snapshot until the native size interpolation ends.
// Closing before first measurement still needs the same initial geometry.
type previewPresence uint8

const (
	previewHidden previewPresence = iota
	previewOpening
	previewShown
	previewClosingUnmeasured
	previewClosing
)

func (p *attachmentPreview) open() bool {
	return p.presence == previewOpening || p.presence == previewShown
}
func (p *attachmentPreview) visible() bool { return p.presence != previewHidden }
func (p *attachmentPreview) motionReady() bool {
	return p.presence != previewOpening && p.presence != previewClosingUnmeasured
}
func (p *attachmentPreview) measured() {
	switch p.presence {
	case previewOpening:
		p.presence = previewShown
	case previewClosingUnmeasured:
		p.presence = previewClosing
	}
}
func (p *attachmentPreview) close() {
	switch p.presence {
	case previewOpening:
		p.presence = previewClosingUnmeasured
	case previewShown:
		p.presence = previewClosing
	}
}

type attachmentImage struct {
	Image n.ImageSource
	phase attachmentImagePhase
	Error string
	Used  uint64
}

type attachmentImagePhase uint8

const (
	attachmentImageReady attachmentImagePhase = iota
	attachmentImageLoading
	attachmentImageFailed
)

var fileAttachmentIcon = svg("M5 2h7l4 4v12H5zM12 2v5h4M8 10h5M8 13h5")

func (v *View) staging() *attachments.Repository {
	if v.attachmentStore == nil {
		v.attachmentStore = attachments.New(filepath.Join(filepath.Dir(v.StatePath), "attachments"))
	}
	return v.attachmentStore
}
func (v *View) pickAttachments() {
	if v.Controller == nil || v.StatePath == "" {
		return
	}
	key := v.Model.DraftKey()
	token := v.attachmentPreparationKey(key)
	v.Controller.AttachmentTask(func() app.Update {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "添加附件", Multiple: true, Message: "PNG、JPEG、GIF 图片或 UTF-8 文本文件"})
		return func(m *app.Model) {
			if err != nil {
				m.Error = err.Error()
				return
			}
			if v.attachmentPreparationKey(key) == token {
				v.addAttachmentFiles(key, paths)
			}
		}
	})
}
func (v *View) addAttachmentFiles(key string, paths []string) {
	if v.Controller == nil || v.StatePath == "" || len(paths) == 0 {
		return
	}
	store := v.staging()
	token := v.attachmentPreparationKey(key)
	if v.attachmentPreparing == nil {
		v.attachmentPreparing = map[string]int{}
	}
	v.attachmentPreparing[token]++
	v.Controller.AttachmentTask(func() app.Update {
		var added []app.DraftAttachment
		var problems []string
		for _, path := range paths {
			file, err := os.Open(path)
			var data []byte
			if err == nil {
				info, e := file.Stat()
				if e != nil {
					err = e
				} else if !info.Mode().IsRegular() {
					err = fmt.Errorf("不支持目录")
				} else {
					data, err = io.ReadAll(io.LimitReader(file, (8<<20)+1))
				}
				file.Close()
			}
			var a domain.Attachment
			if err == nil {
				a, err = store.Save(filepath.Base(path), data)
			}
			if err != nil {
				problems = append(problems, filepath.Base(path)+"："+err.Error())
				continue
			}
			added = append(added, app.DraftAttachment{Attachment: a})
		}
		return func(m *app.Model) {
			v.attachmentPreparing[token]--
			if v.attachmentPreparationKey(key) != token {
				return
			}
			v.appendAttachments(key, added)
			if len(problems) > 0 {
				m.Error = strings.Join(problems, "\n")
			}
		}
	})
}
func (v *View) validateAddedAttachments(key string, added []app.DraftAttachment) error {
	refs := []domain.Attachment{}
	for _, a := range v.Model.Attachments[key] {
		refs = append(refs, a.Attachment)
	}
	for _, a := range added {
		refs = append(refs, a.Attachment)
	}
	return (domain.Submission{Text: v.Model.Drafts[key], Attachments: refs}).ValidateContent()
}
func (v *View) appendAttachments(key string, added []app.DraftAttachment) {
	if err := v.validateAddedAttachments(key, added); err != nil {
		v.Model.Error = err.Error()
		return
	}
	v.Model.Attachments[key] = append(v.Model.Attachments[key], added...)
}
func (v *View) pasteAttachments(key, text string) bool {
	if v.Controller == nil || v.StatePath == "" {
		return false
	}
	if files, err := mygo.Clipboard.ReadFiles(); err == nil && len(files) > 0 {
		v.addAttachmentFiles(key, files)
		return true
	}
	if text != "" {
		return false
	}
	data := mygo.Clipboard.ReadImage()
	if len(data) == 0 {
		return false
	}
	store := v.staging()
	token := v.attachmentPreparationKey(key)
	if v.attachmentPreparing == nil {
		v.attachmentPreparing = map[string]int{}
	}
	v.attachmentPreparing[token]++
	v.Controller.AttachmentTask(func() app.Update {
		a, err := store.Save("粘贴图片.png", data)
		return func(m *app.Model) {
			v.attachmentPreparing[token]--
			if v.attachmentPreparationKey(key) != token {
				return
			}
			if err != nil {
				m.Error = err.Error()
				return
			}
			v.appendAttachments(key, []app.DraftAttachment{{Attachment: a}})
		}
	})
	return true
}
func (v *View) uploadDraft(key string, index int) {
	m := v.Model
	if v.Controller == nil || v.StatePath == "" || !m.Connected || m.WorkerMinor < 6 {
		return
	}
	if v.attachmentUploading == nil {
		v.attachmentUploading = map[string]bool{}
	}
	draft := m.Attachments[key][index]
	workspace := m.WorkspaceID
	target, err := m.AttachmentTarget(v.Controller.Directory, workspace)
	if err != nil {
		return
	}
	token := target.DeviceID + ":" + workspace + ":" + draft.Attachment.ID
	if v.attachmentUploading[token] || (draft.Error != "" && draft.WorkspaceID == workspace && draft.DeviceID == target.DeviceID) {
		return
	}
	v.attachmentUploading[token] = true
	draft.Ready = false
	draft.Error = ""
	draft.WorkspaceID = workspace
	draft.DeviceID = target.DeviceID
	m.Attachments[key][index] = draft
	store := v.staging()
	v.Controller.AttachmentTask(func() app.Update {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := target.Upload(ctx, store, draft.Attachment)
		return func(m *app.Model) {
			delete(v.attachmentUploading, token)
			for i, a := range m.Attachments[key] {
				if a.Attachment.ID == draft.Attachment.ID && a.WorkspaceID == workspace && a.DeviceID == target.DeviceID {
					a.Ready = err == nil
					a.DeviceID = target.DeviceID
					a.WorkspaceID = workspace
					a.Error = ""
					if err != nil {
						a.Error = err.Error()
					}
					m.Attachments[key][i] = a
				}
			}
		}
	})
}
func (v *View) draftAttachmentStrip(c *n.Context, t *n.Theme) {
	v.draftAttachmentStripFor(c, t, v.Model.DraftKey())
}
func (v *View) draftAttachmentStripFor(c *n.Context, t *n.Theme, key string) {
	m := v.Model
	drafts := m.Attachments[key]
	regular := 0
	for _, a := range drafts {
		if a.Attachment.SourcePath == "" {
			regular++
		}
	}
	preparing := v.preparingAttachments(key) && (v.fileMention == nil || v.fileMention.key != key || v.fileMention.phase != mentionCapturing)
	strip := n.ScrollHorizontal(c.Key("draft-attachments:" + key)).FillWidth().MarginX(9)
	target := float32(0)
	if regular > 0 || preparing {
		target = 1
	}
	presence := strip.AnimateTo("presence", target, n.Animation{Duration: 360 * time.Millisecond, Ease: composerDescentEase})
	removeID := ""
	strip.Height(76 * presence).Clip().Hidden(presence == 0).Children(func() {
		n.Row(c).Gap(8).AlignItems(n.Center).Children(func() {
			for i, a := range drafts {
				if a.Attachment.SourcePath != "" {
					continue
				}
				ready := a.Ready && a.WorkspaceID == m.WorkspaceID
				if target, err := m.AttachmentTarget(v.ControllerDirectory(), m.WorkspaceID); err == nil {
					ready = ready && a.DeviceID == target.DeviceID
				}
				if !ready {
					v.uploadDraft(key, i)
				}
				motion := fadeMotion(c, 180*time.Millisecond)
				motion.Position = true
				tile := n.Column(c.Key(a.Attachment.ID)).Size(136, 64).Radius(8).Background(codeSurface(t)).Border(1, t.Border).Transition(motion)
				tile.Children(func() {
					var source n.Rect
					button := control(c).Fill().Padding(7).Label("预览附件：" + a.Attachment.Name).Tooltip(a.Attachment.Name).Children(func() {
						n.Row(c).FillWidth().Gap(7).AlignItems(n.Center).Children(func() {
							source = v.attachmentThumbnail(c, t, a.Attachment, m.WorkspaceID, 36).Bounds()
							n.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
								n.Text(c, a.Attachment.Name).SingleLine().Ellipsis("…").FontSize(12)
								label := attachmentSize(a.Attachment.Size)
								if a.Error != "" {
									label = "上传失败"
								} else if !ready {
									label = "准备中…"
								}
								n.Text(c, label).FontSize(11).TextColor(t.TextMuted)
							})
						})
					})
					if button.Clicked() {
						if !strings.HasPrefix(a.Attachment.MIME, "image/") {
							source = button.Bounds()
						}
						v.openAttachment(a.Attachment, m.WorkspaceID, source)
					}
					if iconButton(c, queueRemoveIcon, "移除附件："+a.Attachment.Name).Absolute().Right(1).Top(1).Size(18, 18).Radius(9).Background(raised(t)).Clicked() {
						removeID = a.Attachment.ID
					}
				})
				if a.Error != "" {
					if ghost(c, "重试").FontSize(11).Tooltip(a.Error).Clicked() {
						a.Error = ""
						m.Attachments[key][i] = a
					}
				}
			}
			if preparing {
				n.Text(c, "准备附件…").FontSize(12).TextColor(t.TextMuted)
			}
		})
	})
	if removeID != "" {
		var kept []app.DraftAttachment
		for _, a := range m.Attachments[key] {
			if a.Attachment.ID != removeID {
				kept = append(kept, a)
			}
		}
		m.Attachments[key] = kept
	}
}
func attachmentSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1<<20 {
		return fmt.Sprintf("%.0f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1<<20))
}

var attachmentDecodeSlots = make(chan struct{}, 2)

func decodeAttachmentImage(path string, maximum int) (n.ImageSource, error) {
	attachmentDecodeSlots <- struct{}{}
	defer func() { <-attachmentDecodeSlots }()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return nil, fmt.Errorf("图片尺寸超出限制")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	scale := min(1, float64(maximum)/float64(max(b.Dx(), b.Dy())))
	small := image.NewNRGBA(image.Rect(0, 0, max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))))
	draw.ApproxBiLinear.Scale(small, small.Bounds(), img, b, draw.Over, nil)
	return n.NewBitmap(small), nil
}
func (v *View) attachmentThumbnail(c *n.Context, t *n.Theme, a domain.Attachment, workspace string, size float32) n.Element {
	if !strings.HasPrefix(a.MIME, "image/") {
		return n.Column(c).Size(size, size).AlignItems(n.Center).Justify(n.Center).Children(func() { n.Icon(c, fileAttachmentIcon).Size(22, 22).TextColor(t.TextMuted) })
	}
	if v.attachmentImages == nil {
		v.attachmentImages = map[string]*attachmentImage{}
	}
	cached := v.attachmentImages[a.ID]
	if cached == nil && v.Controller != nil && v.StatePath != "" {
		if len(v.attachmentImages) >= 32 {
			old := ""
			var oldest uint64 = ^uint64(0)
			for id, e := range v.attachmentImages {
				if e.phase != attachmentImageLoading && e.Used < oldest {
					old, oldest = id, e.Used
				}
			}
			if old != "" {
				delete(v.attachmentImages, old)
			} else {
				return n.Column(c).Size(size, size).Radius(5).Background(t.Border.Alpha(.35))
			}
		}
		cached = &attachmentImage{phase: attachmentImageLoading}
		v.attachmentImages[a.ID] = cached
		target, err := v.Model.AttachmentTarget(v.Controller.Directory, workspace)
		store := v.staging()
		entry := cached
		v.Controller.AttachmentTask(func() app.Update {
			var img n.ImageSource
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				var path string
				path, err = target.Download(ctx, store, a)
				if err == nil {
					img, err = decodeAttachmentImage(path, 256)
				}
			}
			return func(*app.Model) {
				entry.phase = attachmentImageReady
				entry.Image = img
				if err != nil {
					entry.Error = err.Error()
					entry.phase = attachmentImageFailed
				}
			}
		})
	}
	return n.Column(c.Key("thumbnail:"+a.ID)).Size(size, size).Radius(5).Clip().Background(t.Border.Alpha(.35)).Children(func() {
		if cached != nil {
			cached.Used = v.replyFrame
			if cached.Image != nil {
				n.Image(c.Key("loaded-image"), cached.Image).Size(size, size).Fit(n.Cover).Radius(5).Transition(fadeMotion(c, 160*time.Millisecond))
			}
		}
	})
}
func (v *View) openAttachment(a domain.Attachment, workspace string, source n.Rect) {
	if v.Controller == nil || v.StatePath == "" {
		return
	}
	p := &attachmentPreview{Attachment: a, presence: previewOpening, Loading: true, Source: source}
	if cached := v.attachmentImages[a.ID]; cached != nil {
		p.Image = cached.Image
		p.Thumbnail = cached.Image
	}
	v.attachmentPreview = p
	target, err := v.Model.AttachmentTarget(v.Controller.Directory, workspace)
	store := v.staging()
	v.Controller.AttachmentTask(func() app.Update {
		var img n.ImageSource
		var text string
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			var path string
			path, err = target.Download(ctx, store, a)
			if err == nil {
				if strings.HasPrefix(a.MIME, "image/") {
					img, err = decodeAttachmentImage(path, 1600)
				} else {
					var data []byte
					data, err = os.ReadFile(path)
					text = string(data)
				}
			}
		}
		return func(*app.Model) {
			if v.attachmentPreview != p {
				return
			}
			p.Loading = false
			p.LoadedImage = img
			p.Text = text
			if err != nil {
				p.Error = err.Error()
			}
		}
	})
}
func (v *View) attachmentDialog(c *n.Context, t *n.Theme) {
	p := v.attachmentPreview
	if p == nil || !p.visible() {
		return
	}
	isImage := strings.HasPrefix(p.Attachment.MIME, "image/")
	reduce := c.Preferences().ReduceMotion
	morph := isImage && !reduce && p.Source.W > 0 && p.Source.H > 0
	visible := true
	if reduce || (isImage && !morph) {
		visible = p.open()
	}
	dialog := n.DialogBase(c.Key("attachment-preview:"+p.Attachment.ID), &visible, func(backdrop, panel n.Element) {
		w, h := c.Size()
		panelWidth := min(float32(840), w-48)
		panelHeight := float32(86)
		imageAvailable := p.Loading || p.Image != nil || p.LoadedImage != nil
		if isImage && imageAvailable {
			panelHeight = min(580, h-160) + 66
		}
		if p.NaturalHeight > 0 {
			panelHeight = p.NaturalHeight + 30
		}
		panelHeight = panel.AnimateTo("preview-height", panelHeight, motionAnimation(c, 180*time.Millisecond))
		panel.Width(panelWidth).Height(panelHeight).MaxHeight(h-64).Padding(14).Radius(10).Clip().Background(raised(t)).Border(1, t.Border).Label("附件预览")
		if !p.motionReady() {
			panel.Jump("preview-open", 0)
			p.measured()
		}
		target := float32(0)
		if p.open() {
			target = 1
		}
		animation := motionAnimation(c, 280*time.Millisecond)
		animation.Ease = composerDescentEase
		progress := panel.AnimateTo("preview-open", target, animation)
		if p.open() && progress >= 1 && p.LoadedImage != nil {
			p.Image = p.LoadedImage
			p.LoadedImage = nil
		}
		if !p.open() && progress <= 0 {
			p.presence = previewHidden
			if p.Thumbnail != nil {
				p.Image = p.Thumbnail
			}
			c.Services().Invalidate()
		}
		if isImage {
			// Image pixels stay opaque. Only their crop and geometry change.
			backdrop.Background(n.RGBA(0, 0, 0, .2))
			if morph && progress < 1 {
				panel.Opacity(0).IgnoreInput(true)
			}
		} else {
			backdrop.Background(n.RGBA(0, 0, 0, .2*progress))
			if reduce {
				backdrop.Background(n.RGBA(0, 0, 0, .2)).Transition(fadeMotion(c, 60*time.Millisecond))
			} else if p.Source.W > 0 && p.Source.H > 0 {
				scale := min(1, min(p.Source.W/panelWidth, p.Source.H/panelHeight))
				scale += (1 - scale) * progress
				x := (p.Source.X + p.Source.W/2 - w/2) * (1 - progress)
				y := (p.Source.Y + p.Source.H/2 - h/2) * (1 - progress)
				panel.Transform(n.Scaling(scale, scale).Then(n.Translation(x, y)))
			} else {
				panel.Opacity(progress)
			}
		}
		if !p.open() {
			panel.IgnoreInput(true)
		}
		panel.Children(func() {
			content := n.Column(c.Key("preview-content")).FillWidth().Shrink(0).Gap(12)
			invalidate := c.Services().Invalidate
			content.DrawOver(func(_ *n.Painter, r n.Rect) {
				if content.Valid() && p.NaturalHeight != r.H {
					p.NaturalHeight = r.H
					invalidate()
				}
			})
			content.Children(func() {
				n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
					n.Text(c, p.Attachment.Name).FontSize(13).Grow(1).SingleLine()
					if iconButton(c, queueRemoveIcon, "关闭附件预览").Size(24, 24).Clicked() {
						p.close()
					}
				})
				if isImage && imageAvailable {
					n.Column(c.Key("image-slot")).FillWidth().Height(min(580, h-160)).Children(func() {
						if !morph {
							if p.Image != nil {
								n.Image(c.Key("image"), p.Image).Fill().Fit(n.Contain).Label("图片预览内容")
							} else if p.LoadedImage != nil {
								n.Image(c.Key("image"), p.LoadedImage).Fill().Fit(n.Contain).Label("图片预览内容")
							} else {
								n.Text(c, "加载中…").FontSize(12).TextColor(t.TextMuted)
							}
						}
						if p.Error != "" {
							n.Text(c, p.Error).FontSize(12).TextColor(t.TextMuted)
						}
					})
				} else if p.Loading {
					n.Text(c, "加载中…").FontSize(12).TextColor(t.TextMuted)
				} else if p.Error != "" {
					n.Text(c, p.Error).FontSize(12)
				} else if p.Image != nil {
					n.Image(c, p.Image).FillWidth().Height(min(580, h-160)).Fit(n.Contain)
				} else {
					n.Scroll(c).FillWidth().MaxHeight(h - 160).Children(func() {
						n.Text(c, p.Text).Font(v.Model.Preferences.CodeFont).FontSize(12).FixedLineHeight(20).Selectable()
					})
				}
			})
		})
		if morph && p.Image != nil {
			destination := n.Rect{X: (w-panelWidth)/2 + 15, Y: (h-panelHeight)/2 + 51, W: panelWidth - 30, H: min(580, h-160)}
			clip, draw := previewImageGeometry(p.Source, destination, p.Image, progress)
			image := p.Image
			n.Overlay(c.Key("preview-image-layer"), func() {
				n.Column(c).Absolute().Left(clip.X).Top(clip.Y).Width(clip.W).Height(clip.H).Radius(5 * (1 - progress)).Clip().IgnoreInput(true).Role(n.RoleImage).Label("图片预览内容").Draw(func(painter *n.Painter, _ n.Rect) { painter.Image(image, draw, n.Contain) })
			})
		}
	})
	if dialog.Dismissed() {
		p.close()
		c.Services().Invalidate()
	}
	if reduce && !p.open() {
		p.presence = previewHidden
	}
}

func (v *View) ControllerDirectory() string {
	if v.Controller != nil {
		return v.Controller.Directory
	}
	return ""
}

func (v *View) attachmentPreparationKey(key string) string {
	edit := v.Model.QueueEdits[strings.TrimPrefix(key, "session:")]
	return key + "|" + edit.ID + "|" + edit.Token
}
func (v *View) preparingAttachments(key string) bool {
	return v.attachmentPreparing[v.attachmentPreparationKey(key)] > 0
}
