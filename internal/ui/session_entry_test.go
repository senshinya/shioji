package ui

import (
	"encoding/json"
	"image"
	"math"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/domain"
)

func headingInk(im *image.RGBA) int {
	ink := 0
	for y := 10; y < 42; y++ {
		for x := 280; x < 900; x++ {
			p := im.RGBAAt(x, y)
			ink += 255 - int(p.R)
		}
	}
	return ink
}

func TestHomeCreationFadesInWorkspaceAndSessionHeading(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	acceptSend(t, v, job, true)
	ui.Frame()
	first := headingInk(ui.Image())
	ui.Advance(120 * time.Millisecond)
	middle := headingInk(ui.Image())
	ui.Advance(400 * time.Millisecond)
	end := headingInk(ui.Image())
	if end < 1000 || first > end/10 || middle <= first+100 || middle >= end*95/100 {
		t.Fatalf("new heading appears without a fade: first=%d middle=%d end=%d", first, middle, end)
	}
	if !ui.Focused("消息输入") {
		t.Fatal("heading entrance steals composer focus")
	}
}

func TestHomeCreationGrowsSidebarRowWithoutJumpingFollowingWorkspaces(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	v.Model.Snapshot.Workspaces = append(v.Model.Snapshot.Workspaces, domain.Workspace{ID: "w2", Name: "Following workspace"})
	ui.Frame()
	before, _ := ui.Find("Following workspace")
	acceptSend(t, v, job, true)
	ui.Frame()
	first, _ := ui.Find("Following workspace")
	ui.Advance(120 * time.Millisecond)
	middle, _ := ui.Find("Following workspace")
	ui.Advance(400 * time.Millisecond)
	end, ok := ui.Find("Following workspace")
	if !ok || math.Abs(float64(first.Y-before.Y)) > .5 || !(middle.Y > before.Y+3 && middle.Y < end.Y-3) || math.Abs(float64(end.Y-before.Y-30)) > .5 {
		t.Fatalf("sidebar insertion jumps instead of growing: before=%+v first=%+v middle=%+v end=%+v", before, first, middle, end)
	}
}

func TestHomeCreationWaitsForSessionCatalogBeforeRevealingHeading(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	id := acceptSend(t, v, job, false)
	session := v.Model.Snapshot.Sessions[len(v.Model.Snapshot.Sessions)-1]
	v.Model.Snapshot.Sessions = v.Model.Snapshot.Sessions[:len(v.Model.Snapshot.Sessions)-1]
	ui.Frame()
	ui.Advance(800 * time.Millisecond)
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, session)
	ui.Frame()
	first := headingInk(ui.Image())
	ui.Advance(400 * time.Millisecond)
	end := headingInk(ui.Image())
	if v.Model.SessionID != id || end < 1000 || first > end/10 {
		t.Fatalf("late catalog skips heading entrance: first=%d end=%d", first, end)
	}
}

func TestSessionEntryDoesNotReplayOnHistoryNavigationOrExistingSend(t *testing.T) {
	for _, home := range []bool{true, false} {
		v, ui, job := sendFixture(t, home)
		id := acceptSend(t, v, job, true)
		ui.Frame()
		ui.Advance(120 * time.Millisecond)
		if home {
			v.Model.SessionID = ""
			ui.Frame()
			v.Model.SessionID = id
			ui.Frame()
		}
		first := headingInk(ui.Image())
		ui.Advance(500 * time.Millisecond)
		end := headingInk(ui.Image())
		if first != end {
			t.Fatalf("history navigation or existing send replays heading entrance (home=%t): first=%d end=%d", home, first, end)
		}
	}
}

func TestReducedMotionHomeCreationShowsHeadingAndSidebarImmediately(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	acceptSend(t, v, job, true)
	ui.Frame()
	first := headingInk(ui.Image())
	row, ok := ui.Find("New turn")
	ui.Advance(500 * time.Millisecond)
	end := headingInk(ui.Image())
	last, found := ui.Find("New turn")
	if first < 1000 || first != end || !ok || !found || row != last {
		t.Fatal("Reduce Motion still animates the newly created session context")
	}
}

func TestHomeEntryFollowsAcceptedCatalogThenConfirmedEvent(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	m := v.Model
	m.Snapshot.Workspaces = append(m.Snapshot.Workspaces, domain.Workspace{ID: "w2", Name: "Following workspace"})
	ui.Frame()
	before, _ := ui.Find("Following workspace")
	in := job.Input.(domain.Submission)
	op := domain.Operation{ID: in.OperationID, SessionID: "created", State: "accepted"}
	ack, _ := json.Marshal(op)
	job.Apply(m, ack, nil)
	session := domain.Session{ID: "created", WorkspaceID: "w", Status: "starting", Title: "New turn", Config: in.Config}
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, session)
	ui.Frame()
	firstRow, _ := ui.Find("Following workspace")
	if m.SessionID != "" || math.Abs(float64(firstRow.Y-before.Y)) > .5 {
		t.Fatal("accepted catalog inserts the sidebar row without its entrance")
	}
	ui.Advance(400 * time.Millisecond)
	fullRow, _ := ui.Find("Following workspace")
	if math.Abs(float64(fullRow.Y-before.Y-30)) > .5 {
		t.Fatal("accepted sidebar row never finishes entering")
	}
	op.State = "confirmed"
	session.Status = "running"
	if err := m.ApplyEvent(domain.Event{Sequence: m.Sequence + 1, SessionID: session.ID, Session: &session, Operation: &op}); err != nil {
		t.Fatal(err)
	}
	m.Details[session.ID] = domain.SessionDetail{Session: session, Items: []domain.Item{{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Final: true}}}
	ui.Frame()
	firstHeading := headingInk(ui.Image())
	confirmedRow, _ := ui.Find("Following workspace")
	ui.Advance(400 * time.Millisecond)
	endHeading := headingInk(ui.Image())
	if m.SessionID != session.ID || endHeading < 1000 || firstHeading > endHeading/10 {
		t.Fatalf("later confirmed event skips heading entrance: first=%d end=%d", firstHeading, endHeading)
	}
	if math.Abs(float64(confirmedRow.Y-fullRow.Y)) > .5 {
		t.Fatal("confirmation restarts an already visible sidebar entrance")
	}
}

func TestRejectedAcceptedSessionDoesNotReplayHeadingLater(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	m := v.Model
	in := job.Input.(domain.Submission)
	op := domain.Operation{ID: in.OperationID, SessionID: "created", State: "accepted"}
	ack, _ := json.Marshal(op)
	job.Apply(m, ack, nil)
	m.Snapshot.Sessions = append(m.Snapshot.Sessions, domain.Session{ID: "created", WorkspaceID: "w", Status: "failed", Title: "New turn"})
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	op.State = "rejected"
	m.ApplyOperation(op)
	ui.Frame()
	m.SessionID = "created"
	ui.Frame()
	first := headingInk(ui.Image())
	ui.Advance(400 * time.Millisecond)
	if first != headingInk(ui.Image()) {
		t.Fatal("opening a rejected submission replays its home heading entrance")
	}
}
