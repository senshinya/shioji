package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func commandMotionFixture(t *testing.T, dark, reduce bool) (*View, *n.Tester, *[]domain.Item) {
	t.Helper()
	v := New(layoutModel(), nil, "")
	items := []domain.Item{{ID: "first", Kind: "tool", Presentation: "command", Name: "first command", Text: strings.Repeat("retained command output\n", 10), Status: "completed", Final: true}}
	latest := ""
	ui := n.NewTester(func(c *n.Context) {
		theme := applyAppearance(c, v.Model.Preferences)
		ids := []string{}
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		v.syncCommandOutputs(ids, &latest, "")
		n.Column(c).Fill().Padding(20).Gap(8).Background(theme.Background).Children(func() {
			for _, item := range items {
				n.Column(c.Key(item.ID)).FillWidth().Gap(8).Label(item.ID + " block").Children(func() { v.item(c, theme, item) })
			}
			n.Text(c, "following content")
		})
	}, 620, 400)
	ui.SetDark(dark)
	ui.SetPreferences(n.Preferences{ReduceMotion: reduce})
	ui.Advance(time.Second)
	ui.Click("first command")
	ui.Advance(time.Second)
	return v, ui, &items
}
func TestCommandOutputCollapseRetainsHeightUntilAnimationEnds(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, automatic := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/automatic=%v", dark, automatic), func(t *testing.T) {
				_, ui, items := commandMotionFixture(t, dark, false)
				before, _ := ui.Find("first block")
				if automatic {
					*items = append(*items, domain.Item{ID: "next", Kind: "tool", Presentation: "command", Name: "next command", Status: "running"})
					ui.Frame()
				} else if err := ui.Click("first command"); err != nil {
					t.Fatal(err)
				}
				start, _ := ui.Find("first block")
				if math.Abs(float64(start.H-before.H)) > .5 {
					t.Errorf("collapse removed layout immediately: %.1f -> %.1f", before.H, start.H)
				}
				ui.Advance(60 * time.Millisecond)
				middle, _ := ui.Find("first block")
				saveNativeSkillImage(t, ui, fmt.Sprintf("command-close-%v-%v.png", dark, automatic))
				if middle.H <= 20 || middle.H >= before.H || toolLabelContrast(ui.Image(), n.Rect{X: before.X + 10, Y: before.Y + 28, W: 200, H: 20}, dark) < 50 {
					t.Error("collapse has no retained intermediate output", before, middle)
				}
				// Compare reversal at exactly the same native animation time.
				ui.Frame()
				at, _ := ui.Find("first block")
				if err := ui.Click("first command"); err != nil {
					t.Fatal(err)
				}
				reversed, _ := ui.Find("first block")
				if math.Abs(float64(reversed.H-at.H)) > .5 {
					t.Error("rapid reversal jumped", at, reversed)
				}
				ui.Advance(time.Second)
				opened, _ := ui.Find("first block")
				if math.Abs(float64(opened.H-before.H)) > .5 {
					t.Error("reopening lost original output height", before, opened)
				}
				ui.Click("first command")
				ui.Advance(time.Second)
				closed, _ := ui.Find("first block")
				if closed.H > 20.5 || ui.HasText("retained command output") {
					t.Error("closed output still occupies layout or remains mounted", closed)
				}
			})
		}
	}
}
func TestCommandOutputCollapseHonorsReducedMotion(t *testing.T) {
	_, ui, _ := commandMotionFixture(t, false, true)
	ui.Click("first command")
	ui.Frame()
	closed, _ := ui.Find("first block")
	if closed.H > 20.5 || ui.HasText("retained command output") {
		t.Fatal("reduced motion waits for collapse", closed)
	}
}
func TestChildCommandOutputCollapseMovesFollowingMessageContinuously(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprint(automatic), func(t *testing.T) {
			v, ui := sessionWorkFixture(t)
			key := v.Model.WorkKey("s", "agent-a")
			d := v.Model.WorkDetails[key]
			d.Items = []domain.WorkItem{{Item: domain.Item{ID: "child-command", Kind: "tool", Presentation: "command", Name: "child command", Text: strings.Repeat("retained child output\n", 4), Final: true}}, {Item: domain.Item{ID: "after", Kind: "assistant", Text: "after child output", Final: true}}}
			v.Model.WorkDetails[key] = d
			ui.Click("查看子 Agent：检查边界条件")
			ui.Advance(time.Second)
			ui.Click("child command")
			ui.Advance(time.Second)
			before, ok := ui.Find("after child output")
			if !ok {
				t.Fatal("missing following child message")
			}
			heading, _ := ui.Find("child command")
			if automatic {
				d.Items = append(d.Items, domain.WorkItem{Item: domain.Item{ID: "new-child-command", Kind: "tool", Presentation: "command", Name: "next child command"}})
				v.Model.WorkDetails[key] = d
				ui.Frame()
			} else {
				ui.Click("child command")
			}
			start, _ := ui.Find("after child output")
			if math.Abs(float64(start.Y-before.Y)) > .5 {
				t.Error("child output dropped immediately", before, start)
			}
			ui.Advance(60 * time.Millisecond)
			middle, _ := ui.Find("after child output")
			if middle.Y >= before.Y || toolLabelContrast(ui.Image(), n.Rect{X: heading.X + 2, Y: heading.Y + 24, W: 200, H: 20}, false) < 50 {
				t.Error("child output has no closing transition", before, middle)
			}
			ui.Advance(time.Second)
			end, _ := ui.Find("after child output")
			if end.Y >= middle.Y || ui.HasText("retained child output") {
				t.Error("child collapse did not finish", middle, end)
			}
		})
	}
}
