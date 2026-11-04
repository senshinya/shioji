package ui

import (
	"fmt"
	"image"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func toolHistoryModelItems() []domain.Item {
	var items []domain.Item
	for i := 0; i < 24; i++ {
		items = append(items, domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: "Earlier conversation paragraph."})
	}
	for i := 0; i < 8; i++ {
		items = append(items, domain.Item{ID: fmt.Sprint("tool", i), TurnID: "t", Kind: "tool", Name: fmt.Sprint("command ", i), Text: "output\nsecond line\nthird line"})
	}
	return append(items, domain.Item{ID: "reply", TurnID: "t", Kind: "assistant", Text: "Finished."})
}

func copyFrame(ui *n.Tester) *image.RGBA {
	im := ui.Image()
	copy := image.NewRGBA(im.Bounds())
	copy.Pix = append(copy.Pix[:0], im.Pix...)
	return copy
}

func TestToolAndTokenHoverOnlyChangesText(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, label := range []string{"1 次工具调用", "本轮用量"} {
			m := layoutModel()
			d := m.Details["s"]
			d.Items = []domain.Item{{ID: "tool", TurnID: "t", Kind: "tool", Name: "command"}, {ID: "reply", TurnID: "t", Kind: "assistant", Text: "Done"}}
			d.Turns = []domain.Turn{{ID: "t", Status: "completed"}}
			m.Details["s"] = d
			ui := n.NewTester(New(m, nil, "").View, 1440, 900)
			ui.SetDark(dark)
			r, _ := ui.Find(label)
			before := copyFrame(ui)
			ui.Move(r.X+r.W/2, r.Y+r.H/2)
			ui.Advance(180 * time.Millisecond)
			x, y := int(r.X+r.W/2), int(r.Y+2)
			if before.RGBAAt(x, y) != ui.Image().RGBAAt(x, y) {
				t.Fatalf("%s still paints a hover background (dark=%v)", label, dark)
			}
			changed := false
			for y := int(r.Y + 4); y < int(r.Y+r.H-3); y++ {
				for x := int(r.X + 20); x < int(r.X+110) && x < int(r.X+r.W-4); x++ {
					changed = changed || before.RGBAAt(x, y) != ui.Image().RGBAAt(x, y)
				}
			}
			if !changed {
				t.Fatalf("%s text has no hover contrast change (dark=%v)", label, dark)
			}
		}
	}
}

func TestWorkspaceHeadingInsetsAndNoPerWorkspacePlus(t *testing.T) {
	ui := n.NewTester(New(layoutModel(), nil, "").View, 1440, 900)
	label, _ := ui.Find("工作区")
	button, _ := ui.Find("创建工作区")
	ink := (n.Font{Family: "system-ui", Size: 11}).InkBounds("工作区")
	left := label.X + ink.X - 8
	// The 16px plus is centered in a 24px target; its SVG stroke ends at 16.75/20.
	right := float32(256) - (button.X + 4 + 16*16.75/20)
	if math.Abs(float64(left-right)) > 1 {
		t.Fatalf("workspace heading has unequal visible insets: left=%v right=%v", left, right)
	}
	r, _ := ui.Find("Example")
	ui.Move(r.X+r.W/2, r.Y+r.H/2)
	ui.Advance(180 * time.Millisecond)
	if ui.HasText("在 Example 新建会话") {
		t.Fatal("workspace name still exposes its own plus button")
	}
}

func TestToolDisclosureKeepsClickedHeadingInPlace(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		m := layoutModel()
		d := m.Details["s"]
		d.Items = toolHistoryModelItems()
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 700)
		ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
		before, _ := ui.Find("8 次工具调用")
		for i := 0; i < 2; i++ {
			if err := ui.Click("8 次工具调用"); err != nil {
				t.Fatal(err)
			}
			for _, delay := range []time.Duration{40, 180} {
				ui.Advance(delay * time.Millisecond)
				after, ok := ui.Find("8 次工具调用")
				if !ok || math.Abs(float64(after.Y-before.Y)) > .5 {
					t.Fatalf("tool disclosure moved its clicked heading: before=%+v after=%+v", before, after)
				}
			}
		}
	}
}

func TestPopoverActuallyExpandsFromItsTrigger(t *testing.T) {
	m := layoutModel()
	m.Readiness.Permissions = []string{"read-only", "default"}
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	trigger, _ := ui.Find("Permission")
	baseline := copyFrame(ui)
	if err := ui.Click("Permission"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(40 * time.Millisecond)
	entering, _ := ui.Find("菜单选项")
	ui.Advance(180 * time.Millisecond)
	settled, _ := ui.Find("菜单选项")
	if entering.W >= settled.W-.5 {
		t.Fatalf("popover has no expansion: entering=%+v settled=%+v", entering, settled)
	}
	ex, ey := entering.X+entering.W/2, entering.Y+entering.H/2
	sx, sy := settled.X+settled.W/2, settled.Y+settled.H/2
	tx, ty := trigger.X+trigger.W/2, trigger.Y+trigger.H/2
	if (ex-sx)*(tx-sx)+(ey-sy)*(ty-sy) <= 1 {
		t.Fatalf("popover does not originate at its trigger: trigger=%+v entering=%+v settled=%+v", trigger, entering, settled)
	}
	ui.Key(0, n.KeyEscape)
	ui.Advance(40 * time.Millisecond)
	differences := func() int {
		count := 0
		frame := ui.Image()
		for y := int(settled.Y + 2); y < int(settled.Y+settled.H-2); y++ {
			for x := int(settled.X + 2); x < int(settled.X+settled.W-2); x++ {
				if frame.RGBAAt(x, y) != baseline.RGBAAt(x, y) {
					count++
				}
			}
		}
		return count
	}
	if differences() == 0 {
		t.Fatal("popover disappears immediately instead of animating its exit")
	}
	ui.Advance(180 * time.Millisecond)
	if differences() != 0 {
		t.Fatal("popover exit leaves stale content")
	}
}

func TestToolGroupActuallyAnimatesBothDirections(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = toolHistoryModelItems()[24:]
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if err := ui.Click("8 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(40 * time.Millisecond)
	entering, _ := ui.Find("command 0")
	ui.Advance(180 * time.Millisecond)
	settled, _ := ui.Find("command 0")
	if entering.Y >= settled.Y-.5 {
		t.Fatalf("tools have no content entrance transition: entering=%+v settled=%+v", entering, settled)
	}
	if err := ui.Click("8 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(40 * time.Millisecond)
	if !ui.HasText("command 0") {
		t.Fatal("tool content disappears before its exit animation")
	}
	ui.Advance(180 * time.Millisecond)
	if ui.HasText("command 0") {
		t.Fatal("tool content remains after collapse completed")
	}
}

func TestToolDisclosureCanReverseAndUserScrollReleasesAnchor(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = toolHistoryModelItems()
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 700)
	if err := ui.Click("8 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(60 * time.Millisecond)
	ui.Frame()
	key := "tool-group:s/tool0"
	before, _ := v.toolMotions[key].sample(ui.Now())
	if err := ui.Click("8 次工具调用"); err != nil {
		t.Fatal(err)
	}
	after, _ := v.toolMotions[key].sample(ui.Now())
	if math.Abs(float64(before-after)) > .001 {
		t.Fatalf("reversing tool disclosure restarted its animation: %v -> %v", before, after)
	}
	ui.Advance(40 * time.Millisecond)
	if err := ui.Click("8 次工具调用"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(180 * time.Millisecond)
	heading, _ := ui.Find("8 次工具调用")
	ui.Scroll(1300, 400, 0, 80)
	ui.Frame()
	moved, ok := ui.Find("8 次工具调用")
	if !ok || math.Abs(float64(moved.Y-heading.Y)) < 5 {
		t.Fatalf("the disclosure anchor prevents user scrolling: before=%+v after=%+v", heading, moved)
	}
}
