package richtext

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestCaretScrollReachesNextLineOverSeveralFrames(t *testing.T) {
	doc := NewDocument(NewText("one\ntwo\nthree\nfour\nfive\n"))
	doc.SetSelection(0, 0)
	duration := 140 * time.Millisecond
	tt := ui.NewTester(func(c *ui.Context) {
		EditorBase(c, doc, EditorOptions{Label: "input", PlainText: true,
			LineHeight: 20, CaretScrollDuration: duration}).Fill()
	}, 220, 61)
	if err := tt.Click("input"); err != nil {
		t.Fatal(err)
	}
	view := doc.shownIn.Value()
	initial := view.scroll
	next := len("one\ntwo\nthree\n")
	doc.SetSelection(next, next)
	tt.Frame()
	if view.scroll != initial {
		t.Fatalf("caret scroll jumped from %v to %v", initial, view.scroll)
	}
	tt.Advance(duration / 2)
	midpoint := view.scroll
	tt.Advance(duration)
	if midpoint <= initial || view.scroll <= midpoint {
		t.Fatalf("expected progressive scrolling, got %v, %v, %v", initial, midpoint, view.scroll)
	}
	if caret, ok := doc.CaretRect(); !ok || caret.Y+caret.H > 61.5 {
		t.Fatalf("the caret stayed outside the editor: %+v, %v", caret, ok)
	}
}

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
