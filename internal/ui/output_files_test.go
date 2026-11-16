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

func TestToolFileOpensNativeReadOnlyPreviewFromItsOwnRow(t *testing.T) {
	m := layoutModel()
	a := domain.Attachment{ID: domain.NewID(), Name: "hello.go", MIME: "text/plain", Size: 14}
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "file", Kind: "tool", Presentation: "mcp", Name: "生成示例文件", Final: true, Parts: []domain.ContentPart{{Kind: "file", Attachment: &a}}}}
	m.Details["s"] = d
	v := New(m, app.NewController(""), filepath.Join(t.TempDir(), "state.json"))
	defer v.Saver.Close()
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("1 次工具调用")
	ui.Advance(300 * time.Millisecond)
	ui.Click("生成示例文件")
	ui.Advance(300 * time.Millisecond)
	row, ok := ui.Find("预览文件：hello.go")
	if !ok {
		t.Fatal("file result has no preview entry", ui.Texts())
	}
	ui.Click("预览文件：hello.go")
	if v.attachmentPreview == nil || v.attachmentPreview.Source.W != row.W {
		t.Fatal("preview lost origin")
	}
	// Replace only the asynchronous download boundary; the native view is real.
	loaded := *v.attachmentPreview
	loaded.Loading, loaded.Text, loaded.Error = false, "package hello\n", ""
	v.attachmentPreview = &loaded
	ui.Advance(500 * time.Millisecond)
	if box, ok := ui.Find("文件内容：hello.go"); !ok || box.H > 200 {
		t.Fatal("missing native text/code reader", ui.Texts())
	}
	if _, ok := ui.Find("另存到本机"); !ok {
		t.Fatal("missing native save action")
	}
	ui.Click("文件内容：hello.go")
	ui.Type("modified")
	if !loaded.Reader.ReadOnly || loaded.Reader.Buffer.String() != "package hello\n" {
		t.Fatal("preview allowed editing")
	}
}

func TestImageSaveFailureRemainsVisibleAboveTheImage(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "image", Name: "image.png", MIME: "image/png"}, presence: previewOpening, Source: n.Rect{X: 600, Y: 600, W: 100, H: 200}, Image: n.NewBitmap(image.NewRGBA(image.Rect(0, 0, 100, 200))), SaveError: "无法保存文件"}
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	preview, ok := ui.Find("图片预览内容")
	message, found := ui.Find("无法保存文件")
	if !ok || !found || preview.Y < message.Y+message.H {
		t.Fatal("save failure is covered by the image", preview, message)
	}
}

func TestFilePreviewBlendsItsCardContentsBeforeTheLastFrame(t *testing.T) {
	m := layoutModel()
	a := domain.Attachment{ID: domain.NewID(), Name: "result.go", MIME: "text/plain", Size: 301}
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "file", Kind: "tool", Presentation: "mcp", Name: "生成文件", Final: true, Parts: []domain.ContentPart{{Kind: "file", Attachment: &a}}}}
	m.Details["s"] = d
	v := New(m, app.NewController(""), filepath.Join(t.TempDir(), "state.json"))
	defer v.Saver.Close()
	ui := n.NewTester(v.View, 1240, 820)
	ui.Click("1 次工具调用")
	ui.Advance(300 * time.Millisecond)
	ui.Click("生成文件")
	ui.Advance(300 * time.Millisecond)
	ui.Click("预览文件：result.go")
	cardCopies := func() int {
		count := 0
		for _, text := range ui.Texts() {
			if text == "301 B" {
				count++
			}
		}
		return count
	}
	loaded := *v.attachmentPreview
	loaded.Loading, loaded.Text, loaded.Error = false, "package result\n", ""
	v.attachmentPreview = &loaded
	ui.Frame()
	if cardCopies() < 2 {
		t.Fatal("preview loses the compact card contents at its first frame", ui.Texts())
	}
	ink := func() int {
		box, ok := ui.Find("预览缩略内容")
		if !ok {
			return 0
		}
		bitmap := ui.Image()
		bg := bitmap.RGBAAt(int(box.X+box.W-8), int(box.Y+box.H-8))
		energy := 0
		for y := int(box.Y + 6); y < int(box.Y+30); y++ {
			for x := int(box.X + box.W - 52); x < int(box.X+box.W-10); x++ {
				pixel := bitmap.RGBAAt(x, y)
				delta := int(bg.R) - int(pixel.R)
				if delta < 0 {
					delta = -delta
				}
				energy += delta
			}
		}
		return energy
	}
	initialInk := ink()
	ui.Advance(35 * time.Millisecond)
	if cardCopies() < 2 {
		t.Fatal("card contents disappear before the styles can blend")
	}
	if fading := ink(); initialInk < 100 || fading <= 0 || fading >= initialInk {
		t.Fatal("compact metadata does not progressively blend into preview", initialInk, fading)
	}
	ui.Advance(500 * time.Millisecond)
	loaded.close()
	ui.Frame()
	ui.Advance(230 * time.Millisecond)
	if cardCopies() < 2 {
		t.Fatal("compact card contents wait until after the close animation")
	}
	if restored := ink(); restored < initialInk/2 {
		t.Fatal("compact metadata has not returned during the close animation", initialInk, restored)
	}
}
