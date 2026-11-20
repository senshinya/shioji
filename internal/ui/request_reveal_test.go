package ui

import (
	"bytes"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestRequestStatusTransitionsKeepTheOriginalRowAndIconStill(t *testing.T) {
	for _, mode := range []struct {
		kind, title, confirmed, receipt string
		reduce                          bool
	}{
		{"questions", "需要回答", "已回答 · 逐项验收", "逐项验收", false},
		{"approval", "允许执行此命令？", "允许执行此命令？ · 允许这一次", "允许这一次", false},
		{"permissions", "允许额外访问？", "允许额外访问？ · 已允许本轮访问", "已允许本轮访问", false},
		{"questions", "需要回答", "已回答 · 逐项验收", "逐项验收", true},
	} {
		name := mode.kind
		if mode.reduce {
			name += "-reduced-motion"
		}
		t.Run(name, func(t *testing.T) {
			m := layoutModel()
			m.Collapsed["s"] = true
			d := m.Details["s"]
			r := domain.PendingRequest{ID: "q", SessionID: "s", TurnID: "t", Kind: mode.kind, Title: mode.title, State: "pending", Revision: 1}
			d.Requests = []domain.PendingRequest{r}
			d.Turns = []domain.Turn{{ID: "t", Status: domain.ExecutionRunning}}
			d.Items = []domain.Item{
				{ID: "request:q", TurnID: "t", Kind: "request", Text: mode.title, Status: "pending", Order: 1},
				{ID: "later", TurnID: "t", Kind: "assistant", Text: "后续消息保留原位。", Final: true, Order: 2},
			}
			m.Details["s"] = d
			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1050, 720)
			ui.SetPreferences(n.Preferences{ReduceMotion: mode.reduce})
			ui.Advance(time.Second)
			original, ok := ui.Find(mode.title)
			if !ok {
				t.Fatal("original request is missing")
			}
			later, _ := ui.Find("后续消息保留原位。")
			pixels := func(offset, width int) []byte {
				img := ui.Image()
				x, y := int(original.X)+offset, int(original.Y)
				var crop []byte
				for row := y; row < y+20; row++ {
					at := img.PixOffset(x, row)
					crop = append(crop, img.Pix[at:at+width*4]...)
				}
				return crop
			}
			icon := pixels(0, 14)
			for _, change := range []struct {
				state domain.RequestState
				label string
			}{
				{domain.RequestSubmitting, mode.title + " · 提交中"},
				{domain.RequestAnswered, mode.confirmed},
			} {
				d.Requests[0].State = change.state
				d.Items[0].Status = string(change.state)
				v.requestAction(d.Requests[0]).busy = change.state == domain.RequestSubmitting
				if change.state == domain.RequestAnswered {
					d.Requests[0].Receipt = []domain.ContentPart{{Text: mode.receipt}}
				}
				m.Details["s"] = d
				ui.Frame()
				ui.Advance(45 * time.Millisecond)
				current, present := ui.Find(change.label)
				if !present || current.X != original.X || current.Y != original.Y || math.Abs(float64(current.H-original.H)) > .5 {
					t.Fatal("status changes the original row position or size", current, original, ui.Texts())
				}
				if next, _ := ui.Find("后续消息保留原位。"); math.Abs(float64(next.Y-later.Y)) > .5 {
					t.Fatal("status moves later output", next, later)
				}
				if !bytes.Equal(icon, pixels(0, 14)) {
					t.Fatal("status moves or fades the request icon")
				}
				middle := pixels(19, 210)
				ui.Advance(time.Second)
				settled := pixels(19, 210)
				if bytes.Equal(middle, settled) != mode.reduce {
					t.Fatalf("%s: text motion does not match Reduce Motion=%v", change.state, mode.reduce)
				}
			}
		})
	}
}

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
