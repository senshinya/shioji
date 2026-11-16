package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/domain"
)

// Markdown image descriptions must reach the same owning-worker preview as
// ordinary file links, including historical replies and the streaming path.
func TestMarkdownImagesOpenOwningWorkerPreview(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, inline := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/inline=%v", dark, inline), func(t *testing.T) {
				v, ui, root := markdownWorkerPreview(t, "# 图片测试")
				ui.SetDark(dark)
				ui.Frame()
				if err := ui.Click("关闭附件预览"); err != nil {
					t.Fatal(err)
				}
				ui.Frame()
				ui.Advance(time.Second)
				if v.attachmentPreview.visible() {
					t.Fatal("fixture document is still open")
				}
				path := filepath.Join(root, "docs", "comparison.png")
				im := image.NewRGBA(image.Rect(0, 0, 96, 48))
				for y := range 48 {
					for x := range 96 {
						im.SetRGBA(x, y, color.RGBA{R: 60, G: 110, B: 180, A: 255})
					}
				}
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, im)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				text := "![流程图亮暗对照](docs/comparison.png)"
				if inline {
					text = "中文与 English：" + text + "，查看结果。"
				}
				d := v.Model.Details[v.Model.SessionID]
				d.Items = []domain.Item{{ID: "image-reply", Kind: "assistant", Text: text, Final: true}}
				v.Model.Details[v.Model.SessionID] = d
				ui.Frame()
				ui.Advance(time.Second)
				label := "预览图片：流程图亮暗对照"
				waitNative(t, ui, func() bool {
					r, ok := ui.Find(label)
					if !ok || r.W == 0 || r.H < 80 {
						return false
					}
					pixel := ui.Image().RGBAAt(int(r.X+r.W/2), int(r.Y+r.H/2))
					return pixel.R == 60 && pixel.G == 110 && pixel.B == 180
				})
				if v.attachmentPreview.visible() {
					t.Fatal("loading a Markdown thumbnail opened a viewer without a click")
				}
				r, ok := ui.Find(label)
				if !ok {
					t.Fatal("missing Markdown image description", ui.Texts())
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("image-entry-dark-%v-inline-%v.png", dark, inline))
				ui.ClickAt(r.X+5, r.Y+r.H/2)
				waitNative(t, ui, func() bool {
					return v.attachmentPreview != nil && v.attachmentPreview.SourcePath == path || v.fileLink != nil && !v.fileLink.loading
				})
				p := v.attachmentPreview
				if p == nil || p.LoadedImage == nil || p.SourcePath != path || p.SourceSessionID != d.Session.ID {
					t.Fatalf("image did not reach native preview through worker: preview=%+v link=%+v", p, v.fileLink)
				}
				if p.Source.W <= 0 || p.Image == nil || p.Thumbnail == nil || p.Origin == nil {
					t.Fatal("Markdown image did not inherit the tool thumbnail zoom origin", p)
				}
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("image-preview-dark-%v-inline-%v.png", dark, inline))
			})
		}
	}
}

func TestMarkdownImageRetryAndSessionLifetime(t *testing.T) {
	v, ui, root := markdownWorkerPreview(t, "# 图片重试")
	t.Cleanup(func() {
		if t.Failed() && v.markdownImages != nil {
			for key, entry := range v.markdownImages.entries {
				t.Log(key, entry.thumbnail.Error)
			}
		}
	})
	ui.Click("关闭附件预览")
	ui.Frame()
	ui.Advance(time.Second)
	d := v.Model.Details[v.Model.SessionID]
	d.Items = []domain.Item{{ID: "missing-image", Kind: "assistant", Text: "![尚未写入的图片](docs/later.png)", Final: true}}
	v.Model.Details[v.Model.SessionID] = d
	ui.Frame()
	waitNative(t, ui, func() bool { return ui.HasText("图片加载失败") })
	if !ui.HasText("重试图片") {
		t.Fatal("failed image offers no recovery", ui.Texts())
	}
	f, err := os.Create(filepath.Join(root, "docs", "later.png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 96, 48)))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if err = ui.Click("重试图片"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Click("消息输入")
	ui.Type("保留普通草稿")
	waitNative(t, ui, func() bool {
		entry := v.markdownImages.entries["docs/later.png"]
		return entry != nil && entry.preview != nil
	})
	if ui.HasText("图片加载失败") || !ui.Focused("消息输入") || v.Model.Drafts[v.Model.DraftKey()] != "保留普通草稿" {
		t.Fatal("image retry failed or stole the ordinary draft/focus", ui.Texts())
	}
	cache := v.markdownImages
	v.Model.SessionID = ""
	ui.Frame()
	if cache.ctx.Err() == nil || v.markdownImages == cache || len(cache.entries) != 0 {
		t.Fatal("session switch retained the old image transfer scope")
	}
	cache = v.markdownImages
	v.CloseTransient()
	if cache.ctx.Err() == nil || v.markdownImages != nil {
		t.Fatal("window close retained image transfers and thumbnails")
	}
}

func TestMarkdownImagePendingDestinationCannotOpen(t *testing.T) {
	for _, prefix := range []string{"", "行内："} {
		t.Run(prefix, func(t *testing.T) {
			var opened []string
			options := markdown.Options{OnLink: func(dest string) { opened = append(opened, dest) }, Reveal: &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}}
			source := prefix + "![还未完成](/device/comparison"
			ui := n.NewTester(func(c *n.Context) {
				markdown.Stream(c, source, &options).FillWidth()
			}, 640, 240)
			ui.Advance(time.Second)
			label := "还未完成"
			if prefix != "" {
				label = "[还未完成]"
			}
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("missing streamed image", ui.Texts())
			}
			ui.ClickAt(r.X+5, r.Y+r.H/2)
			if len(opened) != 0 {
				t.Fatal("incomplete streamed image opened a partial destination", opened)
			}
			source += ".png)"
			ui.Frame()
			ui.Advance(time.Second)
			r, ok = ui.Find(label)
			if !ok {
				t.Fatal("completed image disappeared")
			}
			ui.ClickAt(r.X+5, r.Y+r.H/2)
			if len(opened) != 1 || opened[0] != "/device/comparison.png" {
				t.Fatal("completed streaming image did not become actionable", opened)
			}
		})
	}
}

func TestMarkdownLoadedImageKeepsItsSizeAndOpens(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 640, 320))
	bitmap := n.NewBitmap(im)
	var opened string
	options := markdown.Options{
		LoadImage: func(string) (n.ImageSource, error) { return bitmap, nil },
		OnLink:    func(dest string) { opened = dest },
	}
	ui := n.NewTester(func(c *n.Context) {
		markdown.View(c, "![已加载图片](comparison.png)", &options).FillWidth()
	}, 320, 240)
	waitNative(t, ui, func() bool {
		r, ok := ui.Find("已加载图片")
		return ok && r.H > 100
	})
	r, _ := ui.Find("已加载图片")
	if r.W > 320 || math.Abs(float64(r.W-2*r.H)) > 1 {
		t.Fatal("interactive wrapper changed the loaded image aspect ratio or exceeded the view", r)
	}
	ui.ClickAt(r.X+5, r.Y+r.H/2)
	if opened != "comparison.png" {
		t.Fatal("loaded image lost its destination", opened)
	}
}

func TestMarkdownImageCacheDoesNotChurnVisibleImages(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.bindMarkdownImages(&v.md, v.Model.Details["s"].Session)
	defer v.closeMarkdownImages()
	v.replyFrame = 10
	original := map[string]*markdownImageEntry{}
	for i := 0; i < 32; i++ {
		path := fmt.Sprintf("%d.png", i)
		original[path] = v.requestMarkdownImage(v.markdownImages, v.Model.Details["s"].Session, path)
	}
	for i := 32; i < 40; i++ {
		v.requestMarkdownImage(v.markdownImages, v.Model.Details["s"].Session, fmt.Sprintf("%d.png", i))
	}
	for path, entry := range original {
		if v.markdownImages.entries[path] != entry || len(v.markdownImages.entries) > 32 {
			t.Fatal("a large visible gallery churns resources instead of retaining its bounded thumbnails")
		}
	}
	v.replyFrame += 2
	v.requestMarkdownImage(v.markdownImages, v.Model.Details["s"].Session, "later.png")
	if v.markdownImages.entries["later.png"] == nil {
		t.Fatal("offscreen resources cannot be evicted for later images")
	}
}

func TestMarkdownImageTransfersStopWhenSecondaryPaneCloses(t *testing.T) {
	root := New(layoutModel(), nil, "")
	child := New(layoutModel(), nil, "")
	root.secondary, child.parent = child, root
	child.bindMarkdownImages(&child.md, child.Model.Details["s"].Session)
	cache := child.markdownImages
	root.Model.SecondarySessionID = ""
	root.prepareSecondary()
	if cache.ctx.Err() == nil || child.markdownImages != nil {
		t.Fatal("detached secondary pane retained image transfers")
	}
}

func TestMarkdownImageQueueWaitDoesNotConsumeTransferTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		directory := t.TempDir()
		controller := app.NewController(directory)
		v := New(layoutModel(), controller, filepath.Join(directory, "client.json"))
		defer v.Saver.Close()
		defer v.CloseTransient()
		source := v.Model.Details["s"].Session
		v.bindMarkdownImages(&v.md, source)
		cache := v.markdownImages
		cache.slots <- struct{}{}
		cache.slots <- struct{}{}
		entry := v.requestMarkdownImage(cache, source, "queued.png")
		// synctest advances virtual time while the two transfer slots are held.
		time.Sleep(2 * time.Minute)
		<-cache.slots
		synctest.Wait()
		for len(controller.Updates) > 0 {
			(<-controller.Updates)(v.Model)
		}
		if entry.thumbnail.phase != attachmentImageFailed || entry.thumbnail.Error == "" {
			t.Fatal("queued image never reached a retryable result after a slot opened", entry.thumbnail.phase)
		}
	})
}
