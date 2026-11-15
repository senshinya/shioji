package ui

import (
	"fmt"
	"testing"
	"time"
)

func TestQueueDefaultsExpandedAndPreservesManualCollapse(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			v, ui := nativeQueueFixture()
			delete(v.Model.QueueExpanded, "s")
			ui.SetDark(dark)
			ui.Frame()
			ui.Advance(time.Second)
			saveNativeSkillImage(t, ui, fmt.Sprintf("queue-default-%v.png", dark))
			if _, visible := ui.Find("Review the rotation code"); !visible {
				t.Fatal("queue without a saved disclosure state must show its messages")
			}
			if err := ui.Click("待发送 2"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if v.Model.QueueExpanded["s"] {
				t.Fatal("click did not collapse the default-expanded queue")
			}
			q := v.Model.Queue()
			q.Revision++
			q.Items = append(q.Items, q.Items[0])
			q.Items[2].ID = "three"
			v.Model.SetQueue(q)
			ui.Frame()
			ui.Advance(time.Second)
			if _, visible := ui.Find("Review the rotation code"); visible {
				t.Fatal("queue update reopened a manually collapsed queue")
			}
			if err := ui.Click("待发送 3"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if _, visible := ui.Find("Review the rotation code"); !visible {
				t.Fatal("queue cannot be expanded again")
			}
		})
	}
}
