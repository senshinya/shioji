package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestMarkdownFileLinksResolveAgainstTheDisplayedSession(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items[0].Text = "[亮色验收视频](/device/review.mp4)\n\n[项目文档](docs/review.md)\n\n[外部页面](https://example.com/review)"
	d.Items[0].Final = true
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	link, ok := ui.Find("亮色验收视频")
	if !ok {
		t.Fatal("missing file link")
	}
	ui.ClickAt(link.X+10, link.Y+link.H/2)
	if v.fileLink == nil || v.fileLink.path != "/device/review.mp4" || v.fileLink.session != "s" || v.fileLink.error == "" {
		t.Fatalf("file link silently ignored: state=%+v session=%s error=%s texts=%v", v.fileLink, v.Model.SessionID, v.Model.Error, ui.Texts())
	}
	ui.Click("关闭文件链接")
	ui.Advance(time.Second)
	external, ok := ui.Find("外部页面")
	if !ok {
		t.Fatal("missing web link")
	}
	ui.ClickAt(external.X+10, external.Y+external.H/2)
	if urls := ui.OpenedURLs(); len(urls) != 1 || urls[0] != "https://example.com/review" {
		t.Fatal("external link handling changed", urls)
	}
}
func TestFileLinkPathDecoding(t *testing.T) {
	for _, tc := range []struct{ link, want string }{
		{"/Users/test/a%20b.mp4", "/Users/test/a b.mp4"},
		{"file:///tmp/review.pdf", "/tmp/review.pdf"},
		{"docs/notes.md", "docs/notes.md"},
		{"main.go:12", "main.go"},
		{"/tmp/main.go:12:3", "/tmp/main.go"},
	} {
		got, err := fileLinkPath(tc.link)
		if err != nil || got != tc.want {
			t.Errorf("%q => %q, %v", tc.link, got, err)
		}
	}
	for _, bad := range []string{"javascript:alert(1)", "file://another-host/tmp/file", "//server/file", "data:text/plain,hello", "/tmp/a%00b"} {
		if _, err := fileLinkPath(bad); err == nil {
			t.Errorf("accepted unsafe link %q", bad)
		}
	}
}
func TestFileLinkCancelAndNavigationDiscardLateResults(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	s := &fileLinkState{session: "s", workspace: v.Model.Details["s"].Session.WorkspaceID, epoch: v.Model.ConnectionEpoch, path: "report.mp4", loading: true, cancel: cancel}
	v.fileLink = s
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)
	if !ui.HasText("report.mp4") || !ui.HasText("正在连接所属设备…") {
		t.Fatal("missing download state")
	}
	if err := ui.Click("取消文件下载"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || v.fileLink != nil {
		t.Fatal("cancel did not cancel the transfer")
	}
	ctx, cancel = context.WithCancel(context.Background())
	s = &fileLinkState{session: "s", workspace: v.Model.Details["s"].Session.WorkspaceID, epoch: v.Model.ConnectionEpoch, path: "report.mp4", loading: true, cancel: cancel}
	v.fileLink = s
	v.Model.SessionID = "other"
	ui.Frame()
	if ctx.Err() == nil || v.fileLink != nil {
		t.Fatal("navigation kept stale download active")
	}
}

func TestFileLinkUsesCachedFileAndReportsSystemOpenFailure(t *testing.T) {
	v := New(layoutModel(), nil, "")
	var services n.Services
	ui := n.NewTester(func(c *n.Context) { services = c.Services(); v.View(c) }, 1240, 820)
	s := &fileLinkState{session: "s", workspace: v.Model.Details["s"].Session.WorkspaceID, epoch: v.Model.ConnectionEpoch, path: "/device/review.mp4", services: services, result: &app.LinkedFile{Path: "/private/cache/review file.mp4", Resource: domain.FileResource{Name: "review file.mp4", MIME: "video/mp4"}}}
	v.fileLink = s
	ui.FailOpenURL(errors.New("没有可用的播放器"))
	v.openLinkedFile(s)
	ui.Frame()
	ui.Advance(time.Second)
	if !strings.Contains(s.error, "没有可用的播放器") || v.fileLink != s {
		t.Fatal("system open failure was silent", s.error)
	}
	if urls := ui.OpenedURLs(); len(urls) != 1 || urls[0] != "file:///private/cache/review%20file.mp4" {
		t.Fatal("opened remote source path instead of downloaded cache", urls)
	}
	ui.FailOpenURL(nil)
	if err := ui.Click("用默认应用打开"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Advance(time.Second)
	if v.fileLink != nil || len(ui.OpenedURLs()) != 2 {
		t.Fatal("retry did not finish opening")
	}
	// A detached secondary pane must never open an asynchronously completed file.
	child := New(layoutModel(), nil, "")
	child.parent = v
	child.fileLink = s
	v.Model.SecondarySessionID = "different-session"
	if child.fileLinkCurrent(s) {
		t.Fatal("detached pane still owns a file open")
	}
}

func TestLinkedPreviewKeepsDocumentsExternalAndOwnsItsSession(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, mime, text string
		native           bool
	}{
		{"review.rtf", "application/rtf", "{\\rtf1 review}", false},
		{"review.pdf", "application/pdf", "%PDF-1.7\nreview", false},
		{"main.go", "text/plain", "package main\n", true},
	} {
		path := filepath.Join(dir, tc.name)
		data := []byte(tc.text)
		os.WriteFile(path, data, 0600)
		hash := sha256.Sum256(data)
		result := app.LinkedFile{Path: path, SHA256: hex.EncodeToString(hash[:]), Resource: domain.FileResource{Name: tc.name, MIME: tc.mime, Size: int64(len(data))}}
		preview, err := prepareLinkedPreview(result, "w", "s", "epoch")
		if err != nil || (preview != nil) != tc.native {
			t.Errorf("wrong native/system route for %s: %v %v", tc.name, preview, err)
		}
		if preview != nil {
			if preview.LocalPath != path || preview.Text != tc.text {
				t.Fatal("preview lost cached bytes")
			}
			v := New(layoutModel(), nil, "")
			v.attachmentPreview = preview
			v.Model.ConnectionEpoch = "other-epoch"
			ui := n.NewTester(v.View, 1240, 820)
			ui.Advance(time.Second)
			if _, ok := ui.Find("附件预览"); ok {
				t.Fatal("previous connection's preview survived handoff")
			}
		}
	}
}

func TestCloseTransientCancelsLinkedFilesInBothPanes(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.secondary = New(layoutModel(), nil, "")
	for _, pane := range []*View{v, v.secondary} {
		ctx, cancel := context.WithCancel(context.Background())
		pane.fileLink = &fileLinkState{cancel: cancel}
		t.Cleanup(func() {
			if ctx.Err() == nil {
				t.Error("window close left linked-file transfer running")
			}
			cancel()
		})
	}
	v.CloseTransient()
	if v.fileLink != nil || v.secondary.fileLink != nil {
		t.Fatal("window close retained file-link state")
	}
}

func TestLinkedFileCannotOpenExecutableThroughSpoofedMIME(t *testing.T) {
	for _, name := range []string{"payload.jar", "payload.command", "payload.app", "payload.workflow", "payload.scpt", "payload.webloc"} {
		t.Run(name, func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			var services n.Services
			ui := n.NewTester(func(c *n.Context) { services = c.Services(); v.View(c) }, 1240, 820)
			s := &fileLinkState{session: "s", workspace: v.Model.Details["s"].Session.WorkspaceID, epoch: v.Model.ConnectionEpoch, path: name, services: services, result: &app.LinkedFile{Path: filepath.Join("/private/cache", name), Resource: domain.FileResource{Name: name, MIME: "video/mp4"}}}
			v.fileLink = s
			v.openLinkedFile(s)
			ui.Frame()
			ui.Advance(time.Second)
			if len(ui.OpenedURLs()) != 0 || ui.HasText("用默认应用打开") {
				t.Fatal("peer MIME enabled an executable default handler", ui.OpenedURLs())
			}
		})
	}
}
