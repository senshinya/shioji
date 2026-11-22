package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestTranscriptStatusGlyphsAlignWithToolHeadingDot(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			v := toolTailFixture(1)
			compaction := domain.Item{ID: "compact", Kind: "activity", Presentation: "compaction", TurnID: "t", Name: "正在压缩上下文", Status: "running"}
			d := v.Model.Details["s"]
			d.Items = append(d.Items, compaction)
			d.Requests = []domain.PendingRequest{{WorkID: "child-question", State: "pending", Kind: "questions"}, {WorkID: "child-approval", State: "pending", Kind: "approval"}}
			v.Model.Details["s"] = d
			v.runningGroup = "tools"
			group := transcriptRow{group: "tools", count: 1, item: domain.Item{TurnID: "t"}}
			type sample struct {
				name  string
				green bool
				draw  func(*n.Context, *n.Theme)
			}
			samples := []sample{
				{"折叠标题", true, func(c *n.Context, theme *n.Theme) { v.toolGroupTitle(c, theme, group, 0) }},
				{"展开标题", false, func(c *n.Context, theme *n.Theme) {
					open := group
					open.toolReveal = 1
					v.toolGroupTitle(c, theme, open, 0)
				}},
				{"组末状态", true, func(c *n.Context, theme *n.Theme) {
					v.toolProgressLine(c, theme, "正在执行命令", nil, c.Now(), 1)
				}},
				{"独立状态", true, func(c *n.Context, theme *n.Theme) {
					v.progressStatus(c, theme, runningActivity{label: "进行中"}, v.runningProgress)
				}},
				{"正在压缩", true, func(c *n.Context, theme *n.Theme) { v.contextCompactionItem(c, theme, compaction) }},
				{"压缩完成", false, func(c *n.Context, theme *n.Theme) {
					done := compaction
					done.Final, done.Status, done.Name = true, "completed", "上下文已压缩"
					v.contextCompactionItem(c, theme, done)
				}},
				{"压缩待确认", false, func(c *n.Context, theme *n.Theme) {
					unknown := compaction
					unknown.ID = "old-compact"
					v.contextCompactionItem(c, theme, unknown)
				}},
				{"Agent 活动", false, func(c *n.Context, theme *n.Theme) {
					v.activityItem(c, theme, domain.Item{Kind: "activity", Presentation: "agents", Name: "启动 Agent"})
				}},
				{"等待工具", false, func(c *n.Context, theme *n.Theme) {
					v.activityItem(c, theme, domain.Item{Kind: "activity", Presentation: "wait", Name: "等待 Tools"})
				}},
				{"搜索活动", false, func(c *n.Context, theme *n.Theme) {
					v.activityItem(c, theme, domain.Item{Kind: "activity", Presentation: "search", Name: "Search files"})
				}},
				{"目标续跑", false, func(c *n.Context, theme *n.Theme) {
					v.activityItem(c, theme, domain.Item{Kind: "activity", Presentation: domain.GoalContinuationPresentation, Name: "目标自动续跑"})
				}},
				{"请求来源", false, func(c *n.Context, theme *n.Theme) {
					v.requestSource(c, theme, domain.Item{Kind: "request", Presentation: "approval", Status: "pending", Text: "需要授权"}, false, nil)
				}},
			}
			for _, work := range []domain.WorkActivity{
				{ID: "child-running", State: "running"},
				{ID: "child-completed", State: "completed"},
				{ID: "child-question", State: "waiting"},
				{ID: "child-approval", State: "waiting"},
				{ID: "child-unknown", State: "unknown"},
			} {
				samples = append(samples, sample{work.ID, work.State == "running", func(c *n.Context, theme *n.Theme) {
					n.Row(c).Gap(6).AlignItems(n.Baseline).Children(func() {
						label := v.workLabel(work)
						v.workStatus(c, theme, work, label, n.Font{Family: theme.Font, Size: 11, Weight: 400})
						n.Text(c, label).FontSize(11).FixedLineHeight(20)
					})
				}})
			}
			const origin, band, scale = 16, 24, 2
			var fontFamily string
			ui := n.NewTester(func(c *n.Context) {
				fontFamily = c.Theme().Font
				if v.runningProgress == nil {
					v.runningProgress = &runningProgress{started: c.Now(), label: "进行中"}
				}
				n.Column(c).Fill().Padding(origin).Background(c.Theme().Background).Children(func() {
					for _, s := range samples {
						key := c.Key(s.name)
						n.Column(key).Height(band).Shrink(0).Children(func() { s.draw(key, c.Theme()) })
					}
				})
			}, 480, origin*2+band*len(samples))
			ui.SetScale(scale)
			ui.SetDark(dark)
			ui.SetPreferences(n.Preferences{ReduceMotion: true})
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("activity-glyphs-dark-%v.png", dark))

			for _, label := range []string{"启动 Agent", "等待 Tools", "Search files"} {
				bounds, ok := ui.Find(label)
				want, _ := n.ShapeText(label, n.Font{Family: fontFamily, Size: 11, Weight: 400}, 0).Size()
				if label != "启动 Agent" {
					// The tester resolves these two labels to the disclosure row.
					bounds.W -= 22
				}
				if !ok || math.Abs(float64(bounds.W-want)) > .5 {
					t.Errorf("%s: caption width %.2f, want 11 DIP text width %.2f", label, bounds.W, want)
				}
			}

			img := ui.Image()
			bg := img.RGBAAt((origin-2)*scale, origin*scale)
			var referenceX, referenceY float64
			for i, s := range samples {
				top := (origin + i*band) * scale
				left, right, upper, lower := img.Bounds().Max.X, -1, img.Bounds().Max.Y, -1
				for y := top; y < top+20*scale; y++ {
					for x := (origin - 2) * scale; x < (origin+16)*scale; x++ {
						p := img.RGBAAt(x, y)
						visible := max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 20
						if s.green {
							visible = int(p.G) > int(p.R)+12 && int(p.G) > int(p.B)+9
						}
						if visible {
							left, right = min(left, x), max(right, x)
							upper, lower = min(upper, y), max(lower, y)
						}
					}
				}
				if right < 0 {
					t.Errorf("%s: no visible marker", s.name)
					continue
				}
				if !s.green && s.name != "child-unknown" {
					height := float64(lower-upper+1) / scale
					if math.Abs(height-8) > 1 {
						t.Errorf("%s: icon ink height %.2f DIP, want 8 DIP", s.name, height)
					}
				}
				x, y := float64(left+right+1)/(2*scale)-origin, float64(upper+lower+1-2*top)/(2*scale)
				if i == 0 {
					referenceX, referenceY = x, y
					if math.Abs(x-8) > .5 {
						t.Errorf("tool heading dot shifted from the middle of its slot: %.2f", x)
					}
				} else if math.Abs(x-referenceX) > 1 || math.Abs(y-referenceY) > 1 {
					t.Errorf("%s: ink center %.2f, %.2f differs from heading dot %.2f, %.2f", s.name, x, y, referenceX, referenceY)
				}
			}
		})
	}
}
