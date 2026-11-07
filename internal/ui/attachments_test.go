package ui

import (
	n "github.com/egoist/mygo/ui"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestPreviewImageReplacementDoesNotFlashDuringMotion(t *testing.T) {
	red := func(size int) n.ImageSource {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			}
		}
		return n.NewBitmap(img)
	}
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "color", Name: "image.png", MIME: "image/png"}, presence: previewOpening, Loading: true, Source: n.Rect{X: 500, Y: 700, W: 36, H: 36}, Image: red(64)}
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	v.attachmentPreview.Image = red(256)
	v.attachmentPreview.Loading = false
	ui.Frame()
	for i := 0; i < 8; i++ {
		ui.Advance(16 * time.Millisecond)
		box, ok := ui.Find("图片预览内容")
		if !ok {
			box, _ = ui.Find("附件预览")
		}
		pixel := ui.Image().RGBAAt(int(box.X+box.W/2), int(box.Y+box.H/2))
		if pixel.R != 255 || pixel.G != 0 || pixel.B != 0 {
			t.Fatal("opaque image flashes while its thumbnail is replaced", i, pixel, box)
		}
	}
}

func TestAttachmentPreviewZoomsFromItsClickedSourceAndReturns(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 6
	a := domain.Attachment{ID: domain.NewID(), Name: "origin.png", MIME: "image/png", Size: 10, SHA256: strings.Repeat("a", 64)}
	m.Attachments["session:s"] = []app.DraftAttachment{{Attachment: a, WorkspaceID: "w", Ready: true}}
	v := New(m, app.NewController(""), filepath.Join(t.TempDir(), "client.json"))
	defer v.Saver.Close()
	v.attachmentImages = map[string]*attachmentImage{a.ID: {Image: n.NewBitmap(image.NewRGBA(image.Rect(0, 0, 60, 60)))}}
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("预览附件：origin.png")
	ui.Advance(60 * time.Millisecond)
	moving, _ := ui.Find("图片预览内容")
	ui.Advance(500 * time.Millisecond)
	settled, _ := ui.Find("图片预览内容")
	source := v.attachmentPreview.Source
	distance := func(box n.Rect) float64 {
		return math.Hypot(float64(box.X+box.W/2-source.X-source.W/2), float64(box.Y+box.H/2-source.Y-source.H/2))
	}
	if moving.W >= settled.W-20 || distance(moving) >= distance(settled) {
		t.Fatal("preview appears at the window center instead of zooming from its source", moving, settled)
	}
	ui.Click("关闭附件预览")
	ui.Advance(70 * time.Millisecond)
	closing, ok := ui.Find("图片预览内容")
	if !ok || closing.W >= settled.W-20 || distance(closing) >= distance(settled) {
		t.Fatal("preview disappears instead of returning to the source", closing, settled)
	}
	ui.Advance(500 * time.Millisecond)
	if _, ok := ui.Find("关闭附件预览"); ok {
		t.Fatal("closed preview remains interactive")
	}
}

func TestImagePreviewSurfaceGrowsAndShrinksWithItsImage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v := New(layoutModel(), nil, "")
		ui := n.NewTester(v.View, 1440, 900)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		bitmap := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 64; x++ {
				bitmap.SetRGBA(x, y, color.RGBA{R: 180, G: 30, B: 40, A: 255})
			}
		}
		v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "surface", Name: "image.png", MIME: "image/png"}, presence: previewOpening, Source: n.Rect{X: 500, Y: 700, W: 100, H: 50}, Image: n.NewBitmap(bitmap)}
		ui.Frame()
		ui.Advance(80 * time.Millisecond)
		moving, _ := ui.Find("附件预览")
		sample := func(r n.Rect) color.RGBA { return ui.Image().RGBAAt(int(r.X+r.W*.65), int(r.Y+8)) }
		entering := sample(moving)
		ui.Advance(500 * time.Millisecond)
		settled, _ := ui.Find("附件预览")
		surface := sample(settled)
		if moving.W >= settled.W-10 || moving.H >= settled.H-10 {
			t.Fatal("image moves while its window remains at final size", dark, moving, settled)
		}
		if entering != surface {
			t.Fatal("preview window only becomes visible at the end", dark, entering, surface)
		}
		v.attachmentPreview.close()
		ui.Frame()
		ui.Advance(80 * time.Millisecond)
		closing, ok := ui.Find("附件预览")
		if !ok || closing.W >= settled.W-10 || sample(closing) != surface {
			t.Fatal("preview window disappears instead of shrinking with image", dark, closing)
		}
	}
}

func TestReducedMotionImagePreviewDoesNotZoomOrFade(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Advance(time.Second)
	rest := ui.Image().RGBAAt(1300, 500)
	v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "reduced", Name: "image.png", MIME: "image/png"}, presence: previewOpening, Source: n.Rect{X: 500, Y: 700, W: 36, H: 36}, Image: n.NewBitmap(image.NewRGBA(image.Rect(0, 0, 60, 60)))}
	ui.Frame()
	ui.Advance(20 * time.Millisecond)
	entering := ui.Image().RGBAAt(1300, 500)
	panel, _ := ui.Find("附件预览")
	if panel.W != 840 {
		t.Fatal("Reduce Motion still zooms", panel)
	}
	ui.Advance(100 * time.Millisecond)
	open := ui.Image().RGBAAt(1300, 500)
	if entering == rest || entering != open {
		t.Fatal("reduced-motion image preview still fades")
	}
	v.attachmentPreview.close()
	ui.Frame()
	ui.Advance(20 * time.Millisecond)
	exiting := ui.Image().RGBAAt(1300, 500)
	ui.Advance(100 * time.Millisecond)
	closed := ui.Image().RGBAAt(1300, 500)
	if exiting != closed || closed != rest {
		t.Fatal("reduced-motion image close still fades")
	}
}

func TestClosingPreviewDuringItsEntranceContinuesFromTheCurrentBox(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "reverse", Name: "image.png", MIME: "image/png"}, presence: previewOpening, Source: n.Rect{X: 500, Y: 700, W: 36, H: 36}, Image: n.NewBitmap(image.NewRGBA(image.Rect(0, 0, 60, 60)))}
	ui.Frame()
	ui.Advance(70 * time.Millisecond)
	ui.Frame()
	before, _ := ui.Find("图片预览内容")
	v.attachmentPreview.close()
	ui.Frame()
	after, _ := ui.Find("图片预览内容")
	if math.Abs(float64(before.X-after.X))+math.Abs(float64(before.Y-after.Y))+math.Abs(float64(before.W-after.W)) > 1 {
		t.Fatal("interrupted preview zoom jumps", before, after)
	}
	ui.Advance(500 * time.Millisecond)
	if _, ok := ui.Find("附件预览"); ok {
		t.Fatal("interrupted preview remains mounted")
	}
}

func TestImageOnlyComposerSendsInsteadOfStopping(t *testing.T) {
	m := layoutModel()
	m.Snapshot.Sessions[0].Status = "running"
	m.WorkerMinor = 6
	m.Drafts["session:s"] = ""
	a := domain.Attachment{ID: domain.NewID(), Name: "screenshot.png", MIME: "image/png", Size: 42, SHA256: strings.Repeat("a", 64)}
	m.Attachments["session:s"] = []app.DraftAttachment{{Attachment: a, WorkspaceID: m.WorkspaceID, Ready: true}}
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1440, 900)
	if _, ok := ui.Find("停止当前轮"); ok {
		t.Fatal("image-only draft became stop")
	}
	if _, ok := ui.Find("移除附件：screenshot.png"); !ok {
		t.Fatal("attachment preview missing")
	}
	if err := ui.Click("加入队列"); err != nil {
		t.Fatal(err)
	}
	found := false
	for len(c.Jobs) > 0 {
		j := <-c.Jobs
		if j.Method == "turn.enqueue" {
			in := j.Input.(domain.Submission)
			found = len(in.Attachments) == 1 && in.Attachments[0] == a
		}
	}
	if !found {
		t.Fatal("attachment not routed to queue")
	}
}

func TestLateAttachmentPreparationDoesNotEnterRestoredOrdinaryDraft(t *testing.T) {
	m := layoutModel()
	m.QueueEdits["s"] = app.QueueEdit{ID: "queued", Displaced: "ordinary"}
	m.Drafts["session:s"] = "queued edit"
	c := app.NewController("")
	v := New(m, c, filepath.Join(t.TempDir(), "client.json"))
	defer v.Saver.Close()
	file := filepath.Join(t.TempDir(), "sample.txt")
	os.WriteFile(file, []byte("attachment"), 0600)
	v.addAttachmentFiles("session:s", []string{file})
	m.CancelQueueEdit("s")
	select {
	case update := <-c.Updates:
		update(m)
	case <-time.After(3 * time.Second):
		t.Fatal("file preparation did not finish")
	}
	if len(m.Attachments["session:s"]) != 0 || m.Drafts["session:s"] != "ordinary" {
		t.Fatal("late attachment leaked into displaced ordinary draft")
	}
}

func TestRemovingAttachmentAnimatesComposerHeight(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 6
	a := domain.Attachment{ID: domain.NewID(), Name: "sample.go", MIME: "text/plain", Size: 10, SHA256: strings.Repeat("a", 64)}
	m.Attachments["session:s"] = []app.DraftAttachment{{Attachment: a, WorkspaceID: m.WorkspaceID, Ready: true}}
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	before, _ := ui.Find("消息编辑器")
	if err := ui.Click("移除附件：sample.go"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(80 * time.Millisecond)
	mid, _ := ui.Find("消息编辑器")
	ui.Advance(time.Second)
	after, _ := ui.Find("消息编辑器")
	if !(before.H > mid.H && mid.H > after.H) {
		t.Fatal("attachment height jumped", before.H, mid.H, after.H)
	}
}

func TestRemovingFirstUnreadyAttachmentDoesNotReindexUpload(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 6
	for _, name := range []string{"first.txt", "second.txt"} {
		m.Attachments["session:s"] = append(m.Attachments["session:s"], app.DraftAttachment{Attachment: domain.Attachment{ID: domain.NewID(), Name: name, MIME: "text/plain", Size: 1, SHA256: strings.Repeat("a", 64)}, WorkspaceID: "w", Ready: true})
	}
	v := New(m, app.NewController(t.TempDir()), t.TempDir()+"/client.json")
	defer v.Saver.Close()
	ui := n.NewTester(v.View, 1440, 900)
	for i := range m.Attachments["session:s"] {
		m.Attachments["session:s"][i].Ready = false
	}
	if err := ui.Click("移除附件：first.txt"); err != nil {
		t.Fatal(err)
	}
	if len(m.Attachments["session:s"]) != 1 || m.Attachments["session:s"][0].Attachment.Name != "second.txt" {
		t.Fatal("wrong item removed")
	}
}

func TestPreparingAttachmentCannotSubmitIncompleteMessage(t *testing.T) {
	m := layoutModel()
	m.Drafts["session:s"] = "include selected file"
	c := app.NewController("")
	v := New(m, c, "")
	v.attachmentPreparing = map[string]int{v.attachmentPreparationKey(m.DraftKey()): 1}
	v.sendMessage(false)
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "session.submit" || job.Method == "turn.enqueue" {
			t.Fatal("message sent before file preparation completed")
		}
	}
	if m.Drafts["session:s"] != "include selected file" {
		t.Fatal("draft lost")
	}
}

func TestThumbnailQueueStaysBoundedWhileEarlierImagesLoad(t *testing.T) {
	v := &View{Model: layoutModel(), Controller: app.NewController(t.TempDir()), StatePath: filepath.Join(t.TempDir(), "client.json"), attachmentImages: map[string]*attachmentImage{}}
	for i := 0; i < 32; i++ {
		v.attachmentImages[domain.NewID()] = &attachmentImage{phase: attachmentImageLoading}
	}
	a := domain.Attachment{ID: domain.NewID(), Name: "image.png", MIME: "image/png", Size: 1, SHA256: strings.Repeat("a", 64)}
	n.NewTester(func(c *n.Context) { v.attachmentThumbnail(c, c.Theme(), a, "w", 36) }, 200, 100)
	if len(v.attachmentImages) > 32 {
		t.Fatal("thumbnail jobs exceeded cache bound", len(v.attachmentImages))
	}
}
