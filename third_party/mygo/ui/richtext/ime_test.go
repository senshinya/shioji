package richtext

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEndedPreeditReleasesApplicationKeys(t *testing.T) {
	for _, unmark := range []bool{false, true} {
		name := "empty-marked-text"
		if unmark {
			name = "native-unmark"
		}
		t.Run(name, func(t *testing.T) {
			doc := NewDocument(NewText("draft"))
			sent := 0
			tt := ui.NewTester(func(c *ui.Context) {
				EditorBase(c, doc, EditorOptions{Label: "input", PlainText: true, OnKey: func(key ui.Key, mods ui.Modifiers) bool {
					if key == ui.KeyEnter && mods == 0 {
						sent++
						return true
					}
					return false
				}}).Fill()
			}, 500, 200)
			if err := tt.Click("input"); err != nil {
				t.Fatal(err)
			}
			client := doc.shownIn.Value()
			client.SetMarkedText(nil, "中", ui.TextInputRange{Start: 1, End: 1})
			if !doc.Composing() {
				t.Fatal("active preedit was not reported")
			}
			if unmark {
				client.UnmarkText()
			} else {
				client.SetMarkedText(nil, "", ui.TextInputRange{})
			}
			if doc.Composing() {
				t.Error("ended preedit still reports composition")
			}
			before := doc.Text().String()
			tt.Key(0, ui.KeyEnter)
			if sent != 1 || doc.Text().String() != before {
				t.Fatalf("Enter bypassed application: sent=%d text=%q want=%q", sent, doc.Text().String(), before)
			}
		})
	}
}

func TestUnmarkThenReplacementRetainsSingleUndoStep(t *testing.T) {
	doc := NewDocument(NewText(""))
	tt := ui.NewTester(func(c *ui.Context) {
		EditorBase(c, doc, EditorOptions{Label: "input", PlainText: true}).Fill()
	}, 500, 200)
	if err := tt.Click("input"); err != nil {
		t.Fatal(err)
	}
	client := doc.shownIn.Value()
	client.SetMarkedText(nil, "你", ui.TextInputRange{Start: 1, End: 1})
	client.UnmarkText()
	client.ReplaceText(&ui.TextInputRange{Start: 0, End: 1}, "你好")
	if got := doc.Text().String(); got != "你好" {
		t.Fatal("replacement lost committed text", got)
	}
	doc.Undo()
	if got := doc.Text().String(); got != "" {
		t.Fatal("unmark and replacement split undo", got)
	}
	doc.Redo()
	if got := doc.Text().String(); got != "你好" {
		t.Fatal("redo lost replacement", got)
	}
}
