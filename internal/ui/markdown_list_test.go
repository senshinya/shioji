package ui

import (
	"fmt"
	"testing"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
)

func TestStreamedOrderedListKeepsMarkerPunctuationOnItsLine(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			text := "1. 正文第一行。\n"
			streaming := true
			options := markdown.Options{Theme: replyTheme((app.ClientPreferences{}).Normalized()), Reveal: &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}}
			ui := n.NewTester(func(c *n.Context) {
				if streaming {
					markdown.Stream(c, text, &options).FillWidth()
				} else {
					markdown.View(c, text, &options).FillWidth()
				}
			}, 500, 1000)
			ui.SetDark(dark)
			for count := 2; count <= 20; count++ {
				text += fmt.Sprintf("%d. 正文第一行。\n", count)
				ui.Frame()
				for i := 1; i <= count; i++ {
					marker, ok := ui.Find(fmt.Sprint(i) + ".")
					if !ok || marker.H > 25 {
						t.Fatalf("at %d items, marker %d wraps its punctuation: %+v, found=%v", count, i, marker, ok)
					}
				}
			}
			// Persisted history uses the same marker geometry as the live prefix.
			before, _ := ui.Find("15.")
			streaming = false
			ui.Frame()
			if after, _ := ui.Find("15."); after != before {
				t.Fatal("completion changes the ordered-list marker geometry", before, after)
			}
		})
	}
}
