package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/codex"
	"shioji/internal/store"
	"shioji/internal/worker"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestMarkdownPreviewRendersAndKeepsBothReadingPositions(t *testing.T) {
	v := New(layoutModel(), nil, "")
	text := "# 阅读设备上的文档\n\n**原生渲染**，保留文件内容。\n\n"
	for i := range 60 {
		text += fmt.Sprintf("段落 %d：中文与 English 的阅读位置。\n\n", i)
	}
	v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "markdown", Name: "README.MD", MIME: "text/plain"}, presence: previewOpening, Text: text}
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if !ui.HasText("阅读设备上的文档") || ui.HasText("# 阅读设备上的文档") {
		t.Fatal("Markdown opens as source instead of formatted native content", ui.Texts())
	}
	reader, ok := ui.Find("Markdown 预览")
	if !ok {
		t.Fatal("missing Markdown scrollport")
	}
	ui.Scroll(reader.X+reader.W/2, reader.Y+reader.H/2, 0, 180)
	ui.Advance(300 * time.Millisecond)
	before, ok := ui.Find("段落 20：中文与 English 的阅读位置。")
	if !ok {
		t.Fatal("missing reading anchor")
	}
	if err := ui.Click("源码"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(300 * time.Millisecond)
	if v.attachmentPreview.Reader == nil || !v.attachmentPreview.Reader.ReadOnly {
		t.Fatal("source must remain a native read-only editor")
	}
	source, ok := ui.Find("文件内容：README.MD")
	if !ok {
		t.Fatal("missing source editor")
	}
	ui.Scroll(source.X+source.W/2, source.Y+source.H/2, 0, 240)
	ui.Advance(300 * time.Millisecond)
	sourceY := v.attachmentPreview.Reader.ScrollY()
	if sourceY < 50 {
		t.Fatal("source did not scroll", sourceY)
	}
	ui.Click("预览")
	ui.Advance(300 * time.Millisecond)
	after, ok := ui.Find("段落 20：中文与 English 的阅读位置。")
	if !ok || math.Abs(float64(before.Y-after.Y)) > 1 {
		t.Fatal("switching loses Markdown reading position", before, after)
	}
	ui.Click("源码")
	ui.Advance(300 * time.Millisecond)
	if math.Abs(v.attachmentPreview.Reader.ScrollY()-sourceY) > 1 {
		t.Fatal("switching loses source position")
	}
	ui.Type("不可修改")
	if v.attachmentPreview.Text != text {
		t.Fatal("source mode edited the file")
	}
}

func markdownWorkerPreview(t *testing.T, text string) (*View, *n.Tester, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "sh-md-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	root := filepath.Join(dir, "project")
	if err = os.MkdirAll(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "docs", "guide.md")
	if err = os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := db.CreateWorkspace("Markdown", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.Accept(domain.Submission{OperationID: domain.NewID(), WorkspaceID: w.ID, Text: "读取文档", Config: domain.RunConfig{Model: "test", Permission: "read-only"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetOperation(op.ID, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
	_, err = db.Apply(op.SessionID, domain.Event{Type: "turn", Turn: &domain.Turn{ID: "turn", Status: "completed"}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := db.Session(op.SessionID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := worker.New(ctx, worker.Config{Directory: dir, Harnesses: []harness.Adapter{codex.Adapter{Home: filepath.Join(dir, "codex"), Executable: "/usr/bin/false"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	go server.Serve()
	m := app.NewModel()
	m.FirstRun = false
	m.LocalDeviceID = snapshot.DeviceID
	m.KnownDeviceID = snapshot.DeviceID
	m.ApplySnapshot(snapshot)
	m.WorkspaceID = w.ID
	m.SessionID = op.SessionID
	m.Connected = true
	m.ConnectionEpoch = "markdown-epoch"
	m.Details[op.SessionID] = detail
	v := New(m, app.NewController(dir), filepath.Join(dir, "client.json"))
	t.Cleanup(func() { v.CloseTransient(); v.Saver.Close() })
	var services n.Services
	ui := n.NewTester(func(c *n.Context) { services = c.Services(); v.View(c) }, 1240, 820)
	ui.Advance(time.Second)
	v.startFileLink(detail.Session, path, services)
	waitNative(t, ui, func() bool { return v.attachmentPreview != nil || v.fileLink != nil && !v.fileLink.loading })
	if v.attachmentPreview == nil {
		t.Fatalf("could not preview Markdown: %+v", v.fileLink)
	}
	ui.Advance(time.Second)
	return v, ui, root
}

func TestMarkdownPreviewResolvesLinksOnTheDocumentsOwningDevice(t *testing.T) {
	v, ui, root := markdownWorkerPreview(t, "# 文档\n\n[相对文件](../next.md)\n\n[外部链接](https://example.com)\n\n[段落](#段落)\n\n## 段落\n\n内容\n")
	link, ok := ui.Find("外部链接")
	if !ok {
		t.Fatal("missing external link")
	}
	ui.ClickAt(link.X+5, link.Y+link.H/2)
	if got := ui.OpenedURLs(); len(got) != 1 || got[0] != "https://example.com" {
		t.Fatal("document link lost external handling", got)
	}
	link, ok = ui.Find("相对文件")
	if !ok {
		t.Fatal("missing relative link")
	}
	ui.ClickAt(link.X+5, link.Y+link.H/2)
	if v.fileLink == nil || v.fileLink.path != filepath.Join(root, "next.md") {
		t.Fatal("relative link did not resolve against document path on its owner", v.fileLink)
	}
	if v.fileLink.session != v.Model.SessionID {
		t.Fatal("link escaped owning session")
	}
}

func TestMarkdownPreviewLoadsLocalImagesThroughWorkerAndCancels(t *testing.T) {
	v, ui, root := markdownWorkerPreview(t, "# 图片\n\n![本地图](pic.png)\n\n![外部图](https://example.invalid/no-network.png)")
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	f, err := os.Create(filepath.Join(root, "docs", "pic.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loader := v.attachmentPreview.Markdown.options.LoadImage
	if loader == nil {
		t.Fatal("Markdown has no owning-worker image loader")
	}
	src, err := loader("pic.png")
	if err != nil || src == nil {
		t.Fatal("relative image failed through worker", err)
	}
	if src, err = loader("https://example.invalid/no-network.png"); err == nil || src != nil {
		t.Fatal("external image was automatically fetched")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	outsideDir, err := os.MkdirTemp(home, ".shioji-md-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(outsideDir) })
	outside := filepath.Join(outsideDir, "outside.png")
	f, err = os.Create(outside)
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, img)
	f.Close()
	if src, err = loader(outside); err == nil || src != nil {
		t.Fatal("image loader bypassed worker accepted directories")
	}
	ui.Click("关闭附件预览")
	ui.Advance(time.Second)
	if src, err = loader("pic.png"); err == nil || src != nil {
		t.Fatal("closed preview can still load files")
	}
}

func TestMarkdownPreviewEmptyFileTravelsThroughExistingFileRoute(t *testing.T) {
	_, ui, _ := markdownWorkerPreview(t, "")
	if !ui.HasText("文件为空") {
		t.Fatal("empty device Markdown did not open natively", ui.Texts())
	}
}

func TestMarkdownPreviewRetainsManualPositionAfterAnchorNavigation(t *testing.T) {
	v := New(layoutModel(), nil, "")
	text := "# 起点\n\n[跳转](#目标)\n\n" + strings.Repeat("段落内容。\n\n", 45) + "## 目标\n\n" + strings.Repeat("结尾内容。\n\n", 30)
	v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "anchors", Name: "anchors.md", MIME: "text/plain"}, presence: previewOpening, Text: text}
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	m := v.attachmentPreview.Markdown
	m.options.OnLink("#目标")
	ui.Frame()
	ui.Advance(time.Second)
	if m.scroll.Y < 500 {
		t.Fatal("anchor did not navigate within preview", m.scroll.Y)
	}
	r, _ := ui.Find("Markdown 预览")
	ui.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, 180)
	ui.Advance(time.Second)
	want := m.scroll.Y
	ui.Click("源码")
	ui.Advance(300 * time.Millisecond)
	ui.Click("预览")
	ui.Advance(time.Second)
	if math.Abs(float64(m.scroll.Y-want)) > 1 {
		t.Fatal("source switch jumped back to the old anchor", want, m.scroll.Y)
	}
}

func TestMarkdownPreviewCancelsResourcesOnConnectionHandoff(t *testing.T) {
	v, ui, _ := markdownWorkerPreview(t, "# 切换连接\n")
	loader := v.attachmentPreview.Markdown.options.LoadImage
	if loader == nil {
		t.Fatal("missing loader")
	}
	v.Model.ConnectionEpoch = "new-connection"
	ui.Frame()
	if _, ok := ui.Find("附件预览"); ok {
		t.Fatal("stale document remained visible")
	}
	if _, err := loader("anything.png"); err == nil {
		t.Fatal("previous connection can still load document images")
	}
}

func TestMarkdownPreviewEmptyAndOtherFiles(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		markdown   bool
	}{
		{"empty.markdown", "", true},
		{"readme.md", "   \n", true},
		{"main.go", "package main\n", false},
		{"notes.txt", "# literal text", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			v.attachmentPreview = &attachmentPreview{Attachment: domain.Attachment{ID: "empty", Name: tc.name, MIME: "text/plain"}, presence: previewOpening, Text: tc.text}
			ui := n.NewTester(v.View, 900, 720)
			ui.Advance(time.Second)
			if tc.markdown {
				if !ui.HasText("文件为空") {
					t.Fatal("empty Markdown has no empty state", ui.Texts())
				}
				if err := ui.Click("源码"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, ok := ui.Find("源码"); ok {
					t.Fatal("non-Markdown file acquired Markdown modes")
				}
				if v.attachmentPreview.Reader == nil || !strings.Contains(v.attachmentPreview.Reader.Label, tc.name) {
					t.Fatal("non-Markdown reader changed")
				}
			}
		})
	}
}
