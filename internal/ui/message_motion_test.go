package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"image"
	"image/color"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

// Exercise mutations inside one already-mounted tool; animating only the
// enclosing transcript row cannot cover these additions and removals.
func TestToolResultBlocksRevealWithoutReplayingExistingContent(t *testing.T) {
	for _, kind := range []string{"file", "image", "text", "code", "markdown", "patch", "agent", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			v.Model.Collapsed["activity:s/tool"] = false
			a := domain.Attachment{ID: "new", Name: "result.go", MIME: "text/plain", Size: 301}
			if kind == "image" {
				a.Name = "result.png"
				a.MIME = "image/png"
				v.attachmentImages = map[string]*attachmentImage{a.ID: {phase: attachmentImageLoading}}
			}
			item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Result", Text: "Existing stable content."}
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Background(c.Theme().Background).Children(func() {
					n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) })
					n.Text(c, "Following content")
				})
			}, 600, 700)
			ui.Advance(time.Second)
			before, _ := ui.Find("Following content")
			fixed, _ := ui.Find("Existing stable content.")
			oldInk := toolLabelContrast(ui.Image(), fixed, false)
			label := "New result"
			switch kind {
			case "file", "image":
				item.Parts = []domain.ContentPart{{Kind: kind, Attachment: &a}}
				label = "预览文件：" + a.Name
				if kind == "image" {
					label = "预览图片：" + a.Name
				}
			case "patch":
				item.Files = []domain.FileChange{{Path: label, Kind: "add", Patch: "@@ -0,0 +1 @@\n+new"}}
			case "agent":
				item.Agents = []domain.AgentActivity{{ID: label, Status: "running", Message: "Child output"}}
			case "truncated":
				item.Truncated = true
				label = "内容过长，已截断显示"
			default:
				item.Parts = []domain.ContentPart{{Kind: kind, Text: label}}
			}
			ui.Frame()
			first, _ := ui.Find("Following content")
			ui.Advance(50 * time.Millisecond)
			middle, _ := ui.Find("Following content")
			block, ok := ui.Find(label)
			if !ok {
				t.Fatal("new block missing", ui.Texts())
			}
			midInk := toolLabelContrast(ui.Image(), block, false)
			ui.Advance(time.Second)
			end, _ := ui.Find("Following content")
			block, _ = ui.Find(label)
			if math.Abs(float64(first.Y-before.Y)) > 1 || middle.Y <= first.Y+1 || middle.Y >= end.Y-1 {
				t.Fatal("block height pops instead of revealing", before, first, middle, end)
			}
			// Images in their loading state have a surface but no ink at the label bounds;
			// all other block contents must fade independently of the existing text.
			if kind != "image" && midInk >= toolLabelContrast(ui.Image(), block, false)-2 {
				t.Fatal("new block ink is already fully visible")
			}
			fixed, _ = ui.Find("Existing stable content.")
			if toolLabelContrast(ui.Image(), fixed, false) != oldInk {
				t.Fatal("existing content was reanimated")
			}
		})
	}
}

func TestRemovingFirstFileKeepsSurvivorAndCollapsesHeight(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.Model.Collapsed["activity:s/tool"] = false
	a := domain.Attachment{ID: "a", Name: "first.go", MIME: "text/plain", Size: 301}
	b := a
	b.ID = "b"
	b.Name = "second.go"
	item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Result", Parts: []domain.ContentPart{{Kind: "file", Attachment: &a}, {Kind: "file", Attachment: &b}}}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) })
			n.Text(c, "After")
		})
	}, 600, 500)
	ui.Advance(time.Second)
	before, _ := ui.Find("After")
	item.Parts = item.Parts[1:]
	ui.Frame()
	first, _ := ui.Find("After")
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("After")
	if _, ok := ui.Find("预览文件：second.go"); !ok {
		t.Fatal("survivor disappeared")
	}
	ui.Advance(time.Second)
	end, _ := ui.Find("After")
	if math.Abs(float64(first.Y-before.Y)) > 1 || middle.Y >= first.Y-1 || middle.Y <= end.Y+1 {
		t.Fatal("removed file collapses instantly", before, first, middle, end)
	}
	if _, ok := ui.Find("预览文件：first.go"); ok {
		t.Fatal("removed file remains actionable")
	}
}

func TestReplyFooterCrossfadesStatusAndLateUsage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			turn := domain.Turn{ID: "t", Status: "running"}
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Background(c.Theme().Background).Children(func() { v.turnFooter(c, c.Theme(), turn) })
			}, 700, 120)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			turn.Status = "completed"
			ui.Frame()
			ui.Advance(50 * time.Millisecond)
			r, ok := ui.Find("结束时间：—")
			if !ok {
				t.Fatal("missing completed information")
			}
			mid := toolLabelContrast(ui.Image(), r, dark)
			ui.Advance(time.Second)
			r, _ = ui.Find("结束时间：—")
			end := toolLabelContrast(ui.Image(), r, dark)
			if mid >= end-2 {
				t.Fatal("completed footer pops in", mid, end)
			}
			old := copyFrame(ui)
			used := int64(125)
			turn.InputTokens = &used
			ui.Frame()
			// The first update frame must preserve the old text before blending digits.
			if !samePixels(old, ui.Image()) {
				t.Fatal("late usage immediately replaces the visible text")
			}
			ui.Advance(time.Second)
			if samePixels(old, ui.Image()) {
				t.Fatal("late usage never appears")
			}
		})
	}
}
func samePixels(a, b *image.RGBA) bool {
	if len(a.Pix) != len(b.Pix) {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}

func TestFirstToolResultExpandsFromAnEmptyBody(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.Model.Collapsed["activity:s/tool"] = false
	item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Waiting"}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) })
			n.Text(c, "After")
		})
	}, 600, 500)
	ui.Advance(time.Second)
	before, _ := ui.Find("After")
	item.Text = "First result"
	ui.Frame()
	first, _ := ui.Find("After")
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("After")
	ui.Advance(time.Second)
	end, _ := ui.Find("After")
	if math.Abs(float64(first.Y-before.Y)) > 1 || middle.Y <= first.Y+1 || middle.Y >= end.Y-1 {
		t.Fatal("empty tool body pops open", before, first, middle, end)
	}
	item.Text = ""
	ui.Frame()
	first, _ = ui.Find("After")
	ui.Advance(60 * time.Millisecond)
	middle, _ = ui.Find("After")
	ui.Advance(time.Second)
	end, _ = ui.Find("After")
	if middle.Y >= first.Y-1 || middle.Y <= end.Y+1 {
		t.Fatal("last result pops closed", first, middle, end)
	}
}

func TestImageFailureAndRetryFadeTheirOwnContents(t *testing.T) {
	v := New(layoutModel(), nil, "")
	a := domain.Attachment{ID: "image", Name: "image.png", MIME: "image/png"}
	v.attachmentImages = map[string]*attachmentImage{a.ID: {phase: attachmentImageLoading}}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Background(c.Theme().Background).Children(func() { v.outputImage(c, c.Theme(), a) })
	}, 400, 300)
	ui.Advance(time.Second)
	v.attachmentImages[a.ID].phase = attachmentImageFailed
	v.attachmentImages[a.ID].Error = "Cannot load"
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	retry, ok := ui.Find("重试图片")
	if !ok {
		t.Fatal("retry missing")
	}
	mid := toolLabelContrast(ui.Image(), retry, false)
	ui.Advance(time.Second)
	retry, _ = ui.Find("重试图片")
	end := toolLabelContrast(ui.Image(), retry, false)
	if mid >= end-2 {
		t.Fatal("retry pops in without a fade", mid, end)
	}
}

func TestNewActivityAndTurnFooterRevealTheirTranscriptHeight(t *testing.T) {
	for _, kind := range []string{"activity", "notice", "footer"} {
		t.Run(kind, func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = []domain.Item{{ID: "user", Kind: "user", Text: "Check", TurnID: "t", Final: true}}
			d.Turns = nil
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1200, 820)
			ui.Advance(time.Second)
			switch kind {
			case "footer":
				d.Turns = []domain.Turn{{ID: "t", Status: "running"}}
			default:
				d.Items = append(d.Items, domain.Item{ID: "added", Kind: kind, Name: "New activity", Text: "New event", Presentation: "context", TurnID: "t"})
			}
			m.Details["s"] = d
			ui.Frame()
			ui.Advance(60 * time.Millisecond)
			label := "New event"
			if kind == "activity" {
				label = "New activity"
			}
			if kind == "footer" {
				label = "进行中"
			}
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("new element missing", ui.Texts())
			}
			mid := toolLabelContrast(ui.Image(), r, false)
			ui.Advance(time.Second)
			r, _ = ui.Find(label)
			end := toolLabelContrast(ui.Image(), r, false)
			if mid >= end-2 {
				t.Fatal("new transcript metadata pops in", kind, mid, end)
			}
		})
	}
}

func TestNestedToolMetadataDoesNotPopIntoExistingBlock(t *testing.T) {
	for _, kind := range []string{"label", "destination", "patch", "agent-message"} {
		t.Run(kind, func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			v.Model.Collapsed["activity:s/tool"] = false
			item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Result"}
			switch kind {
			case "label":
				item.Parts = []domain.ContentPart{{Kind: "text", Text: "Existing text"}}
			case "agent-message":
				item.Agents = []domain.AgentActivity{{ID: "child", Status: "running"}}
			default:
				item.Files = []domain.FileChange{{Path: "main.go", Kind: "update"}}
			}
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Background(c.Theme().Background).Children(func() {
					n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) })
					n.Text(c, "After")
				})
			}, 600, 500)
			ui.Advance(time.Second)
			before, _ := ui.Find("After")
			label := "Additional metadata"
			switch kind {
			case "label":
				item.Parts[0].Label = label
			case "agent-message":
				item.Agents[0].Message = label
			case "destination":
				item.Files[0].Destination = label
				label = "→ " + label
			case "patch":
				item.Files[0].Patch = "@@ -1 +1 @@\n-old\n+new"
				label = "文件补丁：main.go"
			}
			ui.Frame()
			first, _ := ui.Find("After")
			ui.Advance(60 * time.Millisecond)
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("missing content", ui.Texts())
			}
			mid := toolLabelContrast(ui.Image(), r, false)
			ui.Advance(time.Second)
			r, _ = ui.Find(label)
			end := toolLabelContrast(ui.Image(), r, false)
			if math.Abs(float64(first.Y-before.Y)) > 1 || mid >= end-2 {
				t.Fatal("nested content pops in", kind, mid, end, before, first)
			}
		})
	}
}

func TestConversationNoticeRetainsItsHeightWhileClosing(t *testing.T) {
	v := New(layoutModel(), nil, "")
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() { v.notice(c, c.Theme()); n.Text(c, "After notice") })
		})
	}, 800, 300)
	ui.Advance(time.Second)
	before, _ := ui.Find("After notice")
	v.draftSaveError = "Temporary save error"
	ui.Frame()
	first, _ := ui.Find("After notice")
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("After notice")
	ui.Advance(time.Second)
	end, _ := ui.Find("After notice")
	if math.Abs(float64(first.Y-before.Y)) > 1 || middle.Y <= first.Y+1 || middle.Y >= end.Y-1 {
		t.Fatal("notice pops in", before, first, middle, end)
	}
	v.draftSaveError = ""
	ui.Frame()
	first, _ = ui.Find("After notice")
	ui.Advance(60 * time.Millisecond)
	middle, _ = ui.Find("After notice")
	ui.Advance(time.Second)
	end, _ = ui.Find("After notice")
	if middle.Y >= first.Y-1 || middle.Y <= end.Y+1 {
		t.Fatal("notice pops out", first, middle, end)
	}
}

func TestRecoveryModeChangesFadeInsideItsResizingCard(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 8
	v := New(m, nil, "")
	d := m.Details["s"]
	d.Session.Status = "unknown"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "checking"}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Background(c.Theme().Background).Children(func() { v.recovery(c, c.Theme(), d) })
	}, 800, 400)
	ui.Advance(time.Second)
	d.Session.Status = "interrupted"
	d.Session.Recovery = &domain.SessionRecovery{Phase: "ready", CanContinue: true}
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	r, ok := ui.Find("上次回复已中断")
	if !ok {
		t.Fatal("missing recovery prompt", ui.Texts())
	}
	mid := toolLabelContrast(ui.Image(), r, false)
	ui.Advance(time.Second)
	r, _ = ui.Find("上次回复已中断")
	end := toolLabelContrast(ui.Image(), r, false)
	if mid >= end-2 {
		t.Fatal("recovery contents pop in on mode change", mid, end)
	}
}

func TestNewMessageMetadataAnimationsRespectHistoryAndReduceMotion(t *testing.T) {
	for _, reduce := range []bool{false, true} {
		t.Run(fmt.Sprint(reduce), func(t *testing.T) {
			m := layoutModel()
			m.Preferences.ReduceMotion = reduce
			d := m.Details["s"]
			d.Items = []domain.Item{{ID: "activity", Kind: "activity", Name: "Existing activity", Presentation: "context", Text: "Old content"}}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1200, 820)
			r, ok := ui.Find("Existing activity")
			if !ok || r.H < 19 {
				t.Fatal("history is animated from an empty row")
			}
			if reduce {
				d.Items = append(d.Items, domain.Item{ID: "next", Kind: "activity", Name: "Next activity", Presentation: "context", Text: "New content"})
				m.Details["s"] = d
				ui.Frame()
				r, ok = ui.Find("Next activity")
				if !ok || r.H < 19 {
					t.Fatal("Reduce Motion collapses newly arriving metadata")
				}
			}
		})
	}
}

func TestToolTextAppendFadesOnlyTheNewBytes(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.Model.Collapsed["activity:s/tool"] = false
	item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Result", Parts: []domain.ContentPart{{Kind: "text", Label: "Details", Text: "Existing."}}}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Background(c.Theme().Background).Children(func() { n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) }) })
	}, 600, 300)
	ui.Advance(time.Second)
	r, _ := ui.Find("Existing.")
	ink := toolLabelContrast(ui.Image(), r, false)
	item.Parts[0].Text = "Existing. Added result."
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	r, _ = ui.Find(item.Parts[0].Text)
	oldBounds := r
	oldBounds.W = 45
	if toolLabelContrast(ui.Image(), oldBounds, false) != ink {
		t.Fatal("old text refades")
	}
	tail := r
	tail.X += 65
	tail.W -= 65
	mid := toolLabelContrast(ui.Image(), tail, false)
	ui.Advance(time.Second)
	r, _ = ui.Find(item.Parts[0].Text)
	tail = r
	tail.X += 65
	tail.W -= 65
	end := toolLabelContrast(ui.Image(), tail, false)
	if mid >= end-2 {
		t.Fatal("appended text appears at full opacity", mid, end)
	}
}

func TestAnimatedToolSummaryKeepsSearchHighlight(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Result", Text: "Target marker", Final: true}}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Key(n.Cmd, n.KeyF)
	ui.Type("marker")
	ui.Advance(time.Second)
	r, ok := ui.Find("Target marker")
	if !ok {
		t.Fatal("summary missing")
	}
	yellow := 0
	frame := ui.Image()
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			p := frame.RGBAAt(x, y)
			if p.R > 210 && p.G > 170 && p.B < 140 {
				yellow++
			}
		}
	}
	if yellow < 20 {
		t.Fatal("animated summary lost native text-range geometry", yellow)
	}
}

func TestImageLoadHeightAndFollowingTextUseOneLayoutClock(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.Model.Collapsed["activity:s/tool"] = false
	a := domain.Attachment{ID: "image", Name: "picture.png", MIME: "image/png"}
	v.attachmentImages = map[string]*attachmentImage{a.ID: {phase: attachmentImageLoading}}
	item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Result", Parts: []domain.ContentPart{{Kind: "image", Attachment: &a}}}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) })
			n.Text(c, "After")
		})
	}, 600, 500)
	ui.Advance(time.Second)
	img, _ := ui.Find("预览图片：picture.png")
	after, _ := ui.Find("After")
	gap := after.Y - img.Y - img.H
	v.attachmentImages[a.ID] = &attachmentImage{phase: attachmentImageReady, Image: n.NewBitmap(image.NewRGBA(image.Rect(0, 0, 200, 160)))}
	ui.Frame()
	for i := 0; i < 20; i++ {
		ui.Advance(time.Second / 60)
		img, _ = ui.Find("预览图片：picture.png")
		after, _ = ui.Find("After")
		if math.Abs(float64(after.Y-img.Y-img.H-gap)) > 1 {
			t.Fatal("image and its following content use separate height clocks", i, img, after, gap)
		}
	}
}

func TestRepeatedResourceResultsRemainIndependentRows(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.Model.Collapsed["activity:s/tool"] = false
	a := domain.Attachment{ID: "same", Name: "shared.go", MIME: "text/plain", Size: 301}
	item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Result", Parts: []domain.ContentPart{{Kind: "file", Attachment: &a}, {Kind: "file", Attachment: &a}}}
	ui := n.NewTester(func(c *n.Context) { n.Column(c).Fill().Gap(8).Children(func() { v.item(c, c.Theme(), item) }) }, 600, 300)
	ui.Advance(time.Second)
	count := 0
	for _, s := range ui.Texts() {
		if s == a.Name {
			count++
		}
	}
	if count != 2 {
		t.Fatal("repeated resource results are not independently represented", count, ui.Texts())
	}
	item.Parts = item.Parts[:1]
	ui.Frame()
	ui.Advance(time.Second)
	if _, ok := ui.Find("预览文件：shared.go"); !ok {
		t.Fatal("remaining occurrence disappears")
	}
}

func TestReconnectedMetadataStartsAsHistoryInsteadOfReplaying(t *testing.T) {
	m := layoutModel()
	m.ConnectionEpoch = "before"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	m.ConnectionEpoch = "after"
	d := m.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "recovered", Kind: "activity", Name: "Recovered summary", Presentation: "context", Text: "Previously completed", Final: true, TurnID: "old"})
	d.Turns = []domain.Turn{{ID: "old", Status: "completed"}}
	m.Details["s"] = d
	ui.Frame()
	if v.toolArrivals.motions["recovered"] != nil || v.toolArrivals.motions["turn-footer:old"] != nil {
		t.Fatal("reconnect hydration replays previously completed metadata")
	}
}

func TestToolHeadingChangesRetainTheOldInkBeforeBlending(t *testing.T) {
	v := New(layoutModel(), nil, "")
	item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Old tool", Text: "Stable result"}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Background(c.Theme().Background).Children(func() { n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) }) })
	}, 600, 300)
	ui.Advance(time.Second)
	before := copyFrame(ui)
	item.Name = "New tool"
	ui.Frame()
	if !samePixels(before, ui.Image()) {
		t.Fatal("tool heading replaces its ink instantly")
	}
	ui.Advance(time.Second)
	if samePixels(before, ui.Image()) {
		t.Fatal("updated heading never appears")
	}
}

func TestMovingAnImageResultDoesNotClipItsExistingPixels(t *testing.T) {
	v := New(layoutModel(), nil, "")
	v.Model.Collapsed["activity:s/tool"] = false
	a := domain.Attachment{ID: "file", Name: "before.go", MIME: "text/plain", Size: 301}
	b := domain.Attachment{ID: "image", Name: "image.png", MIME: "image/png"}
	im := image.NewRGBA(image.Rect(0, 0, 224, 112))
	for y := 0; y < 112; y++ {
		for x := 0; x < 224; x++ {
			im.SetRGBA(x, y, color.RGBA{R: 220, G: 40, B: 40, A: 255})
		}
	}
	v.attachmentImages = map[string]*attachmentImage{b.ID: {phase: attachmentImageReady, Image: n.NewBitmap(im)}}
	item := domain.Item{ID: "tool", Kind: "tool", Presentation: "mcp", Name: "Result", Parts: []domain.ContentPart{{Kind: "file", Attachment: &a}, {Kind: "image", Attachment: &b}}}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() { n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) }) })
	}, 600, 500)
	ui.Advance(time.Second)
	item.Parts = item.Parts[1:]
	ui.Frame()
	for i := 0; i < 20; i++ {
		ui.Advance(time.Second / 60)
		r, ok := ui.Find("预览图片：image.png")
		if !ok {
			t.Fatal("image disappeared")
		}
		p := ui.Image().RGBAAt(int(r.X+r.W/2), int(r.Y+r.H/2))
		if p.R < 180 || p.G > 90 || p.B > 90 {
			t.Fatal("moving image loses existing pixels", i, r, p)
		}
	}
}
