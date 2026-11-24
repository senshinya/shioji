package main

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
)

func TestMobileToolResultPanelEmptyContentStopsRequestingFrames(t *testing.T) {
	visible := false
	hasBody := false
	var panelH float32
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			toolResultPanel(c.Key("tool-panel"), visible, false, nil, func(c *ui.Context) {
				if hasBody {
					ui.Box(c.Key("body")).FillWidth().Height(50)
				}
			})
			ui.Box(c.Key("after")).FillWidth().Height(10).DrawOver(func(_ *ui.Painter, r ui.Rect) { panelH = r.Y })
		})
	}, 320, 200)

	visible = true
	tester.Frame()
	tester.Advance(time.Second)
	if tester.FramePending() {
		t.Fatal("empty toolResultPanel keeps requesting frames forever")
	}

	hasBody = true
	tester.Frame()
	tester.Advance(60 * time.Millisecond)
	if panelH <= 0 || panelH >= 50 {
		t.Fatalf("toolResultPanel did not animate open after late content arrived: y=%g", panelH)
	}
	tester.Advance(time.Second)
	if panelH < 49.5 || panelH > 50.5 {
		t.Fatalf("toolResultPanel did not settle at 50: y=%g", panelH)
	}
	if tester.FramePending() {
		t.Fatal("settled toolResultPanel still has a pending frame")
	}
}

func TestMobileDisclosureBodyAnimatesFirstExpansion(t *testing.T) {
	expanded := false
	var siblingY float32
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			mobileDisclosureBody(c.Key("disclosure"), expanded, func(body *ui.Context) {
				ui.Box(body.Key("content")).FillWidth().Height(80)
			})
			ui.Box(c.Key("sibling")).FillWidth().Height(10).DrawOver(func(_ *ui.Painter, r ui.Rect) { siblingY = r.Y })
		})
	}, 320, 200)

	if siblingY > 0.5 {
		t.Fatalf("collapsed disclosure reserved height %g", siblingY)
	}

	expanded = true
	tester.Frame()
	tester.Advance(60 * time.Millisecond)
	if siblingY <= 0 || siblingY >= 80 {
		t.Fatalf("mobileDisclosureBody did not animate on first expansion: mid siblingY=%g, want (0, 80)", siblingY)
	}
	tester.Advance(time.Second)
	if siblingY < 79.5 || siblingY > 80.5 {
		t.Fatalf("mobileDisclosureBody did not reach full height 80: siblingY=%g", siblingY)
	}
	if tester.FramePending() {
		t.Fatal("settled mobileDisclosureBody still has a pending frame")
	}
}

func TestMobileTranscriptPictureFadesInAcrossMultipleFrames(t *testing.T) {
	rgba := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 24; x++ {
			rgba.SetRGBA(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	loaded := ui.NewBitmap(rgba)
	var current *ui.Bitmap

	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
			box := ui.Box(c.Key("pic-box")).Size(48, 48).Center().Clip().Background(ui.Hex("#ffffff"))
			box.Children(func() {
				transcriptPicture(c, box, current)
			})
		})
	}, 100, 100)

	// Bitmap arrives after placeholder was shown.
	current = loaded
	tester.Frame()
	start := tester.Image().RGBAAt(24, 24)
	tester.Advance(50 * time.Millisecond)
	mid := tester.Image().RGBAAt(24, 24)
	tester.Advance(time.Second)
	end := tester.Image().RGBAAt(24, 24)

	if mid == end || mid.R <= end.R+10 || start.R <= mid.R {
		t.Fatalf("transcriptPicture did not fade smoothly across frames (killed on frame 2?): start=%v mid=%v end=%v", start, mid, end)
	}
	if tester.FramePending() {
		t.Fatal("settled transcriptPicture still has a pending frame")
	}
}

func TestMobileSessionEntryEmergingFadesAcrossMultipleFrames(t *testing.T) {
	m := app.NewModel()
	sid := "s-emerge"
	session := domain.Session{ID: sid, WorkspaceID: "work", ThreadID: "t1", Status: domain.ExecutionCompleted}
	m.Snapshot = domain.Snapshot{DeviceID: "device", Sessions: []domain.Session{session}}
	m.SessionID = sid
	m.Connected = true
	a := &mobileApp{
		model:              m,
		history:            make(map[string]*ui.ListState),
		latestSessionEntry: sid,
	}

	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
			s := a.session()
			a.renderTranscript(c, nil, s, a.model.Details[s.ID], true, nil, nil, nil, ui.Element{}, firstSubmissionView{})
		})
	}, 360, 400)

	// History finishes loading and replaces the loading placeholder.
	m.Details[sid] = domain.SessionDetail{
		Session:  session,
		Sequence: 1,
		History:  &domain.HistoryWindow{First: 1, Last: 1},
		Items: []domain.Item{
			{ID: "m1", Order: 1, Kind: "assistant", Text: "████████████████████", Final: true},
		},
	}
	tester.Frame()
	if a.latestSessionEntry != "" {
		t.Fatal("latestSessionEntry was not cleared on history arrival")
	}

	// At 40ms into the 120ms emerge fade, the fade must still be active and visibly lighter than settled.
	tester.Advance(40 * time.Millisecond)
	midPending := tester.FramePending()
	midImg := tester.Image()
	tester.Advance(time.Second)
	endImg := tester.Image()

	if !midPending {
		t.Fatal("emerging session fade was killed before 40ms")
	}
	var maxDiff int
	bounds := midImg.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			aPx := midImg.RGBAAt(x, y)
			bPx := endImg.RGBAAt(x, y)
			d := int(aPx.R) - int(bPx.R)
			if d < 0 {
				d = -d
			}
			if d > maxDiff {
				maxDiff = d
			}
		}
	}
	if maxDiff < 15 {
		t.Fatalf("emerging session transcript snapped to full opacity at 40ms (max pixel diff=%d)", maxDiff)
	}
}
