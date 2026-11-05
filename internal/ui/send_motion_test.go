package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
)

func sendFixture(t *testing.T, home bool) (*View, *n.Tester, app.Job) {
	t.Helper()
	m := layoutModel()
	m.ConnectionEpoch = "connected"
	if home {
		m.SessionID = ""
	}
	c := app.NewController("")
	v := New(m, c, "")
	ui := n.NewTester(v.View, 1200, 820)
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Apply != nil {
			job.Apply(m, []byte(`{}`), nil)
		}
	}
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("Explain a tree rotation.")
	if err := ui.Click("发送"); err != nil {
		t.Fatal(err)
	}
	for len(c.Jobs) > 0 {
		job := <-c.Jobs
		if job.Method == "session.submit" {
			return v, ui, job
		}
	}
	t.Fatal("submission did not reach controller")
	return nil, nil, app.Job{}
}

func acceptSend(t *testing.T, v *View, job app.Job, hydrate bool) string {
	t.Helper()
	m := v.Model
	in := job.Input.(domain.Submission)
	id := in.SessionID
	if id == "" {
		id = "created"
	}
	session := domain.Session{ID: id, WorkspaceID: "w", Status: "running", Title: "New turn", Config: in.Config}
	op := domain.Operation{ID: in.OperationID, SessionID: id, State: "confirmed"}
	body, _ := json.Marshal(op)
	job.Apply(m, body, nil)
	if hydrate {
		detail := m.Details[id]
		detail.Session = session
		detail.Items = append(detail.Items, domain.Item{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Final: true})
		m.Details[id] = detail
	}
	found := false
	for i, s := range m.Snapshot.Sessions {
		if s.ID == id {
			m.Snapshot.Sessions[i], found = session, true
		}
	}
	if !found {
		m.Snapshot.Sessions = append(m.Snapshot.Sessions, session)
	}
	return id
}

func TestHomeSendComposerDescendsWithoutDisappearingDuringHydration(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	before, _ := ui.Find("消息编辑器")
	acceptSend(t, v, job, false)
	ui.Frame()
	if _, ok := ui.Find("消息编辑器"); !ok {
		t.Fatal("accepted home send removes the composer while history hydrates")
	}
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("消息编辑器")
	in := job.Input.(domain.Submission)
	id := v.Model.SessionID
	v.Model.Details[id] = domain.SessionDetail{Session: domain.Session{ID: id}, Items: []domain.Item{{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Final: true}}}
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("消息编辑器")
	if !(middle.Y > before.Y+5 && middle.Y < end.Y-5) {
		t.Fatalf("home composer jumps instead of descending: before=%+v middle=%+v end=%+v", before, middle, end)
	}
	if !ui.Focused("消息输入") {
		t.Fatal("home send loses composer focus")
	}
}

func TestHomeSentMessageLeavesTheDraftTextPosition(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprint("delayed-history=", delayed), func(t *testing.T) {
			v, ui, job := sendFixture(t, true)
			source, _ := ui.Find("消息输入")
			acceptSend(t, v, job, !delayed)
			if delayed {
				ui.Frame()
				ui.Advance(100 * time.Millisecond)
				in := job.Input.(domain.Submission)
				id := v.Model.SessionID
				v.Model.Details[id] = domain.SessionDetail{Session: domain.Session{ID: id}, Items: []domain.Item{{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Final: true}}}
			}
			ui.Frame()
			first, ok := ui.Find("Explain a tree rotation.")
			if !ok || math.Abs(float64(first.Y-source.Y)) > 24 || math.Abs(float64(first.X-source.X)) > 24 {
				t.Fatalf("home message appears away from its draft: input=%+v message=%+v", source, first)
			}
			ui.Advance(160 * time.Millisecond)
			middle, _ := ui.Find("Explain a tree rotation.")
			ui.Advance(500 * time.Millisecond)
			end, ok := ui.Find("Explain a tree rotation.")
			if !ok || !(first.Y > middle.Y+20 && middle.Y > end.Y+20 && end.Y >= 80 && end.Y <= 125) {
				t.Fatalf("home message has no continuous path into history: first=%+v middle=%+v end=%+v", first, middle, end)
			}
		})
	}
}

func TestSentMessageRemainsVisibleAsItLeavesTheEditor(t *testing.T) {
	for _, home := range []bool{true, false} {
		t.Run(fmt.Sprint("home=", home), func(t *testing.T) {
			v, ui, job := sendFixture(t, home)
			source, _ := ui.Find("消息输入")
			acceptSend(t, v, job, true)
			ui.Frame()
			im := ui.Image()
			ink := 0
			for y := int(source.Y) + 2; y < int(source.Y)+22; y++ {
				for x := int(source.X) + 12; x < int(source.X)+180; x++ {
					p := im.RGBAAt(x, y)
					if p.R < 120 && p.G < 120 && p.B < 120 {
						ink++
					}
				}
			}
			if ink < 100 {
				t.Fatalf("sent text disappears under the empty composer: visible ink=%d", ink)
			}
		})
	}
}

func TestHomeHydrationKeepsNextDraftVisible(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	acceptSend(t, v, job, false)
	ui.Frame()
	ui.Advance(600 * time.Millisecond)
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	r, _ := ui.Find("消息输入")
	before := copyFrame(ui)
	ui.Type("Visible next draft")
	ui.Frame()
	after := ui.Image()
	changed := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if before.RGBAAt(x, y) != after.RGBAAt(x, y) {
				changed++
			}
		}
	}
	if changed == 0 {
		t.Fatal("next draft is editable but invisible while history hydrates")
	}
}

func TestHomeComposerStartsGentlyBeforeItsLongDescent(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	before, _ := ui.Find("消息编辑器")
	acceptSend(t, v, job, true)
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	early, _ := ui.Find("消息编辑器")
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("消息编辑器")
	progress := (early.Y - before.Y) / (end.Y - before.Y)
	if progress < .05 || progress > .4 {
		t.Fatalf("composer rushes its opening movement instead of accelerating gently: first 100 ms = %.1f%%", progress*100)
	}
}

func TestReturningHomeInterruptsFirstSendDescent(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	acceptSend(t, v, job, true)
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	v.Model.SessionID = ""
	ui.Frame()
	returned, _ := ui.Find("消息编辑器")
	baseline := n.NewTester(New(v.Model, nil, "").View, 1200, 820)
	centered, _ := baseline.Find("消息编辑器")
	if math.Abs(float64(returned.Y-centered.Y)) > .5 {
		t.Fatal("returning Home reverses the send descent instead of cancelling it")
	}
}

func TestAcceptedSendMovesNewMessageIntoReadingPosition(t *testing.T) {
	v, ui, job := sendFixture(t, false)
	d := v.Model.Details["s"]
	d.Items = nil
	for i := 0; i < 28; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: strings.Repeat("Earlier conversation. ", 8), Final: true})
	}
	v.Model.Details["s"] = d
	ui.Frame()
	acceptSend(t, v, job, true)
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	first, ok := ui.Find("Explain a tree rotation.")
	if !ok {
		t.Fatal("newly sent message never enters the viewport")
	}
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("Explain a tree rotation.")
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("Explain a tree rotation.")
	if !(first.Y > middle.Y+5 && middle.Y > end.Y+5 && end.Y >= 80 && end.Y <= 125) {
		t.Fatalf("sent message has no anchored glide: first=%+v middle=%+v end=%+v", first, middle, end)
	}
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("Next draft")
	d = v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "reply", Kind: "assistant", Text: "A small reply.", Final: true})
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	held, _ := ui.Find("Explain a tree rotation.")
	if math.Abs(float64(held.Y-end.Y)) > .5 || v.Model.Drafts["session:s"] != "Next draft" || !ui.Focused("消息输入") {
		t.Fatal("reply growth moves the sent message or steals the next draft")
	}
}

func TestSentMessageStartsGentlyAndRemainsInMotionAfter300ms(t *testing.T) {
	v, ui, job := sendFixture(t, false)
	d := v.Model.Details["s"]
	d.Items = nil
	for i := 0; i < 28; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: strings.Repeat("Earlier conversation. ", 8), Final: true})
	}
	v.Model.Details["s"] = d
	ui.Frame()
	acceptSend(t, v, job, true)
	ui.Frame()
	start, _ := ui.Find("Explain a tree rotation.")
	ui.Advance(100 * time.Millisecond)
	early, _ := ui.Find("Explain a tree rotation.")
	ui.Advance(200 * time.Millisecond)
	later, _ := ui.Find("Explain a tree rotation.")
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("Explain a tree rotation.")
	progress := (start.Y - early.Y) / (start.Y - end.Y)
	if progress > .4 || later.Y-end.Y < 10 {
		t.Fatalf("message rushes upward: first 100 ms = %.1f%%, remaining at 300 ms = %.1fpx", progress*100, later.Y-end.Y)
	}
}

func TestRejectedSendPreservesHomeDraftAndPosition(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	before, _ := ui.Find("消息编辑器")
	job.Apply(v.Model, nil, errors.New("not accepted"))
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	after, _ := ui.Find("消息编辑器")
	// The ordinary error notice takes one line beneath the centered composer;
	// it must not trigger the descent into the conversation dock.
	if v.Model.SessionID != "" || v.Model.Drafts["home"] != "Explain a tree rotation." || math.Abs(float64(before.Y-after.Y)) > 24 || before.H != after.H {
		t.Fatalf("failed send moves the composer or loses its draft: session=%q draft=%q before=%+v after=%+v", v.Model.SessionID, v.Model.Drafts["home"], before, after)
	}
}

func TestReducedMotionSendPositionsWithoutGliding(t *testing.T) {
	for _, home := range []bool{false, true} {
		v, ui, job := sendFixture(t, home)
		ui.SetPreferences(n.Preferences{ReduceMotion: true})
		acceptSend(t, v, job, true)
		ui.Frame()
		message, ok := ui.Find("Explain a tree rotation.")
		if !ok {
			t.Fatal("reduced-motion message missing")
		}
		composer, _ := ui.Find("消息编辑器")
		ui.Advance(500 * time.Millisecond)
		settled, _ := ui.Find("Explain a tree rotation.")
		dock, _ := ui.Find("消息编辑器")
		if message != settled || composer != dock {
			t.Fatal("reduced motion still moves the message or composer")
		}
	}
}

func TestSentAnchorReleasesForReadingAndResumesFollowingWhenFull(t *testing.T) {
	for _, read := range []bool{false, true} {
		v, ui, job := sendFixture(t, false)
		acceptSend(t, v, job, true)
		ui.Frame()
		ui.Advance(500 * time.Millisecond)
		if read {
			ui.Scroll(800, 350, 0, -100)
			ui.Frame()
		}
		before, _ := ui.Find("Explain a tree rotation.")
		d := v.Model.Details["s"]
		for i := 0; i < 25; i++ {
			d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("follow-", i), Kind: "assistant", Text: fmt.Sprint("Streamed paragraph ", i), Final: true})
		}
		v.Model.Details["s"] = d
		ui.Frame()
		ui.Advance(500 * time.Millisecond)
		if read {
			after, ok := ui.Find("Explain a tree rotation.")
			if !ok || math.Abs(float64(before.Y-after.Y)) > .5 {
				t.Fatalf("incoming output overrides the reader's released position: before=%+v after=%+v found=%t", before, after, ok)
			}
			ui.Scroll(800, 350, 0, 2000)
			ui.Frame()
			d.Items = append(d.Items, domain.Item{ID: "last", Kind: "assistant", Text: "Following resumed.", Final: true})
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(700 * time.Millisecond)
			if !ui.HasText("Following resumed.") {
				t.Fatal("returning to the bottom does not resume following")
			}
		} else if !ui.HasText("Streamed paragraph 24") {
			t.Fatal("sent anchor never releases to follow a reply that fills its reserved space")
		}
	}
}

func TestHomeWorkspaceSwitchCancelsPendingSendMotion(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	v.Model.Snapshot.Workspaces = append(v.Model.Snapshot.Workspaces, domain.Workspace{ID: "other", Name: "Other"})
	v.Model.WorkspaceID = "other"
	ui.Frame()
	acceptSend(t, v, job, true)
	ui.Frame()
	if v.Model.SessionID != "" {
		t.Fatal("acceptance navigated away from the chosen workspace")
	}
	v.Model.SessionID = "created"
	v.Model.WorkspaceID = "w"
	ui.Frame()
	first, _ := ui.Find("Explain a tree rotation.")
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("Explain a tree rotation.")
	if first != end {
		t.Fatal("opening a saved session replays the home send after switching workspaces")
	}
}

func TestHomeSendDoesNotStealSearchFocusOrReplayOnNavigation(t *testing.T) {
	v, ui, job := sendFixture(t, true)
	if err := ui.Click("搜索"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(200 * time.Millisecond)
	acceptSend(t, v, job, true)
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	if ui.Focused("消息输入") {
		t.Fatal("accepted send steals focus from the open search")
	}
	v.searchOpen = false
	v.Model.SessionID = ""
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	v.Model.SessionID = "created"
	ui.Frame()
	first, _ := ui.Find("Explain a tree rotation.")
	ui.Advance(500 * time.Millisecond)
	last, _ := ui.Find("Explain a tree rotation.")
	if first != last {
		t.Fatal("opening saved history replays a send transition")
	}
}

func TestRetainedOriginTransitionHonorsAnimationDelay(t *testing.T) {
	show := false
	source := n.Rect{X: 20, Y: 40, W: 100, H: 30}
	animation := n.Animation{Duration: 200 * time.Millisecond, Delay: 100 * time.Millisecond, Ease: n.Linear}
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Fill().Children(func() {
			if show {
				n.Text(c, "Moving text").Size(100, 30).Absolute().Left(200).Top(200).Transition(n.ElementTransition{From: &source, Animation: &animation, Lift: true})
			}
		})
	}, 500, 400)
	show = true
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	waiting, _ := ui.Find("Moving text")
	ui.Advance(140 * time.Millisecond)
	middle, _ := ui.Find("Moving text")
	ui.Advance(200 * time.Millisecond)
	end, _ := ui.Find("Moving text")
	if waiting.X != source.X || !(middle.X > source.X+20 && middle.X < end.X-20) || end.X != 200 {
		t.Fatalf("retained-origin transition ignores its delay: waiting=%+v middle=%+v end=%+v", waiting, middle, end)
	}
}

func TestSentAnchorHonorsAnimationDelay(t *testing.T) {
	state := &n.ListState{FollowEnd: true}
	ui := n.NewTester(func(c *n.Context) {
		n.List(c, state, 100, func(i int) { n.Text(c, fmt.Sprint("delay-row-", i)).Height(32) }).FillWidth().FillHeight()
	}, 800, 600)
	state.AnchorRow(95, 100, n.Animation{Duration: 200 * time.Millisecond, Delay: 100 * time.Millisecond, Ease: n.Linear})
	ui.Frame()
	start, ok := ui.Find("delay-row-95")
	if !ok {
		t.Fatal("sent row is missing")
	}
	ui.Advance(50 * time.Millisecond)
	waiting, ok := ui.Find("delay-row-95")
	if !ok || math.Abs(float64(start.Y-waiting.Y)) > .5 {
		t.Fatalf("sent anchor ignores delay: start=%+v waiting=%+v", start, waiting)
	}
	ui.Advance(150 * time.Millisecond)
	middle, _ := ui.Find("delay-row-95")
	ui.Advance(200 * time.Millisecond)
	end, ok := ui.Find("delay-row-95")
	if !ok || !(start.Y > middle.Y+20 && middle.Y > end.Y+20) || math.Abs(float64(end.Y-100)) > .5 {
		t.Fatalf("delayed anchor fails to settle: start=%+v middle=%+v end=%+v", start, middle, end)
	}
}
