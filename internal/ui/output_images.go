package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"time"
)

func (v *View) outputImage(c *n.Context, t *n.Theme, a domain.Attachment) float32 {
	cached := v.loadAttachmentImage(a, v.Model.WorkspaceID, 512)
	return v.outputImageCard(c, t, a, cached, func(source n.Rect) {
		v.openAttachment(a, v.Model.WorkspaceID, source, attachmentPreviewOrigin{Radius: 8})
	}, func() { delete(v.attachmentImages, a.ID) })
}

// Tool results and Markdown use the same thumbnail geometry and transitions.
// Their resource ownership and loading remain with their respective sources.
func (v *View) outputImageCard(c *n.Context, t *n.Theme, a domain.Attachment, cached *attachmentImage, open func(n.Rect), retry func()) float32 {
	height := float32(80)
	if cached != nil {
		cached.Used = v.replyFrame
	}
	if cached != nil && cached.Image != nil {
		w, h := n.ImageSize(cached.Image)
		if w > 0 {
			height = min(180, max(80, 224*h/w))
		}
	}
	frame := n.Column(c.Key("output-image:" + a.ID)).FillWidth().MaxWidth(224).Clip().Radius(8).Background(codeSurface(t))
	shown := frame.AnimateTo("height", height, motionAnimation(c, 180*time.Millisecond))
	frame.Height(shown)
	var source n.Rect
	var button n.Element
	frame.Children(func() {
		button = n.ButtonBase(c).Fill().Padding(0).Radius(8).Label("预览图片：" + a.Name).Tooltip(a.Name).Disabled(cached == nil || cached.Image == nil)
		button.Children(func() {
			if cached != nil && cached.Image != nil {
				picture := n.Image(c.Key("image"), cached.Image).Fill().Fit(n.Cover).Transition(fadeMotion(c, 160*time.Millisecond))
				if v.previewOwnsThumbnail(c, a.ID, button.Bounds()) {
					picture.Opacity(0)
				}
			} else {
				label := "图片加载中…"
				if cached != nil && cached.phase == attachmentImageFailed {
					label = "图片加载失败"
				}
				n.Text(c.Key("placeholder"), label).FontSize(11).TextColor(t.TextMuted).Transition(fadeMotion(c, 160*time.Millisecond)).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
			}
		})
		source = button.Bounds()
	})
	if button.Clicked() {
		open(source)
	}
	n.Row(c.Key("caption")).FillWidth().MaxWidth(224).Gap(6).AlignItems(n.Baseline).Children(func() {
		n.Text(c, a.Name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(11).TextColor(t.TextMuted)
		if cached != nil && cached.phase == attachmentImageFailed {
			if ghost(c.Key("retry"), "重试图片").Transition(fadeMotion(c, 160*time.Millisecond)).FontSize(11).Tooltip(cached.Error).Clicked() {
				retry()
			}
		}
	})
	if previous := frame.LayoutBounds(); previous.W > 0 {
		return shown - previous.H
	}
	return 0
}
