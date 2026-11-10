package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"strings"
	"testing"
	"time"
)

func TestChildDetailUserMessageIsRightAlignedAndReplyIsCompact(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := sessionWorkFixture(t)
			v.Model.Preferences.ReplySize = 18
			ui.SetDark(dark)
			key := v.Model.WorkKey("s", "agent-a")
			d := v.Model.WorkDetails[key]
			d.Items[0].Item.Text = "HHH"
			v.Model.WorkDetails[key] = d
			ui.Frame()
			ui.Click("查看子 Agent：检查边界条件")
			ui.Advance(time.Second)
			port, _ := ui.Find("子 Agent内容")
			bubble, ok := ui.Find("检查根节点与空子树")
			if !ok {
				t.Fatal("no child user message")
			}
			if math.Abs(float64((port.X+port.W-12)-(bubble.X+bubble.W))) > 1 {
				t.Error("child user bubble is not aligned to the conversation right edge", port, bubble)
			}
			reply, ok := ui.Find("HHH")
			if !ok || reply.H > 21 {
				t.Fatal("child reply inherits oversized main Markdown typography", reply)
			}
		})
	}
}
func TestWorkListsKeepTheirTrailingArrowInsideTheReadingInset(t *testing.T) {
	_, ui := sessionWorkFixture(t)
	for _, name := range []string{"查看子 Agent：检查边界条件", "查看后台任务：运行回归测试"} {
		row, ok := ui.Find(name)
		if !ok {
			t.Fatal("missing work row", name)
		}
		panel, _ := ui.Find("更改侧栏")
		if row.X+row.W > panel.X+panel.W-6+.5 {
			t.Fatal("full-width row plus margin escapes its right inset", name, row, panel)
		}
	}
}
func TestChildBackAndTitleUseOneBaselineAndCompactGap(t *testing.T) {
	_, ui := childRequestsFixture(t)
	ui.Click("查看来源：检查边界条件")
	ui.Advance(time.Second)
	button, ok := ui.Find("返回子 Agent列表")
	if !ok {
		t.Fatal("no back control")
	}
	// The title belongs to the same back control, avoiding the extra empty half
	// of an independent square icon button before the title.
	if button.W < 60 {
		t.Fatal("title is detached from its back control", button)
	}
}

func TestRequestOriginJumpGlyphMatchesNameInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, name := range []string{"HHH", "国国国", "国 H 国"} {
			t.Run(fmt.Sprintf("%v/%s", dark, name), func(t *testing.T) {
				v, _ := childRequestsFixture(t)
				d := v.Model.Details["s"]
				d.Works[0].Title = name
				v.Model.Details["s"] = d
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Background(c.Theme().Background).Padding(20).Children(func() { v.requestOrigin(c, c.Theme(), d.Requests[0]) })
				}, 300, 90)
				ui.SetDark(dark)
				ui.SetScale(2)
				ui.Advance(time.Second)
				b, _ := ui.Find("查看来源：" + name)
				// Find the actual text and icon runs from a shared row, separating them at
				// their whitespace rather than assuming a matching SVG slot means matching ink.
				im := ui.Image()
				bg := im.RGBAAt(0, 0)
				var groups [][4]int
				for x := int((b.X + 4) * 2); x < int((b.X+b.W-4)*2); x++ {
					top, bottom := im.Bounds().Max.Y, -1
					for y := int(b.Y * 2); y < int((b.Y+b.H)*2); y++ {
						p := im.RGBAAt(x, y)
						if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
							top = min(top, y)
							bottom = max(bottom, y)
						}
					}
					if bottom >= 0 {
						if len(groups) == 0 || x-groups[len(groups)-1][1] > 3 {
							groups = append(groups, [4]int{x, x, top, bottom})
						} else {
							g := &groups[len(groups)-1]
							g[1] = x
							g[2] = min(g[2], top)
							g[3] = max(g[3], bottom)
						}
					}
				}
				if len(groups) < 2 {
					t.Fatal("missing separated name/jump glyph ink", groups)
				}
				icon := groups[len(groups)-1]
				top, bottom := im.Bounds().Max.Y, -1
				for _, g := range groups[:len(groups)-1] {
					top = min(top, g[2])
					bottom = max(bottom, g[3])
				}
				if math.Abs(float64((icon[2]+icon[3])-(top+bottom))/4) > 1 || math.Abs(float64((icon[3]-icon[2])-(bottom-top))/2) > 1 {
					t.Fatal("jump glyph differs from name in center/visible height", groups)
				}
			})
		}
	}
}

func TestRequestJumpIconInkIsCenteredInItsGlyphSlot(t *testing.T) {
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Padding(20).Background(c.Theme().Background).Children(func() { n.Icon(c, referenceExternalIcon).Size(20, 20).TextColor(c.Theme().Text).Label("跳转图标") })
	}, 80, 80)
	ui.SetScale(2)
	box, _ := ui.Find("跳转图标")
	im := ui.Image()
	bg := im.RGBAAt(0, 0)
	left, right, top, bottom := im.Bounds().Max.X, -1, im.Bounds().Max.Y, -1
	for y := int(box.Y * 2); y < int((box.Y+box.H)*2); y++ {
		for x := int(box.X * 2); x < int((box.X+box.W)*2); x++ {
			p := im.RGBAAt(x, y)
			if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
				left = min(left, x)
				right = max(right, x)
				top = min(top, y)
				bottom = max(bottom, y)
			}
		}
	}
	if math.Abs(float64(left+right+1)/4-float64(box.X+box.W/2)) > .25 || math.Abs(float64(top+bottom+1)/4-float64(box.Y+box.H/2)) > .25 {
		t.Fatal("jump icon path is not centered within its viewBox", left, right, top, bottom, box)
	}
}

func TestLongChildTitleKeepsTheStatusInsideNarrowSidebar(t *testing.T) {
	v, ui := childRequestsFixture(t)
	d := v.Model.Details["s"]
	name := strings.Repeat("边界检查", 12)
	d.Works[0].Title = name
	v.Model.Details["s"] = d
	v.Model.InspectorWidth = 280
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("查看子 Agent：" + name); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	caption, ok := ui.Find("等待回答")
	panel, _ := ui.Find("更改侧栏")
	if !ok || caption.W < 40 || caption.X+caption.W > panel.X+panel.W-11 {
		t.Fatal("long title squeezes its status caption", caption, panel)
	}
}
