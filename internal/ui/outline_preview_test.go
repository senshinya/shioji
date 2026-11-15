package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func previewModel(reply string) *View {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < 9; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("u", i), Kind: "user", Text: fmt.Sprint("消息 ", i)}, domain.Item{ID: fmt.Sprint("a", i), Kind: "assistant", Text: reply})
	}
	m.Details["s"] = d
	return New(m, nil, "")
}

func TestOutlinePreviewUsesMeasuredMiddleAnchorAndWarmBrowsing(t *testing.T) {
	for _, reply := range []string{"短回复", strings.Repeat("原生回复内容需要换行，", 40)} {
		t.Run(fmt.Sprint(len(reply)), func(t *testing.T) {
			v := previewModel(reply)
			ui := n.NewTester(v.View, 1440, 900)
			ui.Advance(time.Second)
			ui.Click("消息输入")
			ui.Type("keep draft")
			tick, _ := ui.Find("跳转到消息 5")
			ui.Move(tick.X+3, tick.Y+tick.H/2)
			ui.Advance(199 * time.Millisecond)
			if _, ok := ui.Find("对话导航预览"); ok {
				t.Fatal("preview opened early")
			}
			ui.Advance(time.Millisecond)
			ui.Advance(time.Second / 60) // the hidden measurement frame precedes presentation
			panel, ok := ui.Find("对话导航预览")
			if !ok {
				t.Fatal("preview missing at 200 ms")
			}
			if math.Abs(float64(panel.Y+panel.H/2-tick.Y-tick.H/2)) > 1 {
				t.Fatal("preview not vertically centered", panel, tick)
			}
			if panel.W != 320 || panel.Y < 56 || panel.Y+panel.H > 900-v.dockHeight-8 {
				t.Fatal("preview escaped reading boundary", panel, v.dockHeight)
			}
			next, _ := ui.Find("跳转到消息 6")
			ui.Move(next.X+3, next.Y+next.H/2)
			if _, ok := ui.Find("对话导航预览"); !ok {
				t.Fatal("continuous browsing repeated initial delay")
			}
			ui.Key(0, n.KeyEscape)
			ui.Advance(time.Second)
			if _, ok := ui.Find("对话导航预览"); ok {
				t.Fatal("Escape reopened preview under stationary pointer")
			}
			if !ui.Focused("消息输入") || v.Model.Drafts[v.Model.DraftKey()] != "keep draft" {
				t.Fatal("hover stole draft or focus")
			}
		})
	}
}

func TestOutlineKeyboardNavigationKeepsOrdinaryDraft(t *testing.T) {
	v := previewModel("回复")
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	ui.Click("消息输入")
	ui.Type("ordinary draft")
	focused := false
	for tries := 0; tries < 80; tries++ {
		ui.Key(n.Shift, n.KeyTab)
		for i := 1; i <= 9; i++ {
			if ui.Focused(fmt.Sprintf("跳转到消息 %d", i)) {
				focused = true
				break
			}
		}
		if focused {
			break
		}
	}
	if !focused {
		t.Fatal("rail cannot be reached by keyboard")
	}
	ui.Key(0, n.KeyHome)
	ui.Advance(250 * time.Millisecond)
	if !ui.Focused("跳转到消息 1") {
		t.Fatal("Home did not focus first tick")
	}
	ui.Key(0, n.KeyEnd)
	ui.Advance(250 * time.Millisecond)
	if !ui.Focused("跳转到消息 9") {
		t.Fatal("End did not focus last tick")
	}
	if v.Model.Drafts[v.Model.DraftKey()] != "ordinary draft" {
		t.Fatal("keyboard navigation changed draft")
	}
}
