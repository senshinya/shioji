package ui

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestToolImageLoadsWithContinuousHeightAndUsesNativeZoom(t *testing.T) {
	m := layoutModel()
	a := domain.Attachment{ID: domain.NewID(), Name: "result.png", MIME: "image/png"}
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "render", Kind: "tool", Name: "生成图片", Presentation: "image", Final: true, Parts: []domain.ContentPart{{Kind: "image", Text: "result.png", Attachment: &a}}}}
	m.Details["s"] = d
	v := New(m, app.NewController(""), filepath.Join(t.TempDir(), "state.json"))
	defer v.Saver.Close()
	v.attachmentImages = map[string]*attachmentImage{a.ID: {phase: attachmentImageLoading}}
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("1 次工具调用")
	ui.Advance(300 * time.Millisecond)
	before, ok := ui.Find("预览图片：result.png")
	if !ok {
		t.Fatal("tool output has no image preview", ui.Texts())
	}
	ui.Click("消息输入")
	ui.Type("keep draft")
	v.attachmentImages[a.ID] = &attachmentImage{phase: attachmentImageReady, Image: n.NewBitmap(image.NewRGBA(image.Rect(0, 0, 100, 80)))}
	ui.Frame()
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("预览图片：result.png")
	ui.Advance(300 * time.Millisecond)
	after, _ := ui.Find("预览图片：result.png")
	if middle.H <= before.H || middle.H >= after.H {
		t.Fatal("image load jumped to its final height", before, middle, after)
	}
	if !ui.Focused("消息输入") || m.Drafts[m.DraftKey()] != "keep draft" {
		t.Fatal("image load changed focus or draft")
	}
	ui.Click("预览图片：result.png")
	ui.Advance(60 * time.Millisecond)
	zoom, ok := ui.Find("图片预览内容")
	if !ok || v.attachmentPreview.Source.W != after.W || zoom.W <= after.W {
		t.Fatal("output image bypassed source zoom", zoom, v.attachmentPreview)
	}
}
