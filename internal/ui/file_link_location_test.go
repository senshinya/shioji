package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shioji/internal/domain"

	n "github.com/egoist/mygo/ui"
)

// Exercise actual link clicks, the owning worker's file read, and the native
// editor. Dropping a location anywhere along that route must fail this test.
func TestFileLinksRevealSourceLine(t *testing.T) {
	for _, tc := range []struct {
		name, suffix, origin string
		markdown             bool
		last, column         int
	}{
		{"colon", ":100", "document", false, 100, 0},
		{"column", ":100:3", "reply", false, 100, 2},
		{"fragment", "#L100", "document", false, 100, 0},
		{"range", "#L100-L103", "reply", false, 103, 0},
		{"markdown-source", ":100", "document", true, 100, 0},
		{"user-file-url", ":100:3", "user", false, 100, 2},
		{"retry", ":100", "reply", false, 100, 0},
		{"dark", ":100", "document", false, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, ui, root := markdownWorkerPreview(t, "# 代码入口\n\n[跳到实现](../main.go"+tc.suffix+")\n")
			ui.SetDark(tc.name == "dark")
			ui.Frame()
			name := "main.go"
			if tc.markdown {
				name = "notes.md"
			}
			var text strings.Builder
			for i := 1; i <= 180; i++ {
				fmt.Fprintf(&text, "// 第 %03d 行 English：原生文件预览\n", i)
			}
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte(text.String()), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.origin == "document" {
				if tc.markdown {
					v.attachmentPreview.Text = "[跳到实现](../notes.md" + tc.suffix + ")"
					ui.Frame()
				}
			} else {
				ui.Click("关闭附件预览")
				ui.Advance(time.Second)
				d := v.Model.Details[v.Model.SessionID]
				d.Items = []domain.Item{{ID: "location-link", Kind: "assistant"}}
				d.Items[0].Text = "[跳到实现](" + path + tc.suffix + ")"
				if tc.origin == "user" {
					d.Items[0].Kind = "user"
					d.Items[0].Text = "file://" + path + tc.suffix
				}
				d.Items[0].Final = true
				v.Model.Details[v.Model.SessionID] = d
				ui.Frame()
				ui.Advance(time.Second)
			}
			label := "跳到实现"
			if tc.origin == "user" {
				label = "file://" + path + tc.suffix
			}
			link, ok := ui.Find(label)
			if !ok {
				t.Fatal("missing source link", ui.Texts())
			}
			if tc.name == "retry" {
				v.Model.Connected = false
			}
			record := os.Getenv("SHIOJI_FILE_LINE_EVIDENCE") != "" && tc.name == "colon"
			if record {
				for frame := range 15 {
					ui.Advance(time.Second / 30)
					saveFileLineEvidence(t, ui, fmt.Sprintf("frames/frame-%03d", frame))
				}
			}
			ui.ClickAt(link.X+5, link.Y+link.H/2)
			if record {
				for frame := 15; frame < 75; frame++ {
					ui.Advance(time.Second / 30)
					saveFileLineEvidence(t, ui, fmt.Sprintf("frames/frame-%03d", frame))
				}
			}
			if tc.name == "retry" {
				v.Model.Connected = true
				if err := ui.Click("重试"); err != nil {
					t.Fatal(err)
				}
			}
			waitNative(t, ui, func() bool {
				return v.attachmentPreview != nil && v.attachmentPreview.SourcePath == path || v.fileLink != nil && !v.fileLink.loading
			})
			ui.Advance(time.Second)
			p := v.attachmentPreview
			if p == nil || p.SourcePath != path || p.Reader == nil {
				t.Fatalf("line link did not open source: preview=%+v transfer=%+v", p, v.fileLink)
			}
			saveFileLineEvidence(t, ui, tc.name)
			if p.Reader.ScrollY() < 500 {
				t.Fatal("location was discarded: preview stayed at file start", p.Reader.ScrollY())
			}
			snapshot := p.Reader.Buffer.Snapshot()
			if p.Reader.Cursor() != snapshot.LineStart(99)+tc.column {
				t.Fatal("wrong source location", p.Reader.Cursor())
			}
			backgrounds := p.Reader.LineBackgrounds
			if len(backgrounds) != 1 || backgrounds[0].First != 99 || backgrounds[0].Last != tc.last-1 || backgrounds[0].Color.A == 0 {
				t.Fatal("source lines are not highlighted", backgrounds)
			}
			y := p.Reader.LineTop(99) - p.Reader.ScrollY()
			r, _ := ui.Find("文件内容：" + name)
			if y < 0 || y > float64(r.H-18) {
				t.Fatal("target line is outside viewport", y, r)
			}
			ui.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, 180)
			ui.Advance(time.Second)
			scrolled := p.Reader.ScrollY()
			ui.Advance(time.Second)
			if p.Reader.ScrollY() != scrolled {
				t.Fatal("rerender forced reader back to linked line")
			}
		})
	}
}

func TestFileLinkTargetSeparatesLocationFromDevicePath(t *testing.T) {
	for _, tc := range []struct {
		link, path string
		position   fileLinkPosition
	}{
		{"main.go:12", "main.go", fileLinkPosition{Line: 12}},
		{"/tmp/main.go:12:3", "/tmp/main.go", fileLinkPosition{Line: 12, Column: 3}},
		{"file:///tmp/a%20b.go#L12-L15", "/tmp/a b.go", fileLinkPosition{Line: 12, LastLine: 15}},
		{"src/main.go#L12-15", "src/main.go", fileLinkPosition{Line: 12, LastLine: 15}},
		{"src/main.go#L12", "src/main.go", fileLinkPosition{Line: 12}},
		{"/tmp/file%3A12", "/tmp/file:12", fileLinkPosition{}},
		{"/tmp/file%23L12", "/tmp/file#L12", fileLinkPosition{}},
		{"docs/guide.md#heading", "docs/guide.md", fileLinkPosition{}},
		{"src/main.go", "src/main.go", fileLinkPosition{}},
	} {
		path, position, err := fileLinkTarget(tc.link)
		if err != nil || path != tc.path || position != tc.position {
			t.Errorf("%q => %q %+v, %v", tc.link, path, position, err)
		}
	}
}

func saveFileLineEvidence(t *testing.T, ui *n.Tester, name string) {
	t.Helper()
	dir := os.Getenv("SHIOJI_FILE_LINE_EVIDENCE")
	if dir == "" {
		return
	}
	path := filepath.Join(dir, name+".png")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, ui.Image()); err != nil {
		t.Fatal(err)
	}
}
