package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/diagram"
	"shioji/internal/domain"
)

const mermaidSample = "flowchart TD\n A[开始 Start] --> B{通过?}\n B -->|是| C[完成 Done]\n B -->|否| A\n"

var mermaidHelperBuild struct {
	sync.Once
	client diagram.Client
	err    error
}

func mermaidTestCache(t *testing.T) *mermaidCache {
	t.Helper()
	mermaidHelperBuild.Do(func() {
		root, _ := filepath.Abs("../..")
		path := filepath.Join(root, "build/native/mermaid-ui-helper")
		cmd := exec.Command("go", "build", "-o", path, "./cmd/shioji")
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			mermaidHelperBuild.err = err
			t.Log(string(output))
			return
		}
		mermaidHelperBuild.client = diagram.Client{Executable: path, USVG: filepath.Join(root, "build/diagram-runtime/usvg")}
	})
	if mermaidHelperBuild.err != nil {
		t.Fatal(mermaidHelperBuild.err)
	}
	cache := &mermaidCache{client: mermaidHelperBuild.client}
	t.Cleanup(cache.close)
	return cache
}
func TestMermaidReplyRendersAndCopiesOriginalSource(t *testing.T) {
	v := New(layoutModel(), nil, "")
	// First mount must offer a diagram-specific source control, even while loading.
	ui := n.NewTester(func(c *n.Context) {
		v.bindMarkdownLinks(c, &v.md)
		markdown.View(c, "```mermaid\n"+mermaidSample+"```\n", &v.md).FillWidth()
	}, 680, 750)
	if _, ok := ui.Find("查看 Mermaid 源码"); !ok {
		t.Fatal("Mermaid fence remained an ordinary code block", ui.Texts())
	}
	v.mermaid = mermaidTestCache(t)
	ui.Frame()
	waitNative(t, ui, func() bool { _, ok := ui.Find("Mermaid 流程图"); return ok })
	if err := ui.Click("查看 Mermaid 源码"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !ui.HasText("flowchart TD") {
		t.Fatal("source missing", ui.Texts())
	}
	if err := ui.Click("Copy code"); err != nil {
		t.Fatal(err)
	}
	if ui.Clipboard() != mermaidSample {
		t.Fatal("copy modified Mermaid source", ui.Clipboard())
	}
}
func TestMermaidFilePreviewAndDarkAppearance(t *testing.T) {
	v := New(layoutModel(), nil, "")
	cache := mermaidTestCache(t)
	t.Cleanup(func() {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		for source, entry := range cache.entries {
			if entry.err != "" {
				t.Logf("diagram %q: %s", source, entry.err)
			}
		}
	})
	p := &attachmentPreview{Attachment: domain.Attachment{ID: "mermaid", Name: "flow.md"}, presence: previewOpening, Text: "# 工作流程\n\n```mermaid\n" + mermaidSample + "```", Markdown: &markdownPreview{mode: "preview", diagrams: cache}}
	v.attachmentPreview = p
	ui := n.NewTester(v.View, 1000, 800)
	ui.Advance(time.Second)
	waitNative(t, ui, func() bool { _, ok := ui.Find("Mermaid 流程图"); return ok })
	first := cache.scope
	ui.SetDark(true)
	ui.Frame()
	waitNative(t, ui, func() bool { _, ok := ui.Find("Mermaid 流程图"); return ok && cache.scope != first })
	if err := ui.Click("关闭附件预览"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if !cache.closed {
		t.Fatal("closed preview retained render resources")
	}
}
func TestMermaidWaitsForStreamingFenceAndRecoversFromParseError(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.mermaid = mermaidTestCache(t)
	src := "```mermaid\n" + mermaidSample
	ui := n.NewTester(func(c *n.Context) { v.bindMarkdownLinks(c, &v.md); markdown.Stream(c, src, &v.md).FillWidth() }, 680, 750)
	ui.Advance(time.Second)
	if _, ok := ui.Find("Mermaid 流程图"); ok {
		t.Fatal("rendered an unfinished live fence")
	}
	if len(v.mermaid.entries) != 0 {
		t.Fatal("unfinished stream started rendering")
	}
	src += "```\n"
	ui.Frame()
	waitNative(t, ui, func() bool { _, ok := ui.Find("Mermaid 流程图"); return ok })
	src = "```mermaid\nflowchart TD\n A[unterminated\n```\n"
	ui.Frame()
	waitNative(t, ui, func() bool { return ui.HasText("已显示源码") })
	if !ui.HasText("unterminated") {
		t.Fatal("parse failure hid source")
	}
}

func TestMermaidSourceToggleAnimatesItsActualHeight(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.mermaid = mermaidTestCache(t)
	ui := n.NewTester(func(c *n.Context) {
		v.bindMarkdownLinks(c, &v.md)
		markdown.View(c, "```mermaid\n"+mermaidSample+"```\n", &v.md).FillWidth()
	}, 680, 750)
	waitNative(t, ui, func() bool { _, ok := ui.Find("Mermaid 流程图"); return ok })
	ui.Advance(time.Second)
	before, _ := ui.Find("Mermaid")
	ui.Click("查看 Mermaid 源码")
	ui.Advance(50 * time.Millisecond)
	mid, _ := ui.Find("Mermaid")
	ui.Advance(400 * time.Millisecond)
	after, _ := ui.Find("Mermaid")
	if !(after.H < mid.H && mid.H < before.H) {
		t.Fatal("source toggle jumped instead of animating layout height", before, mid, after)
	}
}
func TestMermaidCacheCancellationAndBounds(t *testing.T) {
	cache := mermaidTestCache(t)
	cache.reset("session-a")
	old := cache.ctx
	cache.reset("session-b")
	if old.Err() != context.Canceled {
		t.Fatal("session handoff did not cancel rendering")
	}
	cache.close()
	if cache.ctx.Err() != context.Canceled || !cache.closed {
		t.Fatal("window close did not cancel rendering")
	}
	_ = os.ErrClosed
}
