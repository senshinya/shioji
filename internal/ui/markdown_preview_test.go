package ui

import (
	"fmt"
	"math"
	"shioji/internal/domain"
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
