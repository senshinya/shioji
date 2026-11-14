package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func occlusionView(kind string) *View {
	m := layoutModel()
	d := m.Details["s"]
	line := strings.Repeat("constBoundary=window.document.querySelector('long-content');", 5)
	code := strings.Repeat(line+"\n", 80)
	switch kind {
	case "command":
		d.Items = nil
		for i := 0; i < 35; i++ {
			d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("command-", i), Kind: "tool", Presentation: "command", Name: line, Text: code, Final: true})
		}
	case "tool-code":
		parts := []domain.ContentPart{}
		for i := 0; i < 12; i++ {
			parts = append(parts, domain.ContentPart{Kind: "code", Text: code})
		}
		d.Items = []domain.Item{{ID: "body", Kind: "tool", Presentation: "mcp", Name: "长代码结果", Parts: parts, Final: true}}
	case "nested-markdown":
		d.Items = []domain.Item{{ID: "body", Kind: "assistant", Text: strings.Repeat(strings.Repeat("> ", 60)+"deeply_nested_content\n\n", 8), Final: true}}
	default:
		d.Items = []domain.Item{{ID: "body", Kind: "assistant", Text: "```javascript\n" + code + "```", Final: true}}
	}
	m.Details["s"] = d
	return New(m, nil, "")
}

func TestConversationChromeCoversHistoryAtThePaneEdges(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1000, 1240, 1800} {
			v := occlusionView("markdown")
			ui := n.NewTester(v.View, width, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			ui.Scroll(float32(width-200), 300, 0, -380)
			ui.Move(0, 0)
			ui.Frame()
			ui.Advance(time.Second)
			history, _ := ui.Find("对话记录")
			toolbar, _ := ui.Find("会话顶栏")
			composer, _ := ui.Find("消息编辑器")
			if toolbar.X != history.X || toolbar.W != history.W {
				t.Errorf("top chrome does not cover the full pane: width=%d history=%v toolbar=%v", width, history, toolbar)
			}
			empty := New(layoutModel(), nil, "")
			d := empty.Model.Details["s"]
			d.Items = nil
			empty.Model.Details["s"] = d
			baseline := n.NewTester(empty.View, width, 820)
			baseline.SetDark(dark)
			baseline.Advance(time.Second)
			a, b := ui.Image(), baseline.Image()
			region := image.Rect(int(composer.X), 813, int(composer.X+composer.W), 820)
			if differentPixels(a, b, region) > 0 {
				t.Errorf("history is visible below the composer: width=%d dark=%v", width, dark)
			}
		}
	}
}

func differentPixels(a, b *image.RGBA, r image.Rectangle) int {
	count := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				count++
			}
		}
	}
	return count
}

func TestConversationContentStaysInsideTheComposerRightEdge(t *testing.T) {
	for _, kind := range []string{"markdown", "tool-code", "command", "nested-markdown"} {
		for _, width := range []int{1000, 1240, 1800} {
			v := occlusionView(kind)
			ui := n.NewTester(v.View, width, 820)
			ui.SetDark(true)
			ui.Advance(time.Second)
			ui.Scroll(float32(width-200), 300, 0, -380)
			ui.Move(0, 0)
			ui.Frame()
			ui.Advance(time.Second)
			composer, _ := ui.Find("消息编辑器")
			history, _ := ui.Find("对话记录")
			dock, _ := ui.Find("会话底部面板")
			im := ui.Image()
			bg := im.RGBAAt(int(history.X+1), 70)
			outside := 0
			for y := 110; y < int(dock.Y-30); y++ {
				for x := int(composer.X + composer.W + 1); x < int(history.X+history.W-conversationScrollbarGutter-1); x++ {
					if im.RGBAAt(x, y) != bg {
						outside++
					}
				}
			}
			if outside > 0 {
				t.Errorf("%s paints past composer right edge: width=%d pixels=%d composer=%v", kind, width, outside, composer)
			}
		}
	}
}

func TestConversationOcclusionNativeRecording(t *testing.T) {
	root := os.Getenv("SHIOJI_OCCLUSION_CAPTURE")
	if root == "" {
		t.Skip("set SHIOJI_OCCLUSION_CAPTURE for native videos")
	}
	kind := os.Getenv("SHIOJI_OCCLUSION_KIND")
	if kind == "" {
		kind = "markdown"
	}
	for _, dark := range []bool{false, true} {
		v := occlusionView(kind)
		ui := n.NewTester(v.View, 1240, 820)
		ui.SetDark(dark)
		ui.Advance(time.Second)
		t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(root, fmt.Sprint(dark)))
		for frame := 0; frame < 480; frame++ {
			switch frame {
			case 60, 120:
				ui.Scroll(1000, 300, 0, -380)
			case 180:
				ui.Scroll(1000, 300, 0, 120)
			case 240:
				d := v.Model.Details["s"]
				d.Requests = []domain.PendingRequest{{ID: "q", SessionID: "s", Kind: "questions", State: "pending", Title: "遮挡检查", Questions: []domain.Question{{ID: "q", Text: "检查内容边界", Custom: true}}}}
				v.Model.Details["s"] = d
			case 300:
				v.Model.Drafts["session:s"] = "第一行\n第二行\n第三行\n第四行\n第五行"
			case 360:
				d := v.Model.Details["s"]
				d.Requests = nil
				v.Model.Details["s"] = d
				v.Model.Drafts["session:s"] = ""
			case 420:
				if err := ui.Click("回到最新"); err != nil {
					t.Fatal(err)
				}
			}
			ui.Move(0, 0)
			ui.Frame()
			saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
			ui.Advance(time.Second / 60)
		}
	}
}
