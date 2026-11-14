package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"strings"
	"testing"
	"time"
)

func nativeQueueFixture() (*View, *n.Tester) {
	m := layoutModel()
	m.WorkerMinor = 2
	m.ConnectionEpoch = "connected"
	m.Snapshot.Sessions[0].Status = "running"
	d := m.Details["s"]
	d.Session = m.Snapshot.Sessions[0]
	d.Turns = []domain.Turn{{ID: "active", Status: "running"}}
	d.Queue = domain.QueueState{SessionID: "s", Revision: 1, Items: []domain.QueueItem{{ID: "one", Input: domain.Submission{OperationID: "dispatch-one", Text: "Review the rotation code"}}, {ID: "two", Input: domain.Submission{OperationID: "dispatch-two", Text: "Add a small example"}}}}
	m.Details["s"] = d
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1200, 820)
	v.gitLoads = map[string]*gitLoadState{}
	for len(v.Controller.Jobs) > 0 {
		<-v.Controller.Jobs
	}
	return v, ui
}
func TestQueueDisclosureInterpolatesHeightAndReversesWithoutMovingInput(t *testing.T) {
	v, ui := nativeQueueFixture()
	before, _ := ui.Find("消息编辑区")
	input, _ := ui.Find("消息输入")
	if err := ui.Click("待发送 2"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("消息编辑区")
	during, _ := ui.Find("消息输入")
	if middle.H <= before.H+2 || math.Abs(float64(input.Y-during.Y)) > .5 {
		t.Fatalf("height/focus anchor before=%+v mid=%+v input=%+v during=%+v", before, middle, input, during)
	}
	ui.Click("待发送 2")
	ui.Advance(30 * time.Millisecond)
	closing, _ := ui.Find("消息编辑区")
	if closing.H >= middle.H || closing.H <= before.H {
		t.Fatalf("collapse jumped %+v %+v %+v", before, middle, closing)
	}
	ui.Advance(200 * time.Millisecond)
	after, _ := ui.Find("消息编辑区")
	if math.Abs(float64(after.H-before.H)) > .5 {
		t.Fatal("height not restored", after, before)
	}
	if v.Model.QueueExpanded["s"] {
		t.Fatal("wrong disclosure state")
	}
}
func TestQueueEditAndKeyboardActionsReachWorker(t *testing.T) {
	for _, steer := range []bool{false, true} {
		v, ui := nativeQueueFixture()
		ui.Click("消息输入")
		ui.Type("follow up")
		mods := n.Modifiers(0)
		want := "turn.enqueue"
		if steer {
			mods = n.Cmd
			want = "turn.steer"
		}
		ui.Key(mods, n.KeyEnter)
		found := false
		for len(v.Controller.Jobs) > 0 {
			job := <-v.Controller.Jobs
			if job.Method == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("keyboard did not send %s", want)
		}
	}
	v, ui := nativeQueueFixture()
	v.Model.Drafts["session:s"] = "ordinary"
	ui.Frame()
	ui.Click("待发送 2")
	ui.Advance(200 * time.Millisecond)
	if err := ui.Click("编辑待发消息 1"); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	if v.Model.Drafts["session:s"] != "Review the rotation code" || !ui.Focused("消息输入") {
		t.Fatal("edit not moved into focused composer")
	}
	ui.Click("取消编辑")
	ui.Frame()
	if v.Model.Drafts["session:s"] != "ordinary" {
		t.Fatal("draft not restored")
	}
}
func TestEmptyEnterNeverStopsAndReducedDisclosureIsImmediate(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("消息输入")
	ui.Key(0, n.KeyEnter)
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; job.Method == "turn.interrupt" {
			t.Fatal("empty Enter stopped turn")
		}
	}
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	before, _ := ui.Find("消息编辑区")
	ui.Click("待发送 2")
	ui.Frame()
	after, _ := ui.Find("消息编辑区")
	if after.H < before.H+60 {
		t.Fatal("reduce motion still interpolates height", before, after)
	}
}

func TestSteerReusesComposerSendMotion(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("消息输入")
	ui.Type("steer now")
	ui.Key(n.Cmd, n.KeyEnter)
	if v.sent == nil || v.sent.source.W <= 0 {
		t.Fatal("Steer has no composer-to-history motion")
	}
}
func TestRemovingQueuedItemKeepsRemainingRowVisible(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, ui := nativeQueueFixture()
		ui.SetDark(dark)
		ui.Click("待发送 2")
		ui.Advance(250 * time.Millisecond)
		before, _ := ui.Find("Add a small example")
		q := v.Model.Queue()
		q.Revision++
		q.Items = q.Items[1:]
		v.Model.SetQueue(q)
		ui.Frame()
		for frame := 0; frame <= 12; frame++ {
			current, ok := ui.Find("Add a small example")
			if !ok || current.H < before.H-.5 || math.Abs(float64(current.Y-before.Y)) > 1 {
				t.Fatalf("remaining queue row flickers during removal: dark=%t frame=%d before=%+v current=%+v", dark, frame, before, current)
			}
			ui.Advance(16 * time.Millisecond)
		}
	}
}

func TestQueueSurfaceJoinsComposerWithoutGap(t *testing.T) {
	_, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(250 * time.Millisecond)
	q, _ := ui.Find("待发送队列")
	composer, _ := ui.Find("消息编辑器")
	left, right := q.X-composer.X, composer.X+composer.W-q.X-q.W
	if math.Abs(float64(left)) > .5 || math.Abs(float64(right)) > .5 {
		t.Fatal("queue/composer unequal side insets", q, composer)
	}
	if math.Abs(float64(q.Y+q.H-17-composer.Y)) > .5 {
		t.Fatal("queue/composer gap", q, composer)
	}
}

func TestRunningComposerNeverShowsSendDisclosure(t *testing.T) {
	_, ui := nativeQueueFixture()
	for _, text := range []string{"", "next"} {
		if text != "" {
			ui.Click("消息输入")
			ui.Type(text)
		}
		if _, ok := ui.Find("发送操作"); ok {
			t.Fatal("composer still shows a send disclosure", text)
		}
		primary := "停止当前轮"
		if text != "" {
			primary = "加入队列"
		}
		if _, ok := ui.Find(primary); !ok {
			t.Fatal("composer lost its primary action", primary)
		}
	}
}

func TestQueueLabelsShareComposerBranchColumn(t *testing.T) {
	v, ui := nativeQueueFixture()
	v.Model.SetGitStatus("session:s", domain.GitStatus{Applicable: true, Branch: "main", Repository: "/fixture"})
	ui.Frame()
	ui.Click("待发送 2")
	ui.Advance(250 * time.Millisecond)
	heading, _ := ui.Find("待发送")
	preview, _ := ui.Find("Review the rotation code")
	branch, _ := ui.Find("main")
	// Branch's inline 12 px icon and 5 px gap precede its text.
	branchTextX := branch.X + 17
	if math.Abs(float64(heading.X-branchTextX)) > .5 || math.Abs(float64(preview.X-branchTextX)) > .5 {
		t.Fatal("queue text differs from composer branch column", heading, preview, branch)
	}
	ui.Click("编辑待发消息 1")
	ui.Frame()
	edit, _ := ui.Find("编辑待发消息")
	if math.Abs(float64(edit.X-branchTextX)) > .5 {
		t.Fatal("queue editing label has a different inset", edit, branch)
	}
}

func TestDoubleClickQueueRowEditsAndRetainsOrdinaryDraft(t *testing.T) {
	v, ui := nativeQueueFixture()
	v.Model.Drafts["session:s"] = "ordinary draft"
	ui.Frame()
	ui.Click("待发送 2")
	ui.Advance(200 * time.Millisecond)
	row, _ := ui.Find("Review the rotation code")
	ui.ClickAt(row.X+20, row.Y+row.H/2)
	ui.Advance(80 * time.Millisecond)
	ui.ClickAt(row.X+20, row.Y+row.H/2)
	if v.Model.QueueEdits["s"].ID != "one" || v.Model.Drafts["session:s"] != "Review the rotation code" || !ui.Focused("消息输入") {
		t.Fatal("double click did not edit", v.Model.QueueEdits, v.Model.Drafts)
	}
	ui.Click("取消编辑")
	if v.Model.Drafts["session:s"] != "ordinary draft" {
		t.Fatal("double click lost displaced draft")
	}
}

func TestDragQueueRowCommitsOnlyOnDrop(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(200 * time.Millisecond)
	first, _ := ui.Find("Review the rotation code")
	second, _ := ui.Find("Add a small example")
	ui.Press(second.X+20, second.Y+second.H/2)
	ui.Move(first.X+20, first.Y+2)
	ui.Advance(50 * time.Millisecond)
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; job.Method == "queue.reorder" {
			t.Fatal("reordered before drop")
		}
	}
	ui.Release(first.X+20, first.Y+2)
	found := false
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "queue.reorder" {
			in := job.Input.(protocol.QueueInput)
			if in.ID != "two" || in.BeforeID != "one" || in.Revision != 1 {
				t.Fatal(in)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("drag did not submit reorder")
	}
	if len(v.Model.QueueEdits) > 0 {
		t.Fatal("drag triggered editing")
	}
}

func TestQueueDragRejectsConcurrentChangesAndCancelsOutsideOrEscape(t *testing.T) {
	for _, finish := range []string{"changed", "outside", "escape"} {
		t.Run(finish, func(t *testing.T) {
			v, ui := nativeQueueFixture()
			ui.Click("待发送 2")
			ui.Advance(200 * time.Millisecond)
			first, _ := ui.Find("Review the rotation code")
			second, _ := ui.Find("Add a small example")
			ui.Press(second.X+20, second.Y+second.H/2)
			ui.Move(first.X+20, first.Y+2)
			switch finish {
			case "changed":
				q := v.Model.Queue()
				q.Revision++
				v.Model.SetQueue(q)
				ui.Frame()
				ui.Release(first.X+20, first.Y+2)
			case "outside":
				ui.Move(5, 5)
				ui.Release(5, 5)
			case "escape":
				ui.Key(0, n.KeyEscape)
				ui.Release(first.X+20, first.Y+2)
			}
			for len(v.Controller.Jobs) > 0 {
				if job := <-v.Controller.Jobs; job.Method == "queue.reorder" {
					t.Fatal("cancelled/stale drag reordered queue")
				}
			}
		})
	}
}

func TestQueueReorderAcknowledgementAnimatesRowMovement(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(200 * time.Millisecond)
	before, _ := ui.Find("Add a small example")
	q := v.Model.Queue()
	q.Items = []domain.QueueItem{q.Items[1], q.Items[0]}
	q.Revision++
	v.Model.SetQueue(q)
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	mid, _ := ui.Find("Add a small example")
	ui.Advance(180 * time.Millisecond)
	end, _ := ui.Find("Add a small example")
	if !(mid.Y < before.Y-.5 && mid.Y > end.Y+.5) {
		t.Fatal("reorder jumped instead of moving", before, mid, end)
	}
}

func TestQueueDragMakesRoomBeforeDropAndHoldsUntilAcknowledged(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(200 * time.Millisecond)
	first, _ := ui.Find("Review the rotation code")
	second, _ := ui.Find("Add a small example")
	ui.Press(second.X+20, second.Y+second.H/2)
	ui.Move(first.X+20, first.Y+2)
	ui.Advance(70 * time.Millisecond)
	mid, _ := ui.Find("Review the rotation code")
	if mid.Y <= first.Y+.5 || mid.Y >= first.Y+32-.5 {
		t.Fatal("other row does not move during drag", first, mid)
	}
	ui.Advance(180 * time.Millisecond)
	moved, _ := ui.Find("Review the rotation code")
	if math.Abs(float64(moved.Y-first.Y-32)) > 1 || v.Model.Queue().Items[0].ID != "one" {
		t.Fatal("preview changed authoritative order or failed to make room", first, moved)
	}
	ui.Release(first.X+20, first.Y+2)
	ui.Advance(80 * time.Millisecond)
	held, _ := ui.Find("Review the rotation code")
	if math.Abs(float64(held.Y-moved.Y)) > 1 {
		t.Fatal("drop preview snapped back before ACK", moved, held)
	}
}

func TestQueueDragNeverRecapturesRevisionAcrossDispatch(t *testing.T) {
	v, ui := nativeQueueFixture()
	q := v.Model.Queue()
	q.Items = append(q.Items, domain.QueueItem{ID: "three", Input: domain.Submission{Text: "third queued message"}})
	q.Revision++
	v.Model.SetQueue(q)
	ui.Frame()
	ui.Click("待发送 3")
	ui.Advance(200 * time.Millisecond)
	first, _ := ui.Find("Review the rotation code")
	third, _ := ui.Find("third queued message")
	ui.Press(third.X+20, third.Y+third.H/2)
	ui.Move(first.X+20, first.Y+2)
	q = v.Model.Queue()
	q.Revision++
	q.Items[0].State = "dispatching"
	v.Model.SetQueue(q)
	ui.Frame()
	q = v.Model.Queue()
	q.Revision++
	q.Items = q.Items[1:]
	v.Model.SetQueue(q)
	ui.Frame()
	target, _ := ui.Find("Add a small example")
	ui.Move(target.X+20, target.Y+2)
	ui.Release(target.X+20, target.Y+2)
	for len(v.Controller.Jobs) > 0 {
		if job := <-v.Controller.Jobs; job.Method == "queue.reorder" {
			t.Fatal("held drag captured a new revision after dispatch")
		}
	}
}

func TestQueueLivePreviewSettlesOnSuccessAndRollsBackOnFailure(t *testing.T) {
	for _, reject := range []bool{false, true} {
		v, ui := nativeQueueFixture()
		v.Model.Drafts["session:s"] = "ordinary"
		ui.Frame()
		ui.Click("待发送 2")
		ui.Advance(200 * time.Millisecond)
		first, _ := ui.Find("Review the rotation code")
		initialQueue, _ := ui.Find("待发送队列")
		second, _ := ui.Find("Add a small example")
		ui.Press(second.X+20, second.Y+second.H/2)
		ui.Move(first.X+20, first.Y+2)
		ui.Advance(180 * time.Millisecond)
		ui.Release(first.X+20, first.Y+2)
		var reorder app.Job
		for len(v.Controller.Jobs) > 0 {
			job := <-v.Controller.Jobs
			if job.Method == "queue.reorder" {
				reorder = job
			}
		}
		if reorder.Apply == nil {
			t.Fatal("no reorder command")
		}
		if reject {
			reorder.Apply(v.Model, nil, errors.New("queue changed"))
		} else {
			q := v.Model.Queue()
			q.Revision++
			q.Items = []domain.QueueItem{q.Items[1], q.Items[0]}
			body, _ := json.Marshal(q)
			reorder.Apply(v.Model, body, nil)
		}
		ui.Frame()
		ui.Advance(220 * time.Millisecond)
		row, _ := ui.Find("Review the rotation code")
		finalQueue, _ := ui.Find("待发送队列")
		want := first.Y - initialQueue.Y + 32
		if reject {
			want = first.Y - initialQueue.Y
		}
		if math.Abs(float64(row.Y-finalQueue.Y-want)) > 1 || v.queueDrag != nil || v.Model.Drafts["session:s"] != "ordinary" {
			t.Fatal("drop did not settle/rollback cleanly", row, want, v.queueDrag, v.Model.Drafts)
		}
	}
}

func TestQueueLiveReorderHonorsReducedMotion(t *testing.T) {
	_, ui := nativeQueueFixture()
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Click("待发送 2")
	ui.Frame()
	first, _ := ui.Find("Review the rotation code")
	second, _ := ui.Find("Add a small example")
	ui.Press(second.X+20, second.Y+second.H/2)
	ui.Move(first.X+20, first.Y+2)
	ui.Frame()
	moved, _ := ui.Find("Review the rotation code")
	if math.Abs(float64(moved.Y-first.Y-32)) > 1 {
		t.Fatal("reduced motion interpolated preview", first, moved)
	}
}

// Mirrors the worker's confirmed history insertion followed by queue consumption.
func confirmQueuedInput(v *View) {
	q := v.Model.Queue()
	item := q.Items[0]
	d := v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "input:" + item.Input.OperationID, Kind: "user", Text: item.Input.Text, Status: "sent", Final: true})
	v.Model.Details["s"] = d
	q.Items = q.Items[1:]
	q.Revision++
	v.Model.SetQueue(q)
}

func TestConfirmedQueueSendTravelsFromQueueWithoutLosingDraft(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "immediate"}[immediate], func(t *testing.T) {
			v, ui := nativeQueueFixture()
			v.Model.Drafts["session:s"] = "keep this ordinary draft"
			ui.Frame()
			ui.Click("消息输入")
			ui.Click("待发送 2")
			ui.Advance(250 * time.Millisecond)
			source, _ := ui.Find("Review the rotation code")
			if immediate {
				historyCount := len(v.Model.Details["s"].Items)
				ui.Click("立即发送待发消息 1")
				if len(v.Model.Details["s"].Items) != historyCount {
					t.Fatal("queue send fabricated history before acceptance")
				}
			}
			confirmQueuedInput(v)
			ui.Frame()
			first, ok := ui.Find("Review the rotation code")
			ui.Advance(160 * time.Millisecond)
			middle, _ := ui.Find("Review the rotation code")
			ui.Advance(500 * time.Millisecond)
			end, _ := ui.Find("Review the rotation code")
			if !ok || math.Abs(float64(first.Y-source.Y)) > 24 || !(first.Y > middle.Y+15 && middle.Y > end.Y+15) {
				t.Fatalf("queue message jumps into history: source=%+v first=%+v middle=%+v end=%+v", source, first, middle, end)
			}
			if v.Model.Drafts["session:s"] != "keep this ordinary draft" {
				t.Fatal("queue entrance changed ordinary draft")
			}
		})
	}
}

func TestQueueEditDimsContentOverOpaqueSurfaceAndPreviewsLiveText(t *testing.T) {
	v, _ := nativeQueueFixture()
	ui := n.NewTester(func(c *n.Context) { theme := *c.Theme(); theme.Dark = true; c.SetTheme(&theme); v.View(c) }, 1200, 820)
	ui.Click("待发送 2")
	ui.Advance(250 * time.Millisecond)
	queueColor := func() uint8 {
		q, _ := ui.Find("待发送队列")
		return ui.Image().RGBAAt(int(q.X+q.W/2), int(q.Y+16)).R
	}
	queueContrast := func() int {
		label, _ := ui.Find("待发送")
		background := int(queueColor())
		contrast := 0
		im := ui.Image()
		for y := int(label.Y); y < int(label.Y+label.H); y++ {
			for x := int(label.X); x < int(label.X+label.W); x++ {
				contrast = max(contrast, abs(int(im.RGBAAt(x, y).R)-background))
			}
		}
		return contrast
	}
	ui.Move(0, 0)
	ui.Advance(200 * time.Millisecond)
	background := queueColor()
	normal := queueContrast()
	ui.Click("编辑待发消息 1")
	ui.Frame()
	first := queueContrast()
	if !ui.Focused("消息输入") {
		t.Fatal("fading editor lost focus")
	}
	ui.Advance(60 * time.Millisecond)
	middle := queueContrast()
	middleBackground := queueColor()
	ui.Advance(200 * time.Millisecond)
	dim := queueContrast()
	if queueColor() != background || middleBackground != background {
		t.Error("editing makes the queue backplate transparent", background, middleBackground, queueColor())
	}
	if math.Abs(float64(first)-float64(normal)) > 1 || !(middle < normal-2 && middle > dim+1) || dim >= normal-4 {
		t.Fatal("queue does not gradually dim for editing", normal, first, middle, dim)
	}
	v.Model.Drafts["session:s"] = "Live preview text"
	ui.Frame()
	if !ui.HasText("Live preview text") {
		t.Fatal("queued preview does not follow editor")
	}
	if v.Model.Queue().Items[0].Input.Text != "Review the rotation code" {
		t.Fatal("live preview persisted before save")
	}
	ui.Click("取消编辑")
	ui.Frame()
	closing := queueContrast()
	ui.Advance(60 * time.Millisecond)
	restoring := queueContrast()
	ui.Advance(250 * time.Millisecond)
	restored := queueContrast()
	if math.Abs(float64(closing)-float64(dim)) > 1 || !(restoring > dim+1 && restoring < normal-1) || math.Abs(float64(restored)-float64(normal)) > 1 {
		t.Fatal("queue does not gradually recover on cancel", dim, closing, restoring, restored)
	}
}

func TestQueueTransfersHonorReducedMotion(t *testing.T) {
	for _, edit := range []bool{false, true} {
		v, ui := nativeQueueFixture()
		ui.SetPreferences(n.Preferences{ReduceMotion: true})
		ui.Click("待发送 2")
		ui.Frame()
		label := "Review the rotation code"
		if edit {
			ui.Click("编辑待发消息 1")
			label = "消息输入"
		} else {
			confirmQueuedInput(v)
			ui.Frame()
		}
		first, ok := ui.Find(label)
		ui.Advance(600 * time.Millisecond)
		end, _ := ui.Find(label)
		if !ok || first != end {
			t.Fatal("reduced motion queue transfer still moves", first, end)
		}
	}
}

func TestCollapsedQueueSendStartsAtDisclosure(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Frame()
	source, _ := ui.Find("待发送 2")
	confirmQueuedInput(v)
	ui.Frame()
	first, _ := ui.Find("Review the rotation code")
	ui.Advance(650 * time.Millisecond)
	end, _ := ui.Find("Review the rotation code")
	if math.Abs(float64(first.Y-source.Y)) > 24 || first.Y < end.Y+100 {
		t.Fatal("collapsed queue send has no disclosure origin", source, first, end)
	}
}
func TestQueueDispatchDoesNotMoveHistoryReaderOrReplayOnReopen(t *testing.T) {
	v, ui := nativeQueueFixture()
	d := v.Model.Details["s"]
	for i := 0; i < 35; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint("old-", i), Kind: "assistant", Text: strings.Repeat("Earlier conversation. ", 7), Final: true})
	}
	v.Model.Details["s"] = d
	ui.Frame()
	ui.Advance(600 * time.Millisecond)
	ui.Scroll(800, 350, 0, -800)
	ui.Frame()
	first, last := v.lists["s"].Visible()
	confirmQueuedInput(v)
	ui.Frame()
	ui.Advance(650 * time.Millisecond)
	a, b := v.lists["s"].Visible()
	if a != first || b != last {
		t.Fatal("automatic queued input pulls history reader", first, last, a, b)
	}
	v.Model.SessionID = ""
	ui.Frame()
	v.Model.SessionID = "s"
	ui.Frame()
	if v.sent != nil {
		t.Fatal("saved history replays queue departure on reopen")
	}
}

func TestQueueEditDockHeightExpandsAndReversesContinuously(t *testing.T) {
	v, ui := nativeQueueFixture()
	q := v.Model.Queue()
	q.Items[0].Input.Text = "First line\nSecond line\nThird line\nFourth line"
	q.Revision++
	v.Model.SetQueue(q)
	ui.Click("待发送 2")
	ui.Advance(250 * time.Millisecond)
	before, _ := ui.Find("消息编辑器")
	ui.Click("编辑待发消息 1")
	ui.Frame()
	first, _ := ui.Find("消息编辑器")
	ui.Advance(100 * time.Millisecond)
	middle, _ := ui.Find("消息编辑器")
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("消息编辑器")
	if math.Abs(float64(first.H-before.H)) > 2 || !(middle.H > before.H+3 && middle.H < end.H-3) || math.Abs(float64(end.Y+end.H-before.Y-before.H)) > 1 {
		t.Fatalf("edit dock jumps instead of growing at its fixed bottom: before=%+v first=%+v middle=%+v end=%+v", before, first, middle, end)
	}
	ui.Click("取消编辑")
	ui.Frame()
	closing, _ := ui.Find("消息编辑器")
	ui.Advance(100 * time.Millisecond)
	shrinking, _ := ui.Find("消息编辑器")
	ui.Advance(400 * time.Millisecond)
	closed, _ := ui.Find("消息编辑器")
	if math.Abs(float64(closing.H-end.H)) > 2 || !(shrinking.H < end.H-3 && shrinking.H > before.H+3) || math.Abs(float64(closed.H-before.H)) > 1 {
		t.Fatalf("cancel edit dock jumps: expanded=%+v first=%+v middle=%+v end=%+v", end, closing, shrinking, closed)
	}
}

func TestReducedMotionQueueEditDockChangesWithoutResizingAnimation(t *testing.T) {
	_, ui := nativeQueueFixture()
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Click("待发送 2")
	ui.Frame()
	ui.Click("编辑待发消息 1")
	ui.Frame()
	first, _ := ui.Find("消息编辑器")
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("消息编辑器")
	if first != end {
		t.Fatal("Reduce Motion still animates edit dock height", first, end)
	}
}

func TestRemovingLastQueuedMessageCollapsesWholeQueueAndPreservesInput(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		v, ui := nativeQueueFixture()
		v.Model.QueueExpanded["s"] = expanded
		ui.Frame()
		ui.Advance(250 * time.Millisecond)
		before, _ := ui.Find("待发送队列")
		input, _ := ui.Find("消息输入")
		q := v.Model.Queue()
		q.Items = nil
		q.Revision++
		v.Model.SetQueue(q)
		ui.Frame()
		first, ok := ui.Find("待发送队列")
		ui.Advance(60 * time.Millisecond)
		middle, midVisible := ui.Find("待发送队列")
		during, _ := ui.Find("消息输入")
		ui.Advance(250 * time.Millisecond)
		_, left := ui.Find("待发送队列")
		if !ok || !midVisible || math.Abs(float64(first.H-before.H)) > 2 || !(middle.H > 1 && middle.H < before.H-2) || left || math.Abs(float64(input.Y-during.Y)) > 1 {
			t.Fatalf("last removal instantly drops queue: expanded=%t before=%+v first=%+v middle=%+v visible=%t/%t/%t input=%+v during=%+v", expanded, before, first, middle, ok, midVisible, left, input, during)
		}
	}
}

func TestLastQueuedDispatchDoesNotRestartDuringQueueCollapse(t *testing.T) {
	v, ui := nativeQueueFixture()
	q := v.Model.Queue()
	q.Items = q.Items[:1]
	q.Revision++
	v.Model.SetQueue(q)
	v.Model.QueueExpanded["s"] = true
	ui.Frame()
	ui.Advance(250 * time.Millisecond)
	confirmQueuedInput(v)
	ui.Frame()
	first, _ := ui.Find("Review the rotation code")
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("Review the rotation code")
	ui.Advance(160 * time.Millisecond)
	later, _ := ui.Find("Review the rotation code")
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("Review the rotation code")
	if !(first.Y > middle.Y+5 && middle.Y > later.Y+5 && later.Y > end.Y+5) {
		t.Fatal("last queued entrance restarts as its source collapses", first, middle, later, end)
	}
}

func TestEnqueuedRowSlidesInFromQueueBottom(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(250 * time.Millisecond)
	ui.Click("消息输入")
	ui.Type("New queued instruction")
	source, _ := ui.Find("消息输入")
	ui.Key(0, n.KeyEnter)
	var send app.Job
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "turn.enqueue" {
			send = job
		}
	}
	in := send.Input.(domain.Submission)
	q := v.Model.Queue()
	q.Items = append(q.Items, domain.QueueItem{ID: in.OperationID, Input: domain.Submission{OperationID: "dispatch-new", Text: in.Text}})
	q.Revision++
	v.Model.SetQueue(q)
	body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	send.Apply(v.Model, body, nil)
	ui.Frame()
	first, _ := ui.Find("New queued instruction")
	ui.Advance(120 * time.Millisecond)
	middle, ok := ui.Find("New queued instruction")
	button, _ := ui.Find("移除待发消息 3")
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("New queued instruction")
	if !ok || !(middle.Y > end.Y+3 && middle.Y < source.Y) || math.Abs(float64(middle.X-end.X)) > .5 || math.Abs(float64(middle.Y-button.Y-3)) > .5 {
		t.Fatalf("enqueue does not slide the whole row within queue: source=%+v first=%+v middle=%+v button=%+v end=%+v", source, first, middle, button, end)
	}
	if v.Model.Drafts["session:s"] != "" || len(v.Model.Details["s"].Items) != 1 {
		t.Fatal("queue entrance changed acceptance or sent history")
	}
}

func TestSavedQueueEditFadesWithoutMovingItsRow(t *testing.T) {
	v, ui := nativeQueueFixture()
	v.Model.Drafts["session:s"] = "ordinary draft"
	ui.Frame()
	ui.Click("待发送 2")
	ui.Advance(250 * time.Millisecond)
	ui.Click("编辑待发消息 1")
	ui.Advance(500 * time.Millisecond)
	v.Model.Drafts["session:s"] = "Edited queued instruction"
	ui.Frame()
	ui.Advance(200 * time.Millisecond)
	before, _ := ui.Find("Edited queued instruction")
	zone, _ := ui.Find("待发送队列")
	offset := before.Y - zone.Y
	ui.Click("保存编辑")
	var save app.Job
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "queue.edit" {
			save = job
		}
	}
	q := v.Model.Queue()
	q.Items[0].Input.Text = "Edited queued instruction"
	q.Revision++
	body, _ := json.Marshal(q)
	save.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	middle, ok := ui.Find("Edited queued instruction")
	midZone, _ := ui.Find("待发送队列")
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("Edited queued instruction")
	endZone, _ := ui.Find("待发送队列")
	if !ok || math.Abs(float64(middle.Y-midZone.Y-offset)) > .5 || math.Abs(float64(end.Y-endZone.Y-offset)) > .5 {
		t.Fatal("saving moves queue text instead of restoring its opacity", before, middle, end)
	}
	if v.Model.Drafts["session:s"] != "ordinary draft" || !ui.Focused("消息输入") {
		t.Fatal("saving animation lost ordinary draft/focus")
	}
}

func TestQueueArrivalWaitsForConfirmationAndPreservesNextDraft(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(250 * time.Millisecond)
	ui.Click("消息输入")
	ui.Type("Waiting for enqueue confirmation")
	ui.Key(0, n.KeyEnter)
	var send app.Job
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "turn.enqueue" {
			send = job
		}
	}
	in := send.Input.(domain.Submission)
	q := v.Model.Queue()
	q.Items = append(q.Items, domain.QueueItem{ID: in.OperationID, Input: domain.Submission{OperationID: "dispatch-waiting", Text: in.Text}})
	q.Revision++
	v.Model.SetQueue(q)
	ui.Frame()
	if _, flying := ui.Find("正在加入队列"); flying || v.Model.Drafts["session:s"] != in.Text {
		t.Fatal("arrival departs before confirmation")
	}
	v.Model.Drafts["session:s"] = "newer draft"
	ui.Frame()
	body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	send.Apply(v.Model, body, nil)
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	if v.Model.Drafts["session:s"] != "newer draft" || !ui.Focused("消息输入") {
		t.Fatal("arrival erases or blurs the next draft")
	}
}
func TestRejectedQueueArrivalLeavesDraftInEditor(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("消息输入")
	ui.Type("Keep this rejected input")
	ui.Key(0, n.KeyEnter)
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "turn.enqueue" {
			job.Apply(v.Model, nil, &protocol.RemoteError{Code: "notSent", Message: "rejected"})
		}
	}
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	if _, moving := ui.Find("正在加入队列"); moving || v.Model.Drafts["session:s"] != "Keep this rejected input" || len(v.Model.Queue().Items) != 2 {
		t.Fatal("rejected enqueue moved input or changed queue")
	}
}
func TestCollapsedQueueArrivalKeepsDisclosureCollapsed(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("消息输入")
	ui.Type("A collapsed arrival")
	ui.Key(0, n.KeyEnter)
	var send app.Job
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "turn.enqueue" {
			send = job
		}
	}
	in := send.Input.(domain.Submission)
	q := v.Model.Queue()
	q.Items = append(q.Items, domain.QueueItem{ID: in.OperationID, Input: domain.Submission{OperationID: "dispatch-collapsed", Text: in.Text}})
	q.Revision++
	v.Model.SetQueue(q)
	body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	send.Apply(v.Model, body, nil)
	ui.Frame()
	if _, flying := ui.Find("正在加入队列"); flying {
		t.Fatal("collapsed queue creates a flying text overlay")
	}
	ui.Advance(500 * time.Millisecond)
	if _, moving := ui.Find("正在加入队列"); moving || v.Model.QueueExpanded["s"] {
		t.Fatal("arrival persists or forces disclosure open")
	}
}

func TestQueueKeepsComposerTopCornersRounded(t *testing.T) {
	v, _ := nativeQueueFixture()
	ui := n.NewTester(func(c *n.Context) { theme := *c.Theme(); theme.Dark = true; c.SetTheme(&theme); v.View(c) }, 1200, 820)
	ui.Advance(300 * time.Millisecond)
	composer, _ := ui.Find("消息编辑器")
	corner := ui.Image().RGBAAt(int(composer.X+2), int(composer.Y+2))
	center := ui.Image().RGBAAt(int(composer.X+composer.W/2), int(composer.Y+4))
	arc := ui.Image().RGBAAt(int(composer.X+5), int(composer.Y+5))
	if arc.R <= max(corner.R, center.R)+4 {
		t.Fatal("queued messages square off composer top corner", corner, arc, center, composer)
	}
}
func TestQueueAppearsAndDisappearsBySlidingWithoutFading(t *testing.T) {
	v, _ := nativeQueueFixture()
	ui := n.NewTester(func(c *n.Context) { theme := *c.Theme(); theme.Dark = true; c.SetTheme(&theme); v.View(c) }, 1200, 820)
	v.Model.QueueExpanded["s"] = true
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	before, _ := ui.Find("待发送队列")
	sample := func(r n.Rect) uint8 {
		composer, _ := ui.Find("消息编辑器")
		visible := min(r.H, max(composer.Y-r.Y, 0))
		return ui.Image().RGBAAt(int(r.X+r.W/2), int(r.Y+visible/2)).R
	}
	solid := sample(before)
	q := v.Model.Queue()
	saved := q
	q.Items = nil
	q.Revision++
	v.Model.SetQueue(q)
	ui.Frame()
	ui.Advance(60 * time.Millisecond)
	hiding, ok := ui.Find("待发送队列")
	if !ok || hiding.H >= before.H-2 || math.Abs(float64(sample(hiding))-float64(solid)) > 2 {
		t.Fatal("queue fades while hiding instead of sliding", before, hiding, solid, sample(hiding))
	}
	ui.Advance(300 * time.Millisecond)
	input, _ := ui.Find("消息输入")
	saved.Revision = q.Revision + 1
	v.Model.SetQueue(saved)
	ui.Frame()
	first, _ := ui.Find("待发送队列")
	ui.Advance(60 * time.Millisecond)
	showing, ok := ui.Find("待发送队列")
	shownColor := sample(showing)
	ui.Advance(300 * time.Millisecond)
	end, _ := ui.Find("待发送队列")
	after, _ := ui.Find("消息输入")
	if !ok || first.H > 18 || !(showing.H > 18 && showing.H < end.H-2) || math.Abs(float64(sample(end))-float64(solid)) > 2 || math.Abs(float64(shownColor)-float64(solid)) > 2 || math.Abs(float64(input.Y-after.Y)) > 1 {
		t.Fatal("queue does not slide from composer top", first, showing, end, input, after)
	}
}

func TestQueueSideEdgesContinueBehindComposerRoundedTop(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, _ := nativeQueueFixture()
		ui := n.NewTester(func(c *n.Context) { theme := *c.Theme(); theme.Dark = dark; c.SetTheme(&theme); v.View(c) }, 1200, 820)
		ui.Advance(300 * time.Millisecond)
		composer, _ := ui.Find("消息编辑器")
		for _, x := range []float32{composer.X, composer.X + composer.W - 1} {
			for _, dy := range []float32{-3, 2, 6, 10, 15, 20} {
				p := ui.Image().RGBAAt(int(x), int(composer.Y+dy))
				if dark && p.R < 40 || !dark && p.R > 245 {
					t.Fatalf("queue/composer edge has a gap: dark=%t x=%.1f y=%.1f pixel=%v", dark, x, composer.Y+dy, p)
				}
			}
		}
	}
}

func TestCancelQueueEditFadesBackAndDiscardsLocalPreview(t *testing.T) {
	v, ui := nativeQueueFixture()
	v.Model.Drafts["session:s"] = "ordinary draft"
	ui.Frame()
	ui.Click("待发送 2")
	ui.Advance(250 * time.Millisecond)
	ui.Click("编辑待发消息 1")
	ui.Advance(500 * time.Millisecond)
	before, _ := ui.Find("Review the rotation code")
	zone, _ := ui.Find("待发送队列")
	offset := before.Y - zone.Y
	v.Model.Drafts["session:s"] = "discard these unsaved edits"
	ui.Frame()
	if !ui.HasText("discard these unsaved edits") {
		t.Fatal("queue does not preview local edit")
	}
	ui.Click("取消编辑")
	ui.Frame()
	if v.Model.Drafts["session:s"] != "ordinary draft" || !ui.Focused("消息输入") {
		t.Fatal("cancel delays draft restoration or focus")
	}
	ui.Advance(60 * time.Millisecond)
	middle, ok := ui.Find("Review the rotation code")
	midZone, _ := ui.Find("待发送队列")
	ui.Advance(500 * time.Millisecond)
	end, _ := ui.Find("Review the rotation code")
	endZone, _ := ui.Find("待发送队列")
	if !ok || math.Abs(float64(middle.Y-midZone.Y-offset)) > .5 || math.Abs(float64(end.Y-endZone.Y-offset)) > .5 {
		t.Fatal("cancel moves text instead of restoring queue opacity", before, middle, end)
	}
	if v.Model.Queue().Items[0].Input.Text != "Review the rotation code" {
		t.Fatal("cancel saved discarded edits")
	}
}

func TestEnqueuedRowPushesQueueSurfaceWithItsOwnHeight(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(300 * time.Millisecond)
	before, _ := ui.Find("待发送队列")
	ui.Click("消息输入")
	ui.Type("Push the queue upward")
	ui.Key(0, n.KeyEnter)
	var send app.Job
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "turn.enqueue" {
			send = job
		}
	}
	in := send.Input.(domain.Submission)
	q := v.Model.Queue()
	q.Revision++
	q.Items = append(q.Items, domain.QueueItem{ID: in.OperationID, Input: domain.Submission{OperationID: "dispatch-push", Text: in.Text}})
	v.Model.SetQueue(q)
	body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	send.Apply(v.Model, body, nil)
	ui.Frame()
	type sample struct{ queue, message n.Rect }
	var frames []sample
	for _, d := range []time.Duration{60 * time.Millisecond, 100 * time.Millisecond, 80 * time.Millisecond, 300 * time.Millisecond} {
		ui.Advance(d)
		zone, _ := ui.Find("待发送队列")
		message, ok := ui.Find("Push the queue upward")
		if !ok {
			t.Fatal("incoming row is missing")
		}
		frames = append(frames, sample{zone, message})
	}
	end := frames[len(frames)-1]
	for _, frame := range frames[:len(frames)-1] {
		if math.Abs(float64(frame.message.Y-frame.queue.Y-end.message.Y+end.queue.Y)) > .75 {
			t.Fatalf("queue grows separately from arriving row: before=%+v frames=%+v", before, frames)
		}
	}
	if !(frames[1].queue.H > before.H+2 && frames[1].queue.H < end.queue.H-2) {
		t.Fatal("queue finishes expanding before the row enters", before, frames)
	}
}

func TestCancelledEditFadesAtItsOriginalTextPosition(t *testing.T) {
	v, _ := nativeQueueFixture()
	q := v.Model.Queue()
	q.Items[0].Input.Text = "MMMMMMMMMMMMMMMM\nSecond line\nThird line\nFourth line"
	q.Revision++
	v.Model.SetQueue(q)
	ui := n.NewTester(func(c *n.Context) { theme := *c.Theme(); theme.Dark = true; c.SetTheme(&theme); v.View(c) }, 1200, 820)
	ui.Click("待发送 2")
	ui.Advance(300 * time.Millisecond)
	ui.Click("编辑待发消息 1")
	ui.Advance(500 * time.Millisecond)
	source, _ := ui.Find("消息输入")
	ink := func() int {
		img := ui.Image()
		total := 0
		for y := int(source.Y); y < int(source.Y)+20; y++ {
			for x := int(source.X) + 9; x < int(source.X)+155; x++ {
				p := img.RGBAAt(x, y)
				if p.R > 70 {
					total += int(p.R) - 70
				}
			}
		}
		return total
	}
	original := ink()
	if original < 1000 {
		t.Fatal("missing source text", original, source)
	}
	ui.Click("取消编辑")
	ui.Frame()
	first := ink()
	if first < original*9/10 {
		t.Fatal("cancelled text jumps away before fading", original, first, source)
	}
	ui.Advance(60 * time.Millisecond)
	middle := ink()
	ui.Advance(250 * time.Millisecond)
	end := ink()
	if !(middle > 100 && middle < original) || ui.HasText(q.Items[0].Input.Text) {
		t.Fatal("cancelled editor does not fade in place", original, first, middle, end)
	}
	if !ui.Focused("消息输入") || v.Model.Drafts["session:s"] != "" {
		t.Fatal("cancelled editor did not restore normal input immediately")
	}
}

func enqueueNativeDraft(t *testing.T, v *View, ui *n.Tester, text string) {
	t.Helper()
	ui.Click("消息输入")
	ui.Type(text)
	ui.Key(0, n.KeyEnter)
	var send app.Job
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "turn.enqueue" {
			send = job
		}
	}
	in, ok := send.Input.(domain.Submission)
	if !ok {
		t.Fatal("missing enqueue request")
	}
	q := v.Model.Queue()
	q.Revision++
	q.Items = append(q.Items, domain.QueueItem{ID: in.OperationID, Input: domain.Submission{OperationID: "dispatch-" + in.OperationID, Text: in.Text}})
	v.Model.SetQueue(q)
	body, _ := json.Marshal(domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"})
	send.Apply(v.Model, body, nil)
	ui.Frame()
}

func TestQueueDisclosureRemainsSmoothDuringArrival(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(300 * time.Millisecond)
	enqueueNativeDraft(t, v, ui, "New pending instruction")
	ui.Advance(60 * time.Millisecond)
	before, _ := ui.Find("待发送队列")
	ui.Click("待发送 3")
	ui.Frame()
	first, _ := ui.Find("待发送队列")
	if math.Abs(float64(first.H-before.H)) > 1 {
		t.Fatal("collapse jumps while a queued row arrives", before, first)
	}
	ui.Advance(60 * time.Millisecond)
	middle, _ := ui.Find("待发送队列")
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("待发送队列")
	if !(first.H > middle.H+2 && middle.H > end.H+2) {
		t.Fatal("disclosure does not animate independently of queue growth", first, middle, end)
	}
}

func TestFullQueueScrollsByTheArrivingRowsVisibleHeight(t *testing.T) {
	v, ui := nativeQueueFixture()
	q := v.Model.Queue()
	for i := 3; i <= 6; i++ {
		q.Items = append(q.Items, domain.QueueItem{ID: fmt.Sprint(i), Input: domain.Submission{Text: fmt.Sprintf("Queued row %d", i)}})
	}
	q.Revision++
	v.Model.SetQueue(q)
	ui.Frame()
	ui.Click("待发送 6")
	ui.Advance(300 * time.Millisecond)
	zone, _ := ui.Find("待发送队列")
	ui.Scroll(zone.X+zone.W/2, zone.Y+60, 0, 1000)
	ui.Advance(300 * time.Millisecond)
	before, ok := ui.Find("Queued row 6")
	if !ok {
		t.Fatal("last existing row is not visible")
	}
	enqueueNativeDraft(t, v, ui, "Seventh queued row")
	first, _ := ui.Find("Queued row 6")
	if math.Abs(float64(first.Y-before.Y)) > 1 {
		firstZone, _ := ui.Find("待发送队列")
		t.Fatal("full queue scroll jumps at enqueue acceptance", before, first, zone, firstZone, v.composerNaturalHeight, v.composerInputHeight)
	}
	ui.Advance(160 * time.Millisecond)
	middle, _ := ui.Find("Queued row 6")
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("Queued row 6")
	if !(middle.Y < before.Y-2 && middle.Y > end.Y+2) || math.Abs(float64(before.Y-end.Y-32)) > 1 {
		t.Fatal("full queue scroll is detached from new row growth", before, middle, end)
	}
}

func TestQueueArrivalKeepsComposerHeightForSingleLineDraft(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Advance(400 * time.Millisecond)
	before, _ := ui.Find("消息编辑器")
	enqueueNativeDraft(t, v, ui, "Single line")
	first, _ := ui.Find("消息编辑器")
	ui.Advance(400 * time.Millisecond)
	end, _ := ui.Find("消息编辑器")
	if math.Abs(float64(first.H-before.H)) > .5 || math.Abs(float64(end.H-before.H)) > .5 {
		t.Fatal("composer loses its border height when queue animation starts", before, first, end)
	}
}

func TestOutgoingQueueEditorCannotResizeRestoredDraft(t *testing.T) {
	v, ui := nativeQueueFixture()
	q := v.Model.Queue()
	q.Revision++
	q.Items[0].Input.Text = "First line\nSecond line\nThird line\nFourth line"
	v.Model.SetQueue(q)
	ui.Click("待发送 2")
	ui.Advance(300 * time.Millisecond)
	ui.Click("编辑待发消息 1")
	ui.Advance(500 * time.Millisecond)
	ui.Click("取消编辑")
	ui.Frame()
	input, _ := ui.Find("消息输入")
	if math.Abs(float64(v.composerInputHeight-input.H)) > .5 {
		t.Fatal("fading editor overwrites restored input measurements", v.composerInputHeight, input)
	}
}

func TestQueuedMessagesScrollAfterThreeVisibleRows(t *testing.T) {
	v, ui := nativeQueueFixture()
	q := v.Model.Queue()
	q.Items = append(q.Items, domain.QueueItem{ID: "three", Input: domain.Submission{Text: "Third queued row"}}, domain.QueueItem{ID: "four", Input: domain.Submission{Text: "Fourth queued row"}})
	q.Revision++
	v.Model.SetQueue(q)
	ui.Frame()
	ui.Click("待发送 4")
	ui.Advance(300 * time.Millisecond)
	viewport, _ := ui.Find("待发送消息列表")
	third, _ := ui.Find("Third queued row")
	fourth, _ := ui.Find("Fourth queued row")
	if viewport.H > 100.5 || third.H < 19 || fourth.H > 0 {
		t.Fatal("queue exposes more than three rows before scrolling", viewport, third, fourth)
	}
	ui.Scroll(viewport.X+100, viewport.Y+40, 0, 200)
	ui.Advance(300 * time.Millisecond)
	fourth, _ = ui.Find("Fourth queued row")
	if fourth.H < 19 {
		t.Fatal("fourth queued row is not reachable by internal scrolling", fourth)
	}
}

func TestRapidEnqueueRowsDoNotOverlapWhileQueueGrows(t *testing.T) {
	v, ui := nativeQueueFixture()
	q := v.Model.Queue()
	q.Items = q.Items[:1]
	q.Revision++
	v.Model.SetQueue(q)
	ui.Frame()
	ui.Click("待发送 1")
	ui.Advance(300 * time.Millisecond)
	enqueueNativeDraft(t, v, ui, "First arriving message")
	ui.Advance(40 * time.Millisecond)
	enqueueNativeDraft(t, v, ui, "Second arriving message")
	for i := 0; i < 20; i++ {
		ui.Advance(16 * time.Millisecond)
		first, _ := ui.Find("First arriving message")
		second, _ := ui.Find("Second arriving message")
		if first.H > 0 && second.H > 0 && second.Y < first.Y+first.H+1 {
			t.Fatal("successive queue arrivals overlap instead of pushing each other", first, second)
		}
	}
}

func TestCollapsedQueueChevronAlignsToChineseInk(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, _ := nativeQueueFixture()
		ui := n.NewTester(func(c *n.Context) { theme := *c.Theme(); theme.Dark = dark; c.SetTheme(&theme); v.View(c) }, 1200, 820)
		ui.Advance(time.Second)
		heading, _ := ui.Find("待发送")
		trigger, _ := ui.Find("待发送 2")
		inkCenter := func(r n.Rect) float32 {
			im := ui.Image()
			bg := im.RGBAAt(int(r.X+r.W/2), int(r.Y+1))
			top, bottom := im.Bounds().Max.Y, -1
			for y := int(r.Y); y < int(r.Y+r.H); y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					p := im.RGBAAt(x, y)
					d := int(p.R) - int(bg.R)
					if d > 30 || d < -30 {
						top = min(top, y)
						bottom = max(bottom, y)
					}
				}
			}
			if bottom < 0 {
				t.Fatal("missing queue header ink", r)
			}
			return float32(top+bottom) / 2
		}
		icon := n.Rect{X: trigger.X, Y: trigger.Y, W: 16, H: trigger.H}
		if delta := inkCenter(icon) - inkCenter(heading); math.Abs(float64(delta)) > .5 {
			t.Fatal("queue chevron does not align to Chinese ink", dark, delta, icon, heading)
		}
	}
}

func TestContinueQueueMatchesSecondaryCaptionFont(t *testing.T) {
	v, _ := nativeQueueFixture()
	q := v.Model.Queue()
	q.Paused = true
	v.Model.SetQueue(q)
	var captionWidth float32
	ui := n.NewTester(func(c *n.Context) {
		captionWidth, _ = c.MeasureText(0, n.Span{Text: "继续队列", Font: "system-ui", Size: 11, Weight: 400})
		v.View(c)
	}, 1200, 820)
	ui.Advance(time.Second)
	button, ok := ui.Find("继续队列")
	if !ok {
		t.Fatal("missing continue queue action")
	}
	if math.Abs(float64(button.W-captionWidth)) > .1 || button.H != 18 {
		t.Fatal("continue queue does not match secondary caption typography", button, captionWidth)
	}
}

func TestQueueHeaderUsesOneOpticalCenter(t *testing.T) {
	v, _ := nativeQueueFixture()
	q := v.Model.Queue()
	q.Paused = true
	v.Model.SetQueue(q)
	ui := n.NewTester(v.View, 1200, 820)
	ui.Advance(time.Second)
	header, _ := ui.Find("待发送 2")
	center := header.Y + header.H/2
	for _, entry := range []struct {
		label  string
		size   float32
		weight int
	}{{"待发送", 12, 500}, {"已暂停", 11, 400}, {"继续队列", 11, 400}} {
		r, ok := ui.Find(entry.label)
		if !ok {
			t.Fatal("missing header caption", entry.label)
		}
		font := n.Font{Family: "system-ui", Size: entry.size, Weight: entry.weight}
		m := inlineFontMetrics(entry.label, font)
		baseline := r.Y + (r.H-m.ascent-m.descent)/2 + m.ascent
		actual := baseline - m.center
		if math.Abs(float64(actual-center)) > .5 {
			t.Error("queue caption does not share header optical center", entry.label, actual-center)
		}
	}
}

func TestSendGlyphStaysAnUpArrowAtHomeInQueuesAndWhileEditing(t *testing.T) {
	var expected []uint32
	for _, mode := range []string{"idle", "home", "running", "queued", "editing"} {
		t.Run(mode, func(t *testing.T) {
			v, _ := nativeQueueFixture()
			m := v.Model
			d := m.Details["s"]
			m.Snapshot.Sessions[0].Status = "completed"
			d.Session.Status = "completed"
			d.Turns = nil
			originalQueue := d.Queue
			d.Queue = domain.QueueState{SessionID: "s"}
			m.Details["s"] = d
			m.Drafts["session:s"] = "next message"
			label := "发送"
			switch mode {
			case "home":
				m.SessionID = ""
				m.Drafts["home"] = "next message"
			case "running":
				m.Snapshot.Sessions[0].Status = "running"
				label = "加入队列"
			case "queued", "editing":
				m.SetQueue(originalQueue)
				label = "加入队列"
				if mode == "editing" {
					if err := m.EditQueued("one"); err != nil {
						t.Fatal(err)
					}
					label = "保存编辑"
				}
			}
			ui := n.NewTester(v.View, 1200, 820)
			ui.Advance(time.Second)
			r, ok := ui.Find(label)
			if !ok {
				t.Fatal("missing primary send action", mode)
			}
			im := ui.Image()
			var pixels []uint32
			for y := int(r.Y + 4); y < int(r.Y+24); y++ {
				for x := int(r.X + 4); x < int(r.X+24); x++ {
					p := im.RGBAAt(x, y)
					pixels = append(pixels, uint32(p.R)<<24|uint32(p.G)<<16|uint32(p.B)<<8|uint32(p.A))
				}
			}
			if expected == nil {
				expected = pixels
				return
			}
			for i, pixel := range pixels {
				if pixel != expected[i] {
					t.Fatal("primary send glyph changed from the up arrow", mode)
				}
			}
		})
	}
}

func TestSendButtonRestoresOriginalFaceAndHitTarget(t *testing.T) {
	for _, dark := range []bool{false, true} {
		v, _ := nativeQueueFixture()
		v.Model.Drafts["session:s"] = "next message"
		ui := n.NewTester(func(c *n.Context) { theme := *c.Theme(); theme.Dark = dark; c.SetTheme(&theme); v.View(c) }, 1200, 820)
		ui.Advance(time.Second)
		r, ok := ui.Find("加入队列")
		if !ok || r.W != 28 || r.H != 28 {
			t.Fatal("primary action lost its hit target", r)
		}
		for _, hover := range []bool{false, true} {
			if hover {
				ui.Move(r.X+r.W/2, r.Y+r.H/2)
				ui.Advance(200 * time.Millisecond)
			}
			im := ui.Image()
			y := int(r.Y + 14)
			outside := im.RGBAAt(int(r.X-2), y)
			if im.RGBAAt(int(r.X+1), y) == outside || im.RGBAAt(int(r.X+26), y) == outside {
				t.Fatal("send face has not restored its original diameter", dark, hover)
			}
			if im.RGBAAt(int(r.X+3), y) == outside {
				t.Fatal("send face is missing", dark, hover)
			}
		}
	}
}
