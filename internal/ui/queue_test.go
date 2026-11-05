package ui

import (
	"encoding/json"
	"errors"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
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
	d.Queue = domain.QueueState{SessionID: "s", Revision: 1, Items: []domain.QueueItem{{ID: "one", Input: domain.Submission{Text: "Review the rotation code"}}, {ID: "two", Input: domain.Submission{Text: "Add a small example"}}}}
	m.Details["s"] = d
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1200, 820)
	v.gitPending = map[string]bool{}
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
func TestRemovingQueuedItemMovesNextRowContinuously(t *testing.T) {
	v, ui := nativeQueueFixture()
	ui.Click("待发送 2")
	ui.Advance(200 * time.Millisecond)
	before, _ := ui.Find("Add a small example")
	q := v.Model.Queue()
	q.Revision++
	q.Items = q.Items[1:]
	v.Model.SetQueue(q)
	ui.Frame()
	ui.Advance(50 * time.Millisecond)
	middle, _ := ui.Find("Add a small example")
	ui.Advance(200 * time.Millisecond)
	end, _ := ui.Find("Add a small example")
	if math.Abs(float64(middle.Y-before.Y)) > 1 || math.Abs(float64(end.Y-before.Y)) > 1 {
		t.Fatalf("row jumps before=%+v middle=%+v end=%+v", before, middle, end)
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
	if math.Abs(float64(q.Y+q.H-1-composer.Y)) > .5 {
		t.Fatal("queue/composer gap", q, composer)
	}
}

func TestSendMenuStaysAlignedAboveTrigger(t *testing.T) {
	_, ui := nativeQueueFixture()
	ui.Click("消息输入")
	ui.Type("next")
	ui.Click("发送操作")
	ui.Advance(180 * time.Millisecond)
	trigger, _ := ui.Find("发送操作")
	panel, ok := ui.Find("发送选项")
	if !ok || math.Abs(float64(panel.X+panel.W-trigger.X-trigger.W)) > 1 || panel.Y+panel.H > trigger.Y-5 || panel.H > 94 {
		t.Fatal("send menu alignment/density", trigger, panel)
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
