package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestConversationOutlineJumpsWithoutChangingDraft(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < 8; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("u", i), Kind: "user", Text: fmt.Sprintf("Prompt %d", i)}, domain.Item{ID: fmt.Sprint("a", i), Kind: "assistant", Text: strings.Repeat("A readable explanation.\n\n", 12)})
	}
	m.Details["s"] = d
	m.Drafts["session:s"] = "keep draft"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	if _, ok := ui.Find("跳转到消息 1"); !ok {
		t.Fatal("outline missing")
	}
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	tick, _ := ui.Find("跳转到消息 1")
	ui.Move(tick.X+3, tick.Y+tick.H/2)
	ui.Advance(400 * time.Millisecond)
	if !ui.Focused("消息输入") || m.Drafts["session:s"] != "keep draft" {
		t.Fatal("outline hover stole draft or focus")
	}
	if err := ui.Click("跳转到消息 1"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(500 * time.Millisecond)
	first, _ := v.lists["s"].Visible()
	if first > 2 {
		t.Fatal("outline did not jump to first prompt", first)
	}
	if _, ok := ui.Find("回到最新"); !ok {
		t.Fatal("reading mode has no return to latest")
	}
	ui.Click("回到最新")
	ui.Advance(500 * time.Millisecond)
	if !v.lists["s"].AtEnd() {
		t.Fatal("could not resume bottom following")
	}
}

func TestConversationOutlineHidesInNarrowPane(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "a", Kind: "user", Text: "a"}, {ID: "b", Kind: "user", Text: "b"}, {ID: "c", Kind: "user", Text: "c"}, {ID: "d", Kind: "user", Text: "d"}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 900, 650)
	if _, ok := ui.Find("跳转到消息 1"); ok {
		t.Fatal("rail overlaps narrow reading gutter")
	}
}

func TestLongConversationSelectionSurvivesVirtualRowUnmount(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < 120; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("u", i), Kind: "user", Text: fmt.Sprintf("Prompt number %03d", i)}, domain.Item{ID: fmt.Sprint("a", i), Kind: "assistant", Text: strings.Repeat("Readable **Markdown** with a [link](https://example.com).\n\n", 3)})
	}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	v.lists["s"].ScrollTo(0, n.Start)
	ui.Frame()
	ui.Click("Prompt number 000")
	ui.Key(n.Cmd, n.KeyA)
	ui.Key(n.Cmd, n.KeyC)
	selected := ui.Clipboard()
	if selected != "Prompt number 000" {
		t.Fatal("initial selection failed", selected)
	}
	v.lists["s"].ScrollToEnd()
	ui.Frame()
	if ui.HasText("Prompt number 000") {
		t.Fatal("selected history row remained mounted")
	}
	ui.SetClipboard("sentinel")
	ui.Key(n.Cmd, n.KeyC)
	if ui.Clipboard() != selected {
		t.Fatal("selection lost when row unmounted")
	}
}

func TestConversationOutlineNeedsFourSentUserMessages(t *testing.T) {
	for count := 0; count <= 4; count++ {
		m := layoutModel()
		d := m.Details["s"]
		d.Items = nil
		for i := 0; i < count; i++ {
			d.Items = append(d.Items, domain.Item{ID: fmt.Sprintf("u%d", i), Kind: "user", Text: "Sent prompt"})
		}
		for i := 0; i < 6; i++ {
			d.Items = append(d.Items, domain.Item{ID: fmt.Sprintf("a%d", i), Kind: "assistant", Text: "Continuation"})
		}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.Advance(time.Second)
		_, visible := ui.Find("对话导航")
		if visible != (count >= 4) {
			t.Fatal("rail visibility does not follow sent user message count", count, visible)
		}
	}
}

func TestConversationOutlineHoverFansOutToDifferentLengths(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < 9; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprintf("u%d", i), Kind: "user", Text: fmt.Sprintf("Prompt %d", i)})
	}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("消息输入")
	center, _ := ui.Find("跳转到消息 5")
	ui.Move(center.X+3, center.Y+center.H/2)
	ui.Advance(220 * time.Millisecond)
	image := ui.Image()
	want := []int{6, 10, 14, 20, 26, 20, 14, 10, 6}
	for i, width := range want {
		line, ok := ui.Find(fmt.Sprintf("跳转到消息 %d", i+1))
		if !ok {
			t.Fatal("navigation tick missing", i)
		}
		// Edge hit boxes extend into the gutter; visible lines retain the
		// agreed 10 DIP pitch around the unchanged middle tick.
		x, y := int(line.X), int(center.Y+center.H/2+float32(i-4)*10)
		background := image.RGBAAt(x+32, y)
		length := 0
		for dx := 0; dx < 30; dx++ {
			if image.RGBAAt(x+dx, y) != background {
				length++
			}
		}
		if length != width {
			t.Fatal("hovered rail does not match the graduated reference", i, length, width)
		}
	}
	if !ui.Focused("消息输入") {
		t.Fatal("rail hover stole input focus")
	}
}

func TestConversationOutlineLastTickUsesItsWholeHitArea(t *testing.T) {
	for _, count := range []int{9, 64, 120} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			v, ui := outlineHitUI(count)
			v.outline.List.ScrollToEnd()
			ui.Frame()
			track, _ := ui.Find("对话导航")
			for _, index := range []int{count} {
				tick, ok := ui.Find(fmt.Sprintf("跳转到消息 %d", index))
				if !ok || tick.W <= 0 || tick.H <= 0 {
					t.Fatal("tick missing", index)
				}
				for _, dx := range []float32{3, 18, 28, 33} {
					for dy := float32(1); dy < tick.H; dy++ {
						ui.Move(tick.X+dx, tick.Y+dy)
						ui.Advance(20 * time.Millisecond)
						if v.outline.Hover != index-1 {
							t.Errorf("tick %d cannot be hovered (%g,%g) DIP from its top-left: hover=%d, tick=%+v, track=%+v", index, dx, dy, v.outline.Hover, tick, track)
						}
					}
				}
			}
		})
	}
}

func outlineHitUI(count int) (*View, *n.Tester) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < count; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprintf("u%d", i), Kind: "user", Text: fmt.Sprintf("Prompt %d", i)}, domain.Item{ID: fmt.Sprintf("a%d", i), Kind: "assistant", Text: strings.Repeat("A readable explanation.\n\n", 8)})
	}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	v.lists["s"].ScrollToEnd()
	ui.Frame()
	ui.Advance(time.Second)
	return v, ui
}

func TestConversationOutlineEdgePaddingBelongsToEdgeTicks(t *testing.T) {
	v, ui := outlineHitUI(9)
	ui.Click("消息输入")
	track, _ := ui.Find("对话导航")
	for _, edge := range []struct {
		index int
		y     float32
	}{{0, track.Y + 1}, {8, track.Y + track.H - 1}} {
		ui.Move(track.X+18, edge.y)
		ui.Advance(400 * time.Millisecond)
		if v.outline.Hover != edge.index {
			t.Errorf("edge padding dropped hover: want %d, got %d", edge.index, v.outline.Hover)
		}
		if !ui.Focused("消息输入") {
			t.Fatal("edge hover took composer focus")
		}
	}
	ui.ClickAt(track.X+18, track.Y+track.H-1)
	ui.Advance(time.Second)
	message, ok := ui.Find("Prompt 8")
	if !ok || message.H <= 0 || message.Y > 200 {
		t.Fatal("clicking the bottom edge did not return to the last prompt", message)
	}
	if !ui.Focused("消息输入") {
		t.Fatal("bottom-edge navigation took composer focus")
	}
}

func TestLongConversationOutlineFollowsReader(t *testing.T) {
	v, ui := outlineHitUI(120)
	current := v.outline.Current
	first, last := v.outline.List.Visible()
	saveNativeSkillImage(t, ui, "outline-reader-latest.png")
	if current < 60 || current < first || current >= last {
		t.Fatalf("latest reader marker %d is outside outline viewport [%d,%d)", current, first, last)
	}
	for _, target := range []int{0, 120, 220} {
		v.lists["s"].ScrollTo(target, n.Start)
		ui.Frame()
		ui.Advance(time.Second)
		current = v.outline.Current
		first, last = v.outline.List.Visible()
		if current < first || current >= last {
			t.Fatalf("reader marker %d is outside outline viewport [%d,%d) after main scroll to %d", current, first, last, target)
		}
	}
}

func TestOutlineResumesPendingFollowAfterHoverAndResize(t *testing.T) {
	v, ui := outlineHitUI(120)
	ui.Click("消息输入")
	track, _ := ui.Find("对话导航")
	ui.Move(track.X+18, track.Y+track.H/2)
	ui.Advance(400 * time.Millisecond)
	v.lists["s"].ScrollTo(0, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	// Main reading position changes while the user browses the rail. The
	// rail must defer following, without losing that pending change.
	ui.Move(900, 200)
	ui.Advance(time.Second)
	first, last := v.outline.List.Visible()
	if v.outline.Current != 0 || first > 0 || last <= 0 {
		t.Fatal("outline failed to catch up after pointer leave", v.outline.Current, first, last)
	}
	v.lists["s"].ScrollTo(120, n.Start)
	ui.Frame()
	ui.Advance(time.Second)
	current := v.outline.Current
	ui.SetSize(1440, 600)
	ui.Advance(time.Second)
	first, last = v.outline.List.Visible()
	if current < first || current >= last {
		t.Fatal("resizing hid the unchanged current marker", current, first, last)
	}
	if !ui.Focused("消息输入") {
		t.Fatal("following the reader stole composer focus")
	}
}

func TestOutlineManualBrowsingDoesNotMoveConversation(t *testing.T) {
	v, ui := outlineHitUI(120)
	track, _ := ui.Find("对话导航")
	mainBefore, _ := v.lists["s"].Visible()
	before, _ := v.outline.List.Visible()
	ui.Move(track.X+18, track.Y+track.H/2)
	ui.Wheel(track.X+18, track.Y+track.H/2, 0, -180)
	ui.Advance(time.Second)
	after, _ := v.outline.List.Visible()
	mainAfter, _ := v.lists["s"].Visible()
	if after >= before || mainAfter != mainBefore {
		t.Fatal("rail wheel did not remain independent", before, after, mainBefore, mainAfter)
	}
	ui.Move(900, 200)
	ui.Advance(time.Second)
	settled, _ := v.outline.List.Visible()
	if settled != after {
		t.Fatal("leaving the rail snapped back during manual browsing", after, settled)
	}
}

func TestOutlineReappearsAtReaderPosition(t *testing.T) {
	v, ui := outlineHitUI(120)
	ui.SetSize(900, 900)
	ui.Advance(time.Second)
	ui.SetSize(1440, 900)
	ui.Advance(time.Second)
	first, last := v.outline.List.Visible()
	if v.outline.Current < first || v.outline.Current >= last {
		t.Fatal("outline remount lost current reader marker", v.outline.Current, first, last)
	}
}

func TestLatestOutlineTickIsNotCoveredByTheComposer(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, height := range []int{600, 820} {
			for _, queued := range []bool{false, true} {
				t.Run(fmt.Sprintf("dark=%v/height=%d/queued=%v", dark, height, queued), func(t *testing.T) {
					v, ui := outlineHitUI(93)
					ui.SetDark(dark)
					ui.SetSize(1277, height)
					if queued {
						q := domain.QueueState{SessionID: "s", Revision: 1}
						for i := 0; i < 8; i++ {
							q.Items = append(q.Items, domain.QueueItem{ID: fmt.Sprint("queued-", i), Input: domain.Submission{OperationID: fmt.Sprint("queued-", i), Text: fmt.Sprint("待发送消息 ", i)}})
						}
						v.Model.SetQueue(q)
						v.Model.QueueExpanded["s"] = true
					}
					ui.Frame()
					ui.Advance(time.Second)
					v.lists["s"].ScrollToEnd()
					ui.Advance(time.Second)
					ui.Click("消息输入")
					ui.Advance(time.Second)
					tick, ok := ui.Find("跳转到消息 93")
					if !ok {
						t.Fatal("latest message has no outline target")
					}
					dock, dockOK := ui.Find("会话底部面板")
					toolbar, toolbarOK := ui.Find("会话顶栏")
					rail, railOK := ui.Find("对话导航")
					saveNativeSkillImage(t, ui, fmt.Sprintf("outline-dock-dark-%v-height-%d-queue-%v.png", dark, height, queued))
					if !dockOK || !toolbarOK || !railOK {
						t.Fatal("missing fixed conversation surfaces")
					}
					if rail.Y < toolbar.Y+toolbar.H || rail.Y+rail.H > dock.Y || tick.Y+tick.H > dock.Y {
						t.Fatalf("latest marker is underneath a fixed surface: tick=%+v rail=%+v toolbar=%+v dock=%+v", tick, rail, toolbar, dock)
					}
					ui.Move(tick.X+18, tick.Y+tick.H/2)
					ui.Advance(400 * time.Millisecond)
					if v.outline.Hover != 92 {
						t.Fatal("latest marker exists but cannot be hovered", v.outline.Hover)
					}
					if !ui.Focused("消息输入") {
						t.Fatal("outline hover took composer focus")
					}
				})
			}
		}
	}
}
