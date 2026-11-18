package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestWorkRowsDoNotReserveMissingSummarySpace(t *testing.T) {
	for _, kind := range []string{"agents", "background"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []float32{300, 380} {
				t.Run(fmt.Sprintf("%s/dark=%v/width=%g", kind, dark, width), func(t *testing.T) {
					v := New(layoutModel(), nil, "")
					source := "agent"
					if kind == "background" {
						source = "background"
					}
					elapsed := int64(12000)
					works := []domain.WorkActivity{
						{ID: "plain", Kind: source, Title: "没有完成摘要", State: "completed", DurationMS: &elapsed},
						{ID: "whitespace", Kind: source, Title: "空白摘要", Latest: " \n\t ", State: "completed"},
						{ID: "summary", Kind: source, Title: "有完成摘要", Latest: "边界条件和回归检查通过", State: "completed"},
						{ID: "question", Kind: source, Title: "需要回答", State: "waiting"},
					}
					d := v.Model.Details["s"]
					d.Works = works
					d.Requests = []domain.PendingRequest{{ID: "question", WorkID: "question", Kind: "question", State: "pending", Questions: []domain.Question{{Text: "请选择接下来的检查范围"}}}}
					v.Model.Details["s"] = d
					ui := n.NewTester(func(c *n.Context) {
						theme := applyAppearance(c, v.Model.Preferences)
						n.Column(c).Fill().Background(theme.Background).Children(func() { v.sessionWorkBody(c, theme, kind, works, width) })
					}, int(width), 320)
					ui.SetDark(dark)
					ui.SetPreferences(n.Preferences{ReduceMotion: true})
					ui.Advance(time.Second)
					for i, work := range works {
						row, ok := ui.Find("查看" + inspectorWorkName(kind) + "：" + work.Title)
						showCaption := i == 3 || i == 2 && source == "agent"
						want := float32(36)
						if showCaption {
							want = 56
						}
						if !ok || math.Abs(float64(row.H-want)) > .5 {
							t.Errorf("unexpected row height: %s height=%g want=%g", work.Title, row.H, want)
						}
						if showCaption {
							text := work.Latest
							if i == 3 {
								text = d.Requests[0].Questions[0].Text
							}
							caption, found := ui.Find(text)
							if !found || caption.Y < row.Y+28 || caption.Y+caption.H > row.Y+row.H-7.5 {
								t.Error("real summary is clipped or not below the first line", row, caption)
							}
						}
					}
					// A late subagent summary grows its row without moving the title.
					before, _ := ui.Find(works[0].Title)
					works[0].Latest = "稍后到达的完成摘要"
					ui.Frame()
					ui.Advance(time.Second)
					grown, _ := ui.Find("查看" + inspectorWorkName(kind) + "：" + works[0].Title)
					wantHeight := float32(36)
					if source == "agent" {
						wantHeight = 56
					}
					if grown.H != wantHeight || ui.HasText(works[0].Latest) != (source == "agent") {
						t.Errorf("unexpected late output visibility or row height: %v", grown)
					}
					works[0].Latest = ""
					ui.Frame()
					ui.Advance(time.Second)
					shrunk, _ := ui.Find("查看" + inspectorWorkName(kind) + "：" + works[0].Title)
					after, _ := ui.Find(works[0].Title)
					if shrunk.H != 36 || math.Abs(float64(before.Y-after.Y)) > .5 {
						t.Error("removing summary leaves whitespace or moves first-line text", before, after, shrunk)
					}
				})
			}
		}
	}
}

func TestWorkSummaryHeightChangeKeepsFirstLineStable(t *testing.T) {
	for _, kind := range []string{"agents", "background"} {
		t.Run(kind, func(t *testing.T) {
			v := New(layoutModel(), nil, "")
			source := "agent"
			if kind == "background" {
				source = "background"
			}
			works := []domain.WorkActivity{{ID: "work", Kind: source, Title: "Review parser · 检查边界", State: "completed"}}
			ui := n.NewTester(func(c *n.Context) {
				theme := applyAppearance(c, v.Model.Preferences)
				n.Column(c).Fill().Background(theme.Background).Children(func() { v.sessionWorkBody(c, theme, kind, works, 380) })
			}, 380, 180)
			ui.Advance(time.Second)
			label := "查看" + inspectorWorkName(kind) + "：" + works[0].Title
			before, _ := ui.Find(works[0].Title)
			d := v.Model.Details["s"]
			if source == "background" {
				d.Requests = []domain.PendingRequest{{ID: "question", WorkID: works[0].ID, Kind: "question", State: "pending", Questions: []domain.Question{{Text: "请选择接下来的检查范围"}}}}
				v.Model.Details["s"] = d
			} else {
				works[0].Latest = "检查通过，稍后到达摘要"
			}
			ui.Frame()
			ui.Advance(60 * time.Millisecond)
			middle, _ := ui.Find(label)
			title, _ := ui.Find(works[0].Title)
			if middle.H <= 36 || middle.H >= 56 || math.Abs(float64(title.Y-before.Y)) > .5 {
				t.Fatal("summary addition jumps or moves the first line", middle, before, title)
			}
			ui.Advance(200 * time.Millisecond)
			d.Requests = nil
			v.Model.Details["s"] = d
			works[0].Latest = ""
			ui.Frame()
			ui.Advance(60 * time.Millisecond)
			middle, _ = ui.Find(label)
			title, _ = ui.Find(works[0].Title)
			if middle.H <= 36 || middle.H >= 56 || math.Abs(float64(title.Y-before.Y)) > .5 {
				t.Fatal("summary removal jumps or moves the first line", middle, before, title)
			}
			ui.Advance(200 * time.Millisecond)
			end, _ := ui.Find(label)
			if end.H != 36 {
				t.Fatal("summary removal leaves unused height", end)
			}
		})
	}
}
