package ui

import (
	"fmt"
	"image"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestNewToolsRevealTheirHeightAndFadeTheirInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = []domain.Item{{ID: "user", TurnID: "turn", Kind: "user", Text: "检查命令", Final: true}}
			d.Turns = []domain.Turn{{ID: "turn", Status: "running"}}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1200, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)
			for i := 0; i < 2; i++ {
				before, _ := ui.Find("回复信息")
				name := fmt.Sprint("stream command ", i)
				d.Items = append(d.Items, domain.Item{ID: name, TurnID: "turn", Kind: "tool", Presentation: "command", Name: name, Status: "running"})
				m.Details["s"] = d
				ui.Frame()
				first, _ := ui.Find("回复信息")
				ui.Advance(60 * time.Millisecond)
				middle, _ := ui.Find("回复信息")
				label, ok := ui.Find(name)
				if !ok {
					t.Fatal("tool label missing during reveal")
				}
				ink := toolLabelContrast(ui.Image(), label, dark)
				ui.Advance(time.Second)
				end, _ := ui.Find("回复信息")
				label, _ = ui.Find(name)
				finalInk := toolLabelContrast(ui.Image(), label, dark)
				if math.Abs(float64(first.Y-before.Y)) > 1 || middle.Y <= first.Y+1 || middle.Y >= end.Y-1 {
					t.Fatal("tool arrives with its entire height instead of revealing", i, before, first, middle, end)
				}
				if ink >= finalInk-3 {
					t.Fatal("tool label has no gradual fade", ink, finalInk)
				}
			}
		})
	}
}

func toolLabelContrast(im *image.RGBA, r n.Rect, dark bool) int {
	contrast := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+150) && x < int(r.X+r.W); x++ {
			p := im.RGBAAt(x, y)
			value := int(p.R)
			if !dark {
				value = 255 - value
			}
			contrast = max(contrast, value)
		}
	}
	return contrast
}

func TestRunningCommandOutputAlwaysShowsItsLastLine(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	item := domain.Item{ID: "command", Kind: "tool", Presentation: "command", Name: "streaming command", Status: "running"}
	item.Text = strings.Repeat("\n", 30) + "tail 30"
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Background(c.Theme().Background).Children(func() {
			n.Column(c).FillWidth().Gap(8).Label("命令输出检查").Children(func() { v.item(c, c.Theme(), item) })
		})
	}, 600, 350)
	ui.Advance(time.Second)
	assertTail := func() {
		t.Helper()
		container, _ := ui.Find("命令输出检查")
		ink := 0
		frame := ui.Image()
		for y := int(container.Y + container.H - 26); y < int(container.Y+container.H-8); y++ {
			for x := int(container.X + 10); x < int(container.X+180); x++ {
				if frame.RGBAAt(x, y).R < 150 {
					ink++
				}
			}
		}
		if ink < 10 {
			t.Fatal("running command does not show its final line", ink, container)
		}
	}
	assertTail()
	ui.Scroll(300, 100, 0, -80)
	ui.Frame()
	for i := 30; i < 35; i++ {
		item.Text = strings.Repeat("\n", i+1) + fmt.Sprint("tail ", i+1)
		ui.Frame()
		ui.Advance(300 * time.Millisecond)
		assertTail()
	}
	item.Final, item.Status = true, "completed"
	ui.Frame()
	ui.Scroll(300, 100, 0, -80)
	ui.Frame()
	before := copyFrame(ui)
	ui.Advance(time.Second)
	// Compare content, excluding the independently fading overlay scrollbar.
	after := ui.Image()
	for y := 28; y < 188; y++ {
		for x := 10; x < 580; x++ {
			if before.RGBAAt(x, y) != after.RGBAAt(x, y) {
				t.Fatal("completed output keeps forcing the tail")
			}
		}
	}
}

func TestCompletedCommandBatchStillFollowsItsFinalOutput(t *testing.T) {
	for _, initial := range []string{"", "first output"} {
		t.Run(fmt.Sprint(len(initial)), func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			item := domain.Item{ID: "command", Kind: "tool", Presentation: "command", Name: "batch command", Text: initial, Status: "running"}
			ui := n.NewTester(func(c *n.Context) {
				n.Column(c).Fill().Background(c.Theme().Background).Children(func() {
					n.Column(c).FillWidth().Gap(8).Label("batch output").Children(func() { v.item(c, c.Theme(), item) })
				})
			}, 600, 350)
			ui.Advance(time.Second)
			item.Text, item.Final, item.Status = strings.Repeat("\n", 30)+"final batch tail\n", true, "completed"
			ui.Frame()
			ui.Advance(time.Second)
			box, _ := ui.Find("batch output")
			ink := toolLabelContrast(ui.Image(), n.Rect{X: 10, Y: box.Y + box.H - 26, W: 160, H: 18}, false)
			if ink < 80 {
				t.Fatal("completion drops a batch's pending follow before its output lays out", initial, ink, box)
			}
		})
	}
}

func TestNewCompletedToolBatchStartsAtItsLastLine(t *testing.T) {
	v, ui := progressFollowFixture()
	d := v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "batch-command", TurnID: "turn", Kind: "tool", Name: "batch command", Presentation: "command", Status: "completed", Final: true, Text: strings.Repeat("\n", 30) + "new batch tail\n"})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	end, ok := v.historyRowBounds["batch-command"]
	if !ok || ui.HasText("已完成") {
		t.Fatal("missing batch row or premature group receipt")
	}
	// Completion of this call does not close the running turn's tool group.
	// Its output still ends one 8 px row gap above the running footer.
	ink := toolLabelContrast(ui.Image(), n.Rect{X: end.X + 24 + 10, Y: end.Y + end.H - 34, W: 160, H: 18}, false)
	if ink < 80 {
		t.Fatal("coalesced start/completion opens new live output at its first line", ink, end)
	}
}

func TestCommandOutputTrailingNewlineDoesNotAddBottomPadding(t *testing.T) {
	v := New(layoutModel(), nil, "")
	item := domain.Item{ID: "command", Kind: "tool", Name: "command", Text: "last output line", Status: "completed", Final: true}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).FillWidth().Gap(8).Label("output container").Children(func() { v.item(c, c.Theme(), item) })
	}, 600, 350)
	ui.Advance(time.Second)
	before, _ := ui.Find("output container")
	item.Text += "\n\n"
	ui.Frame()
	ui.Advance(time.Second)
	after, _ := ui.Find("output container")
	if math.Abs(float64(before.H-after.H)) > .5 {
		t.Fatal("terminal line endings inflate the visible bottom inset", before, after)
	}
}

func TestCommandAppendKeepsExistingLinesStillWhileTheViewportGrows(t *testing.T) {
	v := New(layoutModel(), nil, "")
	item := domain.Item{ID: "command", Kind: "tool", Name: "command", Text: "existing output", Status: "running"}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Background(c.Theme().Background).Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) })
		})
	}, 600, 350)
	ui.Advance(time.Second)
	before := copyFrame(ui)
	item.Text += "\nappended output"
	ui.Frame()
	middleInk := 0
	for frame := 0; frame < 12; frame++ {
		current := ui.Image()
		for y := 36; y < 54; y++ {
			for x := 10; x < 150; x++ {
				if before.RGBAAt(x, y) != current.RGBAAt(x, y) {
					t.Fatal("growing viewport scrolls away and repaints existing output", frame)
				}
			}
		}
		if frame == 3 {
			middleInk = toolLabelContrast(current, n.Rect{X: 10, Y: 54, W: 150, H: 18}, false)
		}
		ui.Advance(time.Second / 60)
	}
	ui.Advance(300 * time.Millisecond)
	finalInk := toolLabelContrast(ui.Image(), n.Rect{X: 10, Y: 54, W: 150, H: 18}, false)
	if middleInk >= finalInk-3 {
		t.Fatal("newly appended text has no fade independent of the old output", middleInk, finalInk)
	}
}

func TestCommandAppendAtHeightLimitGlidesInsteadOfJumping(t *testing.T) {
	v := New(layoutModel(), nil, "")
	item := domain.Item{ID: "command", Kind: "tool", Name: "command", Status: "running"}
	for i := 0; i < 30; i++ {
		item.Text += fmt.Sprintf("output %02d\n", i)
	}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Background(c.Theme().Background).Children(func() {
			n.Column(c).FillWidth().Gap(8).Children(func() { v.item(c, c.Theme(), item) })
		})
	}, 600, 350)
	ui.Advance(time.Second)
	before := copyFrame(ui)
	item.Text += "new final line\n"
	ui.Frame()
	firstFrame := ui.Image()
	for y := 40; y < 120; y++ {
		for x := 10; x < 180; x++ {
			if before.RGBAAt(x, y) != firstFrame.RGBAAt(x, y) {
				t.Fatal("appended output jumps to a new offset on its first frame")
			}
		}
	}
	ui.Advance(80 * time.Millisecond)
	middle := copyFrame(ui)
	ui.Advance(time.Second)
	changed := false
	finalFrame := ui.Image()
	for y := 40; y < 120; y++ {
		for x := 10; x < 180; x++ {
			changed = changed || middle.RGBAAt(x, y) != finalFrame.RGBAAt(x, y)
		}
	}
	if !changed {
		t.Fatal("appended output has no remaining scroll interpolation")
	}
}

func TestToolArrivalFollowsLatestWithoutMovingTheHistoryReader(t *testing.T) {
	v, ui := progressFollowFixture()
	before, _ := ui.Find("进度 35")
	d := v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "command", TurnID: "turn", Kind: "tool", Presentation: "command", Name: "streaming command", Status: "running"})
	v.Model.Details["s"] = d
	ui.Frame()
	first, _ := ui.Find("进度 35")
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("进度 35")
	ui.Advance(time.Second)
	end, _ := ui.Find("进度 35")
	if math.Abs(float64(first.Y-before.Y)) > 1 || middle.Y >= first.Y || middle.Y <= end.Y || end.Y >= before.Y-20 {
		t.Fatal("tool arrival does not smoothly follow measured growth", before, first, middle, end)
	}
	// First stdout arrives after the command header, then grows past its cap.
	d.Items[len(d.Items)-1].Text = strings.Repeat("first output\n", 20)
	v.Model.Details["s"] = d
	ui.Frame()
	for i := 0; i < 30; i++ {
		ui.Advance(time.Second / 60)
	}
	if !v.lists["s"].FollowingEnd() || !v.lists["s"].AtEnd() {
		t.Fatal("command output growth loses following")
	}
	ui.Scroll(800, 350, 0, -180)
	ui.Frame()
	marker, ok := ui.Find("进度 30")
	if !ok || v.lists["s"].FollowingEnd() {
		t.Fatal("fixture did not enter history")
	}
	d.Items = append(d.Items, domain.Item{ID: "next-command", TurnID: "turn", Kind: "tool", Name: "next command", Status: "running"})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(time.Second)
	after, ok := ui.Find("进度 30")
	if !ok || math.Abs(float64(after.Y-marker.Y)) > 1 || v.lists["s"].FollowingEnd() {
		t.Fatal("new tool pulls the reader out of history", marker, after)
	}
}

func TestToolArrivalDoesNotReplayOnHistoryOrReducedMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		v, ui := progressFollowFixture()
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		d := v.Model.Details["s"]
		d.Items = append(d.Items, domain.Item{ID: "command", TurnID: "turn", Kind: "tool", Name: "command", Status: "running"})
		v.Model.Details["s"] = d
		if !reduced {
			v.Model.FoldToolHistory["s"] = true
		}
		ui.Frame()
		if len(v.lists["s"].Reveals) != 0 {
			t.Fatal("history/reduced motion replays tool arrivals", reduced)
		}
	}
}

func TestConversationToolbarDoesNotCoverPartOfTheTopScrollbar(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, dragging := range []bool{false, true} {
			t.Run(fmt.Sprint(dark, "/", dragging), func(t *testing.T) {
				_, ui := progressFollowFixture()
				ui.SetDark(dark)
				ui.Scroll(1150, 300, 0, -10000)
				ui.Frame()
				// Newly measured virtual rows can correct the first estimated
				// offset; scroll again so the fixture really reaches the top.
				ui.Scroll(1150, 300, 0, -10000)
				ui.Frame()
				first, ok := ui.Find("进度 0")
				if !ok || math.Abs(float64(first.Y-80)) > 1 {
					t.Fatal("fixture did not reach the actual top", first)
				}
				left := 1191
				if dragging {
					ui.Press(1194, 20)
					ui.Frame()
					left = 1189
				}
				for x := left; x < 1197; x++ {
					a, b := ui.Image().RGBAAt(x, 20), ui.Image().RGBAAt(x, 140)
					if a != b {
						t.Fatal("toolbar clips the upper thumb into a second, narrower strip", x, a, b)
					}
				}
			})
		}
	}
}

func TestConversationScrollbarAvoidsTheWindowCornersAtBothEnds(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			_, ui := progressFollowFixture()
			ui.SetDark(dark)
			for _, direction := range []float32{-10000, 10000} {
				ui.Scroll(1150, 300, 0, direction)
				ui.Frame()
				ui.Scroll(1150, 300, 0, direction)
				ui.Frame()
				checkCorners := func() {
					t.Helper()
					for _, y := range []int{3, 4, 5, 6, 7, 812, 813, 814, 815, 816} {
						if a, b := ui.Image().RGBAAt(1194, y), ui.Image().RGBAAt(1199, y); a != b {
							t.Fatal("thumb enters the window's rounded corner", direction, y, a, b)
						}
					}
				}
				checkCorners()
				y := 15
				if direction > 0 {
					y = 805
				}
				if a, b := ui.Image().RGBAAt(1194, y), ui.Image().RGBAAt(1199, y); a == b {
					t.Fatal("fixture did not show the scrollbar near its endpoint", direction, y)
				}
				ui.Press(1194, float32(y))
				endY := float32(-20)
				if direction > 0 {
					endY = 840
				}
				ui.Move(1194, endY)
				ui.Frame()
				checkCorners()
				ui.Release(1194, endY)
			}
		})
	}
}
