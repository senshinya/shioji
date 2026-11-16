package ui

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

// A delayed historical thumbnail must not push the already-visible tail down
// and then pull it back up with the live-output follow animation.
func TestSessionEntryImageLoadKeepsFollowingMessagesInPlace(t *testing.T) {
	for _, preferences := range []struct{ dark, reduced bool }{{}, {dark: true}, {reduced: true}, {dark: true, reduced: true}} {
		t.Run(fmt.Sprintf("dark=%t/reduced=%t", preferences.dark, preferences.reduced), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = []domain.Item{
				{ID: "older", Kind: "assistant", Text: strings.Repeat("较早内容。\n\n", 25), Final: true},
				{ID: "picture", Kind: "assistant", Text: "![历史图片](docs/image.png)\n\n图片后同一回复的文字。", Final: true},
				{ID: "question", Kind: "user", Text: "请解释图片中的内容。", Final: true},
				{ID: "answer", Kind: "assistant", Text: "最后一条回复保持阅读位置。\n\n" + strings.Repeat("这是图片的解释。\n\n", 5), Final: true},
			}
			m.Details["s"] = d
			v := New(m, nil, "")
			v.bindMarkdownImages(&v.md, d.Session)
			defer v.closeMarkdownImages()
			entry := &markdownImageEntry{id: "historical-image", thumbnail: attachmentImage{phase: attachmentImageLoading}}
			v.markdownImages.entries["docs/image.png"] = entry
			ui := n.NewTester(v.View, 1200, 820)
			ui.SetDark(preferences.dark)
			ui.SetPreferences(n.Preferences{ReduceMotion: preferences.reduced})
			ui.Advance(time.Second)
			before, ok := ui.Find("最后一条回复保持阅读位置。")
			if !ok || !v.lists["s"].FollowingEnd() {
				t.Fatal("fixture did not show latest reply", ui.Texts())
			}
			capture := func(frame int) {
				if dir := os.Getenv("SHIOJI_IMAGE_ENTRY_CAPTURE"); dir != "" && !preferences.reduced {
					t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(dir, fmt.Sprint(preferences.dark)))
					saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
				}
			}
			capture(0)
			entry.thumbnail.phase = attachmentImageReady
			entry.thumbnail.Image = deferredImageBitmap()
			worst := float64(0)
			for frame := 1; frame <= 60; frame++ {
				ui.Frame()
				current, visible := ui.Find("最后一条回复保持阅读位置。")
				if !visible {
					t.Fatal("image load lost latest reply", frame)
				}
				worst = max(worst, math.Abs(float64(current.Y-before.Y)))
				capture(frame)
				ui.Advance(time.Second / 60)
			}
			if worst > 1 {
				t.Errorf("historical image displaced following messages by %g DIP", worst)
			}
			if !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() {
				t.Fatal("image load lost latest position")
			}
		})
	}
}

func deferredImageBitmap() n.ImageSource {
	im := image.NewRGBA(image.Rect(0, 0, 224, 170))
	for y := range 170 {
		for x := range 224 {
			shade := uint8(210)
			if x%56 < 2 || y%42 < 2 {
				shade = 170
			}
			im.SetRGBA(x, y, color.RGBA{R: shade, G: shade, B: shade, A: 255})
		}
	}
	return n.NewBitmap(im)
}

// All of these deferred results share the transcript's height ownership. Test
// actual View rows so a card-only animation test cannot miss list movement.
func TestDeferredTranscriptContentKeepsReadingPosition(t *testing.T) {
	for _, kind := range []string{"mermaid", "tool-image", "tool-markdown", "tool-text", "file-card", "command"} {
		for _, dark := range []bool{false, true} {
			for _, reading := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/dark=%t/reading=%t", kind, dark, reading), func(t *testing.T) {
					m := layoutModel()
					d := m.Details["s"]
					a := domain.Attachment{ID: "deferred-picture", Name: "result.png", MIME: "image/png"}
					middle := domain.Item{ID: "deferred", Kind: "activity", Name: "加载结果", Presentation: "mcp", Text: "正在读取", Final: true}
					switch kind {
					case "mermaid":
						middle.Kind = "assistant"
						middle.Text = "```mermaid\n" + mermaidSample + "```\n\n图表后同一回复的文字。"
					case "tool-image":
						middle.Parts = []domain.ContentPart{{Kind: "image", Attachment: &a}}
					case "tool-markdown":
						middle.Text = ""
						middle.Parts = []domain.ContentPart{{Kind: "markdown", Text: "![历史图片](docs/image.png)\n\n图片下方的工具文字。"}}
					case "command":
						middle.Kind = "tool"
						middle.Presentation = "command"
					}
					older := ""
					for i := 0; i < 25; i++ {
						older += fmt.Sprintf("较早内容 %02d。\n\n", i)
					}
					d.Items = []domain.Item{
						{ID: "older", Kind: "assistant", Text: older, Final: true},
						middle,
						{ID: "question", Kind: "user", Text: "请解释上面的内容。", Final: true},
						{ID: "answer", Kind: "assistant", Text: "最后一条回复保持阅读位置。\n\n" + strings.Repeat("这是加载结果的解释。\n\n", 5), Final: true},
					}
					m.Details["s"] = d
					v := New(m, nil, "")
					defer v.closeMarkdownImages()
					defer func() {
						if v.mermaid != nil {
							v.mermaid.close()
						}
					}()
					v.bindMarkdownImages(&v.md, d.Session)
					entry := &markdownImageEntry{id: "historical-image", thumbnail: attachmentImage{phase: attachmentImageLoading}}
					v.markdownImages.entries["docs/image.png"] = entry
					v.attachmentImages = map[string]*attachmentImage{a.ID: {phase: attachmentImageLoading}}
					var diagram *mermaidEntry
					ui := n.NewTester(func(c *n.Context) {
						if kind == "mermaid" {
							if v.mermaid == nil {
								v.mermaid = &mermaidCache{}
							}
							v.mermaid.reset(fmt.Sprintf("s||%t", c.Theme().Dark))
							if v.mermaid.entries[mermaidSample] == nil {
								diagram = &mermaidEntry{running: true}
								v.mermaid.entries[mermaidSample] = diagram
							}
						}
						v.View(c)
					}, 1200, 820)
					ui.SetDark(dark)
					ui.Advance(time.Second)
					if kind != "mermaid" {
						if kind == "command" {
							ui.Click("1 次工具调用")
							ui.Advance(time.Second)
						}
						if err := ui.Click("加载结果"); err != nil {
							t.Fatal(err, ui.Texts())
						}
						ui.Advance(time.Second)
					}
					ui.Scroll(800, 350, 0, 10000)
					ui.Frame()
					ui.Advance(time.Second)
					if reading {
						ui.Scroll(800, 350, 0, -300)
						ui.Frame()
						ui.Advance(time.Second)
					}
					ui.Click("消息输入")
					ui.Type("保留草稿")
					marker := "最后一条回复保持阅读位置。"
					if reading {
						marker = ""
						for i := 0; i < 25; i++ {
							label := fmt.Sprintf("较早内容 %02d。", i)
							if r, found := ui.Find(label); found && r.Y >= 48 && r.Y < 400 {
								marker = label
								break
							}
						}
					}
					before, ok := ui.Find(marker)
					if !ok || v.lists["s"].FollowingEnd() == reading {
						t.Fatalf("fixture has wrong reading mode: marker=%q found=%t following=%t", marker, ok, v.lists["s"].FollowingEnd())
					}
					switch kind {
					case "mermaid":
						var err error
						diagram.image, err = n.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="224" height="240"><rect width="224" height="240" fill="#777"/></svg>`))
						if err != nil {
							t.Fatal(err)
						}
						diagram.done = true
						diagram.running = false
					case "tool-image":
						v.attachmentImages[a.ID] = &attachmentImage{phase: attachmentImageReady, Image: deferredImageBitmap()}
					case "tool-markdown":
						entry.thumbnail.phase = attachmentImageReady
						entry.thumbnail.Image = deferredImageBitmap()
					case "tool-text", "command":
						d.Items[1].Text = "读取完成\n" + strings.Repeat("工具返回的内容。\n", 5)
						m.Details["s"] = d
					case "file-card":
						d.Items[1].Parts = []domain.ContentPart{{Kind: "file", Attachment: &domain.Attachment{ID: "file", Name: "result.txt", MIME: "text/plain", Size: 123}}}
						m.Details["s"] = d
					}
					worst := float64(0)
					for frame := 0; frame < 60; frame++ {
						ui.Frame()
						if kind == "tool-markdown" && !reading {
							text, found := ui.Find("图片下方的工具文字。")
							row := v.historyRowBounds["deferred"]
							if !found || text.Y+text.H > row.Y+row.H-31 {
								t.Errorf("loaded Markdown child outgrew its tool row: frame=%d text=%v row=%v", frame, text, row)
								break
							}
						}
						current, visible := ui.Find(marker)
						if !visible {
							t.Fatal("load lost reading marker", frame)
						}
						worst = max(worst, math.Abs(float64(current.Y-before.Y)))
						ui.Advance(time.Second / 60)
					}
					if worst > 1 {
						t.Errorf("deferred content displaced following messages by %g DIP", worst)
					}
					if v.lists["s"].FollowingEnd() == reading {
						t.Fatal("load changed reading mode")
					}
					if m.Drafts[m.DraftKey()] != "保留草稿" || !ui.Focused("消息输入") {
						t.Fatal("load changed draft or focus")
					}
				})
			}
		}
	}
}

// The source of a newly arriving question takes over the running status slot;
// only the separate dock may move existing transcript content.
func TestDeferredRequestSourceKeepsStatusSlot(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%t/reduced=%t", dark, reduced), func(t *testing.T) {
				v, ui := progressFollowFixture()
				ui.SetDark(dark)
				ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
				ui.Advance(time.Second)
				before, ok := ui.Find("进度 35")
				dockBefore, _ := ui.Find("会话底部面板")
				if !ok || !v.lists["s"].FollowingEnd() {
					t.Fatal("fixture is not at latest")
				}
				d := v.Model.Details["s"]
				d.Requests = []domain.PendingRequest{{ID: "q", SessionID: "s", TurnID: "turn", Kind: "questions", State: "pending", Title: "需要回答", Questions: []domain.Question{{ID: "choice", Text: "是否继续？", Custom: true}}}}
				d.Items = append(d.Items, domain.Item{ID: "request:q", TurnID: "turn", Kind: "request", Text: "需要回答", Status: "pending"})
				v.Model.Details["s"] = d
				worst := float64(0)
				for frame := 0; frame < 60; frame++ {
					ui.Frame()
					current, visible := ui.Find("进度 35")
					dock, _ := ui.Find("会话底部面板")
					if !visible {
						t.Fatal("question load lost previous content", frame)
					}
					worst = max(worst, math.Abs(float64(current.Y-before.Y+dock.H-dockBefore.H)))
					ui.Advance(time.Second / 60)
				}
				if worst > 1 {
					t.Errorf("request source and running status overlapped their layout slots by %g DIP", worst)
				}
				if !v.lists["s"].AtEnd() || !v.lists["s"].FollowingEnd() {
					t.Fatal("question load lost latest position")
				}
			})
		}
	}
}

// Child work details reuse tool rendering but own a separate scroll container.
// Their layout must never reset the main conversation's following animation.
func TestChildToolGrowthDoesNotChangeParentFollowAnimation(t *testing.T) {
	v := New(layoutModel(), nil, "")
	parent := &n.ListState{FollowAnimation: &conversationFollowAnimation}
	v.lists["s"] = parent
	v.Model.Collapsed["activity:s/child-tool"] = false
	item := domain.Item{ID: "child-tool", Kind: "tool", Presentation: "mcp", Name: "Child result", Text: "Existing output", Final: true}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			v.activityItem(c, c.Theme(), item, false)
			n.Text(c, "After child result")
		})
	}, 600, 700)
	ui.Advance(time.Second)
	before, ok := ui.Find("After child result")
	if !ok {
		t.Fatal("child fixture is missing its result")
	}
	item.Text += "\n" + strings.Repeat("Additional child output\n", 5)
	for frame := 0; frame < 60; frame++ {
		ui.Frame()
		if parent.FollowAnimation != &conversationFollowAnimation {
			t.Fatalf("child tool changed parent follow animation at frame %d", frame)
		}
		ui.Advance(time.Second / 60)
	}
	after, _ := ui.Find("After child result")
	if after.Y <= before.Y+20 {
		t.Fatal("child result did not actually grow", before, after)
	}
}
