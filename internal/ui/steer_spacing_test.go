package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func TestInsertedUserMessageUsesNormalTurnSpacing(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, expanded := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/expanded=%v", dark, expanded), func(t *testing.T) {
				gap := func(inserted bool) float32 {
					v := toolTailFixture(2)
					d := v.Model.Details["s"]
					turn := "next-turn"
					if inserted {
						turn = "t"
					}
					d.Items = append(d.Items, domain.Item{ID: "steer", Kind: "user", TurnID: turn, Text: "继续检查消息间距", Final: true})
					// Compare the same visible status/user boundary, with only the
					// protocol turn identity changed; no receipt belongs between it.
					d.Turns = nil
					v.Model.Details["s"] = d
					ui := n.NewTester(v.View, 1240, 820)
					ui.SetDark(dark)
					ui.Advance(time.Second)
					if expanded {
						if err := ui.Click("2 次工具调用"); err != nil {
							t.Fatal(err)
						}
						ui.Advance(time.Second)
					}
					if ui.HasText("go test ./internal/ui -run Check0") != expanded {
						t.Fatal("incorrect disclosure fixture")
					}
					label := "2 次工具调用"
					if expanded {
						label = "go test ./internal/ui -run Check1"
					}
					previous, ok := ui.Find(label)
					if !ok {
						t.Fatal("last visible tool row missing", label)
					}
					next, ok := v.historyRowBounds["steer"]
					if !ok {
						t.Fatal("inserted message missing")
					}
					name := "normal"
					if inserted {
						name = "inserted"
					}
					saveNativeSkillImage(t, ui, fmt.Sprintf("steer-gap-%s-%v-%v.png", name, dark, expanded))
					return next.Y - previous.Y - previous.H
				}
				want, got := gap(false), gap(true)
				if math.Abs(float64(want-32)) > .5 || math.Abs(float64(got-want)) > .5 {
					t.Fatalf("inserted message gap %.1f, normal turn gap %.1f; want the same 32 DIP", got, want)
				}
			})
		}
	}
}

func TestInsertedUserAfterProseUsesNormalTurnSpacing(t *testing.T) {
	gap := func(turn string) float32 {
		v := liveCompletionModel()
		d := v.Model.Details["s"]
		d.Items = []domain.Item{{ID: "reply", Kind: "assistant", TurnID: "t", Text: "上方的回复正文", Final: true}, {ID: "steer", Kind: "user", TurnID: turn, Text: "继续检查", Final: true}}
		d.Turns = nil
		v.Model.Details["s"] = d
		ui := n.NewTester(v.View, 1240, 820)
		ui.Advance(time.Second)
		previous, ok := ui.Find("上方的回复正文")
		if !ok {
			t.Fatal("previous reply missing")
		}
		return v.historyRowBounds["steer"].Y - previous.Y - previous.H
	}
	want, got := gap("next-turn"), gap("t")
	if want < 32 || math.Abs(float64(want-got)) > .5 {
		t.Fatalf("inserted user after prose has gap %.1f; normal turn gap %.1f", got, want)
	}
}
