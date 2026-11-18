package ui

import (
	"image/color"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestLightKeyboardFocusContrastsWithTheWindowAndSidebar(t *testing.T) {
	var focus n.Color
	m := layoutModel()
	ui := n.NewTester(func(c *n.Context) {
		focus = applyAppearance(c, m.Preferences).Focus
	}, 400, 260)
	ui.SetDark(false)
	ui.Advance(time.Second)
	rgb := func(nc n.Color) color.RGBA {
		return color.RGBA{R: nc.R, G: nc.G, B: nc.B, A: nc.A}
	}
	for _, hex := range []string{"#ffffff", "#ededed", "#f4f4f4"} {
		if ratio := selectionContrast(rgb(focus), rgb(n.Hex(hex))); ratio < 3 {
			t.Errorf("focus contrast on %s is %.2f", hex, ratio)
		}
	}
}

func TestSidebarToolbarButtonsShareTheNativeHitSize(t *testing.T) {
	ui := n.NewTester(New(layoutModel(), nil, "").View, 850, 600)
	ui.Advance(time.Second)
	for _, label := range []string{"搜索", "工作区菜单", "创建工作区"} {
		r, ok := ui.Find(label)
		if !ok || r.W != 28 || r.H != 28 {
			t.Errorf("%s has a different hit size: %v", label, r)
		}
	}
}

func TestFileLinkUsesTheCompactDialogReadingInset(t *testing.T) {
	m := layoutModel()
	v := New(m, nil, "")
	v.fileLink = &fileLinkState{session: "s", workspace: "w", epoch: m.ConnectionEpoch, path: "docs/review.pdf", error: "所属设备暂不可用。"}
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)
	panel, ok := ui.Find("文件链接")
	title, titled := ui.Find("review.pdf")
	if !ok || !titled || math.Abs(float64(title.X-panel.X-17)) > 1 {
		t.Fatalf("file link header is off the compact dialog reading line: panel=%v title=%v", panel, title)
	}
}

func TestInformationPopoversShareTopContentInset(t *testing.T) {
	ui := n.NewTester(New(layoutModel(), nil, "").View, 1240, 820)
	ui.Advance(time.Second)
	usage, ok := ui.Find("上下文用量")
	if !ok {
		t.Fatal("missing context entry point")
	}
	ui.Move(usage.X+usage.W/2, usage.Y+usage.H/2)
	ui.Advance(time.Second)
	contextPanel, shown := ui.Find("上下文详情")
	if !shown {
		t.Fatal(ui.Texts())
	}
	titleEdge := func(panel n.Rect) int {
		img := ui.Image()
		right := img.RGBAAt(int(panel.X+panel.W-14), int(panel.Y+10))
		for y := int(panel.Y + 2); y < int(panel.Y+30); y++ {
			for x := int(panel.X + 12); x < int(panel.X+100); x++ {
				if delta := int(img.RGBAAt(x, y).R) - int(right.R); delta < -55 || delta > 55 {
					return y - int(panel.Y)
				}
			}
		}
		t.Fatalf("missing visible information title in %v", panel)
		return 0
	}
	contextInset := titleEdge(contextPanel)
	ui.Move(0, 0)
	ui.Advance(time.Second)
	if err := ui.Click("当前设备"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	devicePanel, shown := ui.Find("当前设备状态")
	if !shown {
		t.Fatal(ui.Texts())
	}
	deviceInset := titleEdge(devicePanel)
	if math.Abs(float64(deviceInset-contextInset)) > 1 {
		t.Fatalf("information title insets differ: device=%v context=%v", deviceInset, contextInset)
	}
}
