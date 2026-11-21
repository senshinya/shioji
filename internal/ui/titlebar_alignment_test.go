package ui

import (
	"fmt"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
)

func TestMainToolbarContentAlignsWithSidebarFrame(t *testing.T) {
	for _, width := range []int{1440, 850} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := layoutModel()
			ui := n.NewTester(New(m, nil, "").View, width, 820)
			ui.SetDark(width < 900)
			ui.Advance(time.Second)
			check := func(center float32, labels ...string) {
				t.Helper()
				for _, label := range labels {
					r, ok := ui.Find(label)
					if !ok || math.Abs(float64(r.Y+r.H/2-center)) > .5 {
						t.Errorf("%s does not center at y=%g: %+v", label, center, r)
					}
				}
			}
			const toolbarCenter = 22 // A 28 DIP control starting at the sidebar frame's 8 DIP top.
			const trafficLightCenter = 29
			check(trafficLightCenter, "搜索")
			check(toolbarCenter, "会话名称", "当前设备", "展开侧栏")
			if err := ui.Click("展开侧栏"); err != nil {
				t.Fatal(err)
			}
			ui.Move(0, 0)
			ui.Advance(time.Second)
			check(toolbarCenter, "更改", "刷新更改", "关闭更改侧栏")
			saveNativeSkillImage(t, ui, fmt.Sprintf("window-toolbar-%d.png", width))

			detail := m.Details["s"]
			detail.Plan = &domain.Plan{Narrative: "核对目录"}
			m.Details["s"] = detail
			ui.Frame()
			ui.Advance(time.Second)
			check(toolbarCenter, "文件更改", "更改标题", "刷新更改", "关闭更改侧栏")
			if err := ui.Click("新建会话"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			check(trafficLightCenter, "搜索")
			check(toolbarCenter, "当前设备")
		})
	}
}
