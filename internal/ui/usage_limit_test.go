package ui

import (
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestUsageLimitComposerAnimatesArrivalAndDeparture(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor = 2
	m.ConnectionEpoch = "connected"
	s := &m.Snapshot.Sessions[0]
	m.Drafts[m.DraftKey()] = "待发送草稿"
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1100, 750)
	ui.Advance(400 * time.Millisecond)
	editor, _ := ui.Find("消息输入")
	before, _ := ui.Find("消息编辑区")

	d := m.Details[s.ID]
	s.Status = domain.ExecutionFailed
	d.Session = *s
	d.Turns = []domain.Turn{{ID: "limit", Status: domain.ExecutionFailed, UsageLimit: &domain.UsageLimit{}}}
	m.Details[s.ID] = d
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	opening, _ := ui.Find("消息编辑区")
	ui.Advance(350 * time.Millisecond)
	opened, _ := ui.Find("消息编辑区")
	if _, ok := ui.Find("重置时间暂未知"); !ok {
		t.Fatal("usage-limit notice is missing from the live composer")
	}
	if !(before.H < opening.H && opening.H < opened.H) {
		t.Fatalf("dock did not expand through intermediate heights: %.1f %.1f %.1f", before.H, opening.H, opened.H)
	}
	atOpen, _ := ui.Find("消息输入")
	if math.Abs(float64(editor.Y-atOpen.Y)) > 1 {
		t.Fatalf("editor moved while the notice opened: %v -> %v", editor, atOpen)
	}
	saveNativeSkillImage(t, ui, "usage-limit-light.png")
	ui.SetDark(true)
	ui.Frame()
	saveNativeSkillImage(t, ui, "usage-limit-dark.png")

	s.Status, d.Session.Status = domain.ExecutionRunning, domain.ExecutionRunning
	d.Turns = append(d.Turns, domain.Turn{ID: "retry", Status: domain.ExecutionRunning})
	m.Details[s.ID] = d
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	closing, _ := ui.Find("消息编辑区")
	ui.Advance(280 * time.Millisecond)
	closed, _ := ui.Find("消息编辑区")
	if _, ok := ui.Find("重置时间暂未知"); ok {
		t.Fatal("notice stayed visible after the next turn started")
	}
	if !(closed.H < closing.H && closing.H < opened.H) {
		t.Fatalf("dock did not collapse through intermediate heights: %.1f %.1f %.1f", opened.H, closing.H, closed.H)
	}
	if m.Drafts[m.DraftKey()] != "待发送草稿" {
		t.Fatal("notice altered the draft")
	}
}
