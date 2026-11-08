package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/domain"
	"testing"
	"time"
)

func TestReferenceSelectionStandsOutFromItsPopupInBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, trigger := range []rune{'@', '/'} {
			t.Run(fmt.Sprintf("dark=%v/%c", dark, trigger), func(t *testing.T) {
				m := layoutModel()
				if dark {
					m.Preferences.Appearance = "dark"
				} else {
					m.Preferences.Appearance = "light"
				}
				v := New(m, nil, "")
				state := &fileMention{key: "home", trigger: trigger, phase: mentionShowing, present: true,
					results: domain.FileSearchResult{Files: []domain.WorkspaceFile{{Root: "/project", Path: "one.go"}, {Root: "/project", Path: "two.go"}}},
					skills:  []domain.Skill{{SkillReference: domain.SkillReference{ID: "one", Name: "design"}}, {SkillReference: domain.SkillReference{ID: "two", Name: "check"}}}}
				ui := n.NewTester(func(c *n.Context) {
					theme := applyAppearance(c, m.Preferences)
					n.Column(c).Fill().Background(theme.Background).Children(func() {
						n.Column(c).Height(400)
						anchor := n.Column(c).Size(500, 80)
						v.mentionPopup(c, theme, anchor, composerInput{binding: &composerDocument{key: "home"}})
					})
				}, 800, 600)
				v.fileMention = state
				ui.Frame()
				ui.Advance(300 * time.Millisecond)
				labels := []string{"引用文件：one.go", "引用文件：two.go"}
				if trigger == '/' {
					labels = []string{"选择 Skill：design", "选择 Skill：check"}
				}
				first, ok := ui.Find(labels[0])
				if !ok {
					t.Fatal("first candidate missing", ui.Texts())
				}
				second, ok := ui.Find(labels[1])
				if !ok {
					t.Fatal("second candidate missing")
				}
				sample := func(r n.Rect) int { return int(ui.Image().RGBAAt(int(r.X+3), int(r.Y+r.H/2)).R) }
				selected, rest := sample(first), sample(second)
				if dark && selected-rest < 18 {
					t.Fatal("dark selection merges into the raised popup", selected, rest)
				}
				if !dark && rest-selected < 12 {
					t.Fatal("light selection is indistinct", selected, rest)
				}
				state.selected = 1
				ui.Frame()
				ui.Advance(45 * time.Millisecond)
				mid := sample(second)
				ui.Advance(200 * time.Millisecond)
				end := sample(second)
				if dark && !(mid > rest && mid < end) || !dark && !(mid < rest && mid > end) {
					t.Fatal("keyboard selection background changes without a transition", rest, mid, end)
				}
			})
		}
	}
}

func TestWorkspaceMenuDeviceInkSharesTheRightEdge(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			m := layoutModel()
			if dark {
				m.Preferences.Appearance = "dark"
			} else {
				m.Preferences.Appearance = "light"
			}
			v := New(m, nil, "")
			choices := []menuChoice{{id: "one", name: "真实命名测试", detail: "这台 Mac", checked: true}, {id: "two", name: "长工作区名称会保留设备列的位置", detail: "MacBook Air"}}
			ui := n.NewTester(func(c *n.Context) {
				theme := applyAppearance(c, m.Preferences)
				n.Column(c).Fill().Background(theme.Background).Padding(20).Children(func() { trigger := n.Button(c, "工作区选择"); v.choiceMenu(c, theme, "test", trigger, choices) })
			}, 600, 400)
			ui.Click("工作区选择")
			ui.Advance(300 * time.Millisecond)
			inkRight := func(label string) float32 {
				r, ok := ui.Find(label)
				if !ok {
					t.Fatal("device missing", label, ui.Texts())
				}
				im := ui.Image()
				bg := im.RGBAAt(int(r.X+r.W-1), int(r.Y))
				right := -1
				for y := int(r.Y); y < int(r.Y+r.H); y++ {
					for x := int(r.X); x < int(r.X+r.W); x++ {
						p := im.RGBAAt(x, y)
						d := int(p.R) - int(bg.R)
						if d > 35 || d < -35 {
							right = max(right, x)
						}
					}
				}
				if right < 0 {
					t.Fatal("device ink missing", label)
				}
				return float32(right)
			}
			a, b := inkRight("这台 Mac"), inkRight("MacBook Air")
			if math.Abs(float64(a-b)) > 1 {
				t.Fatal("device names are not right aligned", a, b)
			}
		})
	}
}
