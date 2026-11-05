package ui

import (
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func liveCompletionModel() *View {
	m := layoutModel()
	m.Snapshot.Sessions[0].Status = "running"
	d := m.Details["s"]
	d.Session.Status = "running"
	d.Items = []domain.Item{{ID: "question", TurnID: "t", Kind: "user", Text: "Explain rotation.", Final: true}, {ID: "live", TurnID: "t", Kind: "assistant", Text: "Prefix."}}
	d.Turns = []domain.Turn{{ID: "t", Status: "running"}}
	m.Details["s"] = d
	return New(m, nil, "")
}

func sidebarMark(ui *n.Tester) *image.RGBA { return sidebarMarkFor(ui, "Layout") }

func sidebarMarkFor(ui *n.Tester, label string) *image.RGBA {
	r, _ := ui.Find(label)
	im := image.NewRGBA(image.Rect(0, 0, 16, 22))
	for y := 0; y < 22; y++ {
		for x := 0; x < 16; x++ {
			im.SetRGBA(x, y, ui.Image().RGBAAt(int(r.X)+18+x, int(r.Y)+3+y))
		}
	}
	return im
}

func sameMark(a, b *image.RGBA) bool {
	if len(a.Pix) != len(b.Pix) {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}

func TestVisibleSessionCheckWaitsForNativeTextTailAndFade(t *testing.T) {
	v := liveCompletionModel()
	ui := n.NewTester(v.View, 1200, 820)
	running := sidebarMark(ui)
	d := v.Model.Details["s"]
	d.Items[1].Text = "Prefix. Final words."
	d.Items[1].Final = true
	d.Session.Status = "completed"
	d.Turns[0].Status = "completed"
	v.Model.Details["s"] = d
	v.Model.Snapshot.Sessions[0].Status = "completed"
	ui.Frame()
	if !sameMark(running, sidebarMark(ui)) {
		t.Fatal("sidebar shows completed before the native output animation drains")
	}
	for i := 0; i < 100 && !ui.HasText("Prefix. Final words."); i++ {
		ui.Advance(time.Second / 60)
	}
	if !ui.HasText("Prefix. Final words.") {
		t.Fatal("final text never appears")
	}
	if !sameMark(running, sidebarMark(ui)) {
		t.Fatal("sidebar ignores the last text fade after the full source appears")
	}
	ui.Advance(600 * time.Millisecond)
	if sameMark(running, sidebarMark(ui)) || ui.FramePending() {
		t.Fatal("sidebar never becomes a check after all output settles")
	}
	if v.Model.Snapshot.Sessions[0].Status != "completed" {
		t.Fatal("presentation delay modifies runtime completion")
	}
}

func TestScrollingAwayDoesNotLeaveCompletionWaitingForUnmountedOutput(t *testing.T) {
	v := liveCompletionModel()
	d := v.Model.Details["s"]
	var history []domain.Item
	for i := 0; i < 80; i++ {
		history = append(history, domain.Item{ID: fmt.Sprint("old-", i), Kind: "assistant", Text: strings.Repeat("Earlier conversation paragraph. ", 8), Final: true})
	}
	d.Items = append(history, d.Items...)
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1200, 820)
	running := sidebarMark(ui)
	last := len(d.Items) - 1
	d.Items[last].Text += " " + replyBurst
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	ui.Scroll(800, 350, 0, -100000)
	ui.Frame()
	if ui.HasText("Prefix.") {
		t.Fatal("test did not unmount the live reply")
	}
	d.Items[last].Text += "Final tail."
	d.Items[last].Final = true
	d.Session.Status = "completed"
	d.Turns[0].Status = "completed"
	v.Model.Details["s"] = d
	v.Model.Snapshot.Sessions[0].Status = "completed"
	ui.Frame()
	ui.Advance(2 * time.Second)
	if sameMark(running, sidebarMark(ui)) || ui.FramePending() {
		t.Fatal("unmounted output keeps the sidebar in progress forever")
	}
}

func TestFooterGreenDotBreathesWithoutRebuildingReply(t *testing.T) {
	v := liveCompletionModel()
	builds := 0
	ui := n.NewTester(func(c *n.Context) { builds++; v.View(c) }, 1200, 820)
	if !ui.HasText("进行中") {
		t.Fatal("running footer is missing")
	}
	dot, ok := ui.Find("进行中状态")
	if !ok {
		t.Fatal("running footer has no green indicator")
	}
	ui.Advance(1400 * time.Millisecond)
	count := builds
	first := ui.Image().RGBAAt(int(dot.X+dot.W/2), int(dot.Y+dot.H/2))
	ui.Advance(700 * time.Millisecond)
	second := ui.Image().RGBAAt(int(dot.X+dot.W/2), int(dot.Y+dot.H/2))
	if first == second {
		t.Fatal("footer green dot does not breathe")
	}
	if builds != count {
		t.Fatal("breathing footer rebuilds the reply view")
	}
}

func TestFooterGreenDotRespectsReducedMotion(t *testing.T) {
	v := liveCompletionModel()
	ui := n.NewTester(v.View, 1200, 820)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	dot, ok := ui.Find("进行中状态")
	if !ok {
		t.Fatal("Reduce Motion hides the status dot")
	}
	first := copyFrame(ui)
	ui.Advance(700 * time.Millisecond)
	x, y := int(dot.X+dot.W/2), int(dot.Y+dot.H/2)
	if first.RGBAAt(x, y) != ui.Image().RGBAAt(x, y) || ui.FramePending() {
		t.Fatal("Reduce Motion still pulses the footer")
	}
}

func TestCompletionDelayAppliesOnlyToTheCurrentlyDisplayedSession(t *testing.T) {
	v := liveCompletionModel()
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, domain.Session{ID: "b", WorkspaceID: "w", Title: "Background task", Status: "completed"})
	ui := n.NewTester(v.View, 1200, 820)
	background := sidebarMarkFor(ui, "Background task")
	d := v.Model.Details["s"]
	d.Items[1].Text += " " + replyBurst
	d.Items[1].Final = true
	d.Session.Status = "completed"
	d.Turns[0].Status = "completed"
	v.Model.Details["s"] = d
	v.Model.Snapshot.Sessions[0].Status = "completed"
	ui.Frame()
	if !sameMark(background, sidebarMarkFor(ui, "Background task")) {
		t.Fatal("a different session's completion waits for the visible reply")
	}
	v.Model.SessionID = "b"
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	baseline := n.NewTester(New(v.Model, nil, "").View, 1200, 820)
	if !sameMark(sidebarMark(ui), sidebarMark(baseline)) || ui.FramePending() {
		t.Fatal("leaving a conversation keeps its completion delayed")
	}
}

func TestVisibleSessionCheckWaitsForAllReplyFragments(t *testing.T) {
	v := liveCompletionModel()
	ui := n.NewTester(v.View, 1200, 820)
	running := sidebarMark(ui)
	d := v.Model.Details["s"]
	d.Items[1].Text += " " + strings.Repeat("Long reply. ", 120)
	d.Items[1].Final = true
	d.Items = append(d.Items, domain.Item{ID: "short", TurnID: "t", Kind: "assistant", Text: "Short reply.", Final: true})
	d.Session.Status = "completed"
	d.Turns[0].Status = "completed"
	v.Model.Details["s"] = d
	v.Model.Snapshot.Sessions[0].Status = "completed"
	ui.Frame()
	ui.Advance(220 * time.Millisecond)
	if !sameMark(running, sidebarMark(ui)) {
		t.Fatal("a short reply fragment marks the session complete before the longer fragment settles")
	}
	ui.Advance(1200 * time.Millisecond)
	if sameMark(running, sidebarMark(ui)) {
		t.Fatal("completed fragments never release the sidebar check")
	}
}
