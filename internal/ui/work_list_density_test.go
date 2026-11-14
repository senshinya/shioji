package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestWorkListDensityTracksSummaryContent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			title := func(kind string, i int) string {
				if kind == "agent" {
					return []string{"检查节点引用", "整理检查结果", "检查边界条件"}[i]
				}
				return []string{"检查代码格式", "运行单元测试", "运行回归测试"}[i]
			}
			v, ui := sessionWorkFixture(t)
			d := v.Model.Details["s"]
			d.Works = nil
			for _, kind := range []string{"agent", "background"} {
				for i, summary := range []string{"", "检查完成，未发现问题。", "正在检查边界条件"} {
					state := "completed"
					if i == 2 {
						state = "running"
					}
					d.Works = append(d.Works, domain.WorkActivity{ID: fmt.Sprintf("%s-%d", kind, i), Kind: kind, TurnID: "t", Title: title(kind, i), Latest: summary, State: state})
				}
			}
			v.Model.Details["s"] = d
			v.Model.InspectorLayout.Groups = []app.InspectorGroup{{Views: []string{"changes", "agents"}, Selected: "agents"}, {Views: []string{"tasks", "background"}, Selected: "background"}}
			v.changesFor("s").WorkGroups = nil
			ui.SetDark(dark)
			ui.Frame()
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("work-density-%v.png", dark))
			for _, kind := range []string{"agent", "background"} {
				name := "子 Agent"
				if kind == "background" {
					name = "后台任务"
				}
				plain, _ := ui.Find("查看" + name + "：" + title(kind, 0))
				detail, _ := ui.Find("查看" + name + "：" + title(kind, 1))
				if math.Abs(float64(plain.H-36)) > .5 || math.Abs(float64(detail.H-56)) > .5 {
					t.Error("empty summary reserves a second line", plain, detail)
				}
			}
			// A real summary arriving/removing changes height continuously, and the
			// title/status remain aligned to the first line throughout.
			label := "查看子 Agent：" + title("agent", 0)
			d.Works[0].Latest = "新增的完成摘要"
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(70 * time.Millisecond)
			middle, _ := ui.Find(label)
			if middle.H <= 36 || middle.H >= 56 {
				t.Error("summary arrival height jumps", middle)
			}
			ui.Advance(300 * time.Millisecond)
			expanded, _ := ui.Find(label)
			if math.Abs(float64(expanded.H-56)) > .5 {
				t.Error("summary does not expand row", expanded)
			}
			d.Works[0].Latest = ""
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(70 * time.Millisecond)
			middle, _ = ui.Find(label)
			if middle.H <= 36 || middle.H >= 56 {
				t.Error("summary removal height jumps", middle)
			}
			ui.Advance(300 * time.Millisecond)
			collapsed, _ := ui.Find(label)
			if math.Abs(float64(collapsed.H-36)) > .5 {
				t.Error("summary removal leaves empty space", collapsed)
			}
		})
	}
}
