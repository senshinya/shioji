package ui

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestAttachmentPreviewCommandWheelZoomsOnlyImage(t *testing.T) {
	v, ui := imageControlFixture(t, true)
	panel, _ := ui.Find("附件预览")
	pixels, _ := ui.Find("图片预览内容")
	x, y := pixels.X+pixels.W*.15, pixels.Y+pixels.H*.5
	redAt := func() bool {
		p := ui.Image().RGBAAt(int(x), int(y))
		return p.R > 200 && p.G < 40 && p.B < 40
	}
	if !redAt() {
		t.Fatal("expected the left half of the fitted image")
	}
	// Use the toolbar too: the scope is the whole preview panel.
	ui.Scroll(panel.X+panel.W/2, panel.Y+22, 0, 160, n.Cmd)
	if got := v.Model.Preferences.InterfaceZoom; got != 1.25 {
		t.Fatalf("preview wheel changed the interface zoom to %g", got)
	}
	if after, _ := ui.Find("附件预览"); after != panel || redAt() {
		t.Fatal("zoom out did not shrink the image inside the fixed panel", after, panel)
	}
	ui.Scroll(x, y, 0, -160, n.Cmd)
	if !redAt() {
		t.Fatal("reverse scroll did not restore the fitted image")
	}
	ui.Click("关闭附件预览")
	ui.Advance(time.Second)
	ui.Wheel(x, y, 0, -40, n.Cmd)
	if v.Model.Preferences.InterfaceZoom <= 1.25 {
		t.Fatal("closing the preview did not restore interface zoom")
	}
}

func TestAttachmentPreviewRotatesImageInBothPresentations(t *testing.T) {
	for _, morph := range []bool{true, false} {
		_, ui := imageControlFixture(t, morph)
		panel, _ := ui.Find("附件预览")
		img, _ := ui.Find("图片预览内容")
		ui.Click("逆时针旋转 90°")
		if after, _ := ui.Find("附件预览"); after != panel {
			t.Fatal("rotation moved the panel", after, panel)
		}
		raster := ui.Image()
		x := int(img.X + img.W/2)
		cy := img.Y + img.H/2
		if top, bottom := raster.RGBAAt(x, int(cy-60)), raster.RGBAAt(x, int(cy+60)); top.B < 200 || bottom.R < 200 {
			t.Fatalf("rotation did not move the blue right half above the red left half (morph=%v): %v, %v", morph, top, bottom)
		}
	}
}

func imageControlFixture(t *testing.T, morph bool) (*View, *n.Tester) {
	t.Helper()
	m := layoutModel()
	m.Preferences.InterfaceZoom = 1.25
	m.Preferences.ReduceMotion = !morph
	v := New(m, nil, "")
	bitmap := image.NewRGBA(image.Rect(0, 0, 240, 120))
	draw.Draw(bitmap, image.Rect(0, 0, 120, 120), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	draw.Draw(bitmap, image.Rect(120, 0, 240, 120), image.NewUniform(color.RGBA{B: 255, A: 255}), image.Point{}, draw.Src)
	v.attachmentPreview = &attachmentPreview{
		Attachment: domain.Attachment{ID: "preview-controls", Name: "image.png", MIME: "image/png"},
		presence:   previewOpening,
		Source:     n.Rect{X: 600, Y: 560, W: 60, H: 38},
		Image:      n.NewBitmap(bitmap),
	}
	ui := n.NewTester(v.View, 1240, 900)
	ui.Advance(time.Second)
	if _, ok := ui.Find("图片预览内容"); !ok {
		t.Fatal("fixture did not open the image preview")
	}
	return v, ui
}
