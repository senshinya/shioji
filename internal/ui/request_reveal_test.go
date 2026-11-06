package ui

import (
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestNewRequestSourceRevealsProgressively(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	m.Details["s"] = d
	m.Drafts["session:s"] = "保留普通草稿"
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 900)
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	d.Items = []domain.Item{{ID: "request:q", Kind: "request", Text: "需要回答", Status: "pending"}}
	m.Details["s"] = d
	ui.Frame()
	// The button's accessible name stays complete even while its text reveals.
	if !v.visibleOutputAnimating() {
		t.Fatal("new request source has no progressive reveal to finish")
	}
	before, _ := ui.Find("需要回答")
	var prefix bool
	for i := 0; i < 30; i++ {
		ui.Advance(time.Second / 60)
		if current, _ := ui.Find("需要回答"); current.Y != before.Y || current.H != before.H {
			t.Fatal("request reveal changes the source row's position or height", before, current)
		}
		for _, text := range ui.Texts() {
			if text == "需" || text == "需要" || text == "需要回" {
				prefix = true
			}
		}
	}
	if !prefix || !ui.HasText("需要回答") {
		t.Fatal("request source has no readable intermediate prefix or never settles", ui.Texts())
	}
	if v.visibleOutputAnimating() {
		t.Fatal("finished request source keeps output animating")
	}
	if !ui.Focused("消息输入") || m.Drafts["session:s"] != "保留普通草稿" {
		t.Fatal("request reveal steals focus or changes the ordinary draft")
	}
}

func TestRequestSourceHistoryAndReducedMotionDoNotReplay(t *testing.T) {
	for _, mode := range []string{"history", "reduce", "reconnect"} {
		t.Run(mode, func(t *testing.T) {
			m := layoutModel()
			d := m.Details["s"]
			item := domain.Item{ID: "request:q", Kind: "request", Text: "需要回答", Status: "pending"}
			d.Items = nil
			if mode == "history" {
				d.Items = []domain.Item{item}
			}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1440, 900)
			if mode == "reduce" {
				ui.SetPreferences(n.Preferences{ReduceMotion: true})
			}
			if mode != "history" {
				d.Items = []domain.Item{item}
				m.Details["s"] = d
				if mode == "reconnect" {
					m.ConnectionEpoch = "new"
				}
				ui.Frame()
			}
			if !ui.HasText("需要回答") || v.visibleOutputAnimating() {
				t.Fatal("existing or reduced-motion request source replays its entrance")
			}
		})
	}
}

func TestRequestSourceHitTargetHugsItsContent(t *testing.T) {
	v := requestMotionModel()
	v.Model.Collapsed["s"] = true
	d := v.Model.Details["s"]
	d.Requests = append(d.Requests, domain.PendingRequest{ID: "new", SessionID: "s", Kind: "questions", Title: "需要回答", State: "pending", Questions: []domain.Question{{ID: "q", Text: "Another question", Custom: true}}})
	d.Items = []domain.Item{{ID: "request:new", Kind: "request", Text: "需要回答", Status: "pending"}}
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1440, 900)
	ui.Advance(time.Second)
	source, ok := ui.Find("需要回答")
	if !ok || source.H != 20 || source.W > 160 {
		t.Fatal("request source hover target spans the transcript", source)
	}
	ui.ClickAt(source.X+300, source.Y+source.H/2)
	ui.Advance(300 * time.Millisecond)
	if !v.Model.Collapsed["s"] || v.Model.Pager["s"] != 0 {
		t.Fatal("blank transcript space opens a request")
	}
	ui.Click("需要回答")
	ui.Advance(300 * time.Millisecond)
	if v.Model.Collapsed["s"] || v.Model.Pager["s"] != 2 {
		t.Fatal("compact source target does not open its request")
	}
}
