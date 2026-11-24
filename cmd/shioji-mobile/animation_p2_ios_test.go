package main

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestMobileSwitchSettlesIn160msNot500msSpring(t *testing.T) {
	on := false
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			mobileSwitch(c.Key("sw"), "示例开关", &on, nil)
		})
	}, 120, 80)
	tester.Advance(time.Second)

	on = true
	tester.Frame()
	// After 180ms, a 160ms EaseOut switch must be completely settled (not a 500ms spring).
	tester.Advance(180 * time.Millisecond)
	if tester.FramePending() {
		t.Fatal("mobileSwitch still animating at 180ms (using 500ms spring instead of 160ms EaseOut)")
	}
}

func TestMobileTranscriptLatestAnimatesWidthGrowthAndEntrance(t *testing.T) {
	m := app.NewModel()
	a := &mobileApp{model: m}
	list := ui.ListState{}
	prefetch := transcriptPrefetch{}
	caption := "最新消息"
	live := false
	show := false

	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			dock := ui.Box(c.Key("dock")).Absolute().Left(0).Right(0).Bottom(0).Height(40)
			if show {
				a.transcriptLatest(c.Key("return-latest"), dock, "s1", domain.SessionDetail{}, &list, &prefetch, false, caption, nil, live)
			}
		})
	}, 360, 400)
	tester.Advance(time.Second)

	// 1. Appearing must trigger an enter transition.
	show = true
	tester.Frame()
	tester.Advance(50 * time.Millisecond)
	if !tester.FramePending() {
		t.Fatal("transcriptLatest snapped in without an enter transition")
	}
	tester.Advance(time.Second)

	before, ok := tester.Find("回到最新消息")
	if !ok {
		t.Fatal("missing transcriptLatest button")
	}

	// 2. Growing caption width must animate smoothly rather than snapping via max(target, shown).
	caption = "正在执行一段较长的命令输出任务"
	live = true
	tester.Frame()
	tester.Advance(40 * time.Millisecond)
	mid, _ := tester.Find("回到最新消息")
	tester.Advance(time.Second)
	end, _ := tester.Find("回到最新消息")
	if end.W <= before.W+20 {
		t.Fatalf("caption did not grow button width enough: before=%g end=%g", before.W, end.W)
	}
	if math.Abs(float64(mid.W-end.W)) < 3 {
		t.Fatalf("transcriptLatest snapped to full grown width instead of animating: before=%g mid=%g end=%g", before.W, mid.W, end.W)
	}
}

func TestMobileHistoryMessageHeightClearsActiveAfterEditCancels(t *testing.T) {
	editing := false
	tall := false
	var heightAt30ms float32
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			mobileHistoryMessageHeight(c, editing, false, func(body *ui.Context) {
				h := float32(40)
				if tall {
					h = 90
				}
				ui.Box(body.Key("msg")).FillWidth().Height(h)
			})
			ui.Box(c.Key("below")).FillWidth().Height(10).DrawOver(func(_ *ui.Painter, r ui.Rect) { heightAt30ms = r.Y })
		})
	}, 320, 200)
	editing = true
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	editing = false
	tester.Frame()
	tester.Advance(400 * time.Millisecond)

	tall = true
	tester.Frame()
	tester.Advance(30 * time.Millisecond)
	// If *active was cleared after settling, the plain wrapper grows directly to 90 without
	// being permanently stuck in a 280ms AnimatedContentHeight clip.
	if heightAt30ms < 89 {
		t.Fatalf("mobileHistoryMessageHeight stayed permanently stuck in AnimatedContentHeight clip after edit ended: y=%g", heightAt30ms)
	}
}

func TestMobileCopyButtonShowsFeedbackAfterIdle(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			mobileCopyButton(c.Key("copy"), "复制地址", "https://example.com")
		})
	}, 160, 80)
	// Simulate the user sitting idle for 5 seconds without rebuilding frames.
	tester.Advance(5 * time.Second)
	if _, ok := tester.Find("复制地址"); !ok {
		t.Fatal("missing copy button")
	}
	tester.Click("复制地址")
	tester.Frame()
	if _, ok := tester.Find("已复制"); !ok {
		t.Fatal("copy button failed to show 已复制 feedback after idling >2s (stale c.Now() capture)")
	}
	tester.Advance(2100 * time.Millisecond)
	if _, ok := tester.Find("已复制"); ok {
		t.Fatal("copy button feedback did not clear after 2s")
	}
}

func TestMobileSessionListPrunesFoldingWhenGroupRemoved(t *testing.T) {
	var foldingMap *map[string]bool
	showSecond := true
	a := &mobileApp{model: app.NewModel()}
	tester := ui.NewTester(func(c *ui.Context) {
		foldingMap = ui.Local(c.Root(), "session-group-folding", func() map[string]bool { return map[string]bool{} })
		groups := []mobileSessionGroup{
			{key: "ws:1", title: "Alpha", rows: []mobileSessionRow{{session: domain.Session{ID: "s1", Title: "One"}}}},
		}
		if showSecond {
			groups = append(groups, mobileSessionGroup{key: "ws:2", title: "Beta", rows: []mobileSessionRow{{session: domain.Session{ID: "s2", Title: "Two"}}}})
		}
		pruneSessionGroupFolding(c, groups)
		ui.Column(c).Fill().Children(func() {
			menu := contextMenuFrame{zoom: 1}
			for _, g := range groups {
				a.sessionGroup(c, &menu, g)
			}
		})
	}, 320, 320)
	tester.Advance(time.Second)

	// Start collapsing the second group, then remove it mid-fold.
	if _, ok := tester.Find("Beta"); !ok {
		t.Fatal("missing Beta group header")
	}
	tester.Click("Beta")
	tester.Advance(30 * time.Millisecond)
	if !(*foldingMap)["ws:2"] {
		t.Fatal("expected ws:2 to be tracked as folding mid-collapse")
	}
	showSecond = false
	tester.Frame()
	if len(*foldingMap) != 0 {
		t.Fatalf("folding map leaked removed group key: %v", *foldingMap)
	}
}
