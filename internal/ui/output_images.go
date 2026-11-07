package ui

import (
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"time"
)

func (v *View) outputImage(c *n.Context, t *n.Theme, a domain.Attachment) {
	cached := v.loadAttachmentImage(a, v.Model.WorkspaceID, 512)
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
	frame.Height(frame.AnimateTo("height", height, motionAnimation(c, 180*time.Millisecond)))
	var source n.Rect
	var button n.Element
	frame.Children(func() {
		button = n.ButtonBase(c).Fill().Padding(0).Radius(8).Label("预览图片：" + a.Name).Tooltip(a.Name).Disabled(cached == nil || cached.Image == nil)
		button.Children(func() {
			if cached != nil && cached.Image != nil {
				n.Image(c.Key("image"), cached.Image).Fill().Fit(n.Cover).Transition(fadeMotion(c, 160*time.Millisecond))
			} else {
				label := "图片加载中…"
				if cached != nil && cached.phase == attachmentImageFailed {
					label = "图片加载失败"
				}
				n.Text(c, label).FontSize(11).TextColor(t.TextMuted)
			}
		})
		source = button.Bounds()
	})
	if button.Clicked() {
		v.openAttachment(a, v.Model.WorkspaceID, source)
	}
	n.Row(c).FillWidth().MaxWidth(224).Gap(6).AlignItems(n.Baseline).Children(func() {
		n.Text(c, a.Name).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(11).TextColor(t.TextMuted)
		if cached != nil && cached.phase == attachmentImageFailed {
			if ghost(c, "重试图片").FontSize(11).Tooltip(cached.Error).Clicked() {
				delete(v.attachmentImages, a.ID)
			}
		}
	})
}
