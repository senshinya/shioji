package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"math"
	"shioji/internal/app"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func historyFixture() (*View, *n.Tester, domain.Item) {
	m := layoutModel()
	m.WorkerMinor = 9
	m.ConnectionEpoch = "history-epoch"
	m.Drafts["session:s"] = "ordinary unsent draft"
	d := m.Details["s"]
	d.Session.ThreadID = "thread"
	d.Session.Revision = 4
	d.Session.Config = m.Config()
	m.Snapshot.Sessions[0] = d.Session
	item := domain.Item{ID: "user", TurnID: "turn", Kind: "user", Text: "old question", Status: "sent", Order: 1, Final: true}
	for i := range d.Items {
		d.Items[i].TurnID = "turn"
	}
	d.Items = append([]domain.Item{item}, d.Items...)
	d.Turns = []domain.Turn{{ID: "turn", Status: "completed"}}
	m.Details["s"] = d
	v := New(m, app.NewController(""), "")
	ui := n.NewTester(v.View, 1200, 820)
	return v, ui, item
}
func TestHistoryEditorKeepsOrdinaryDraftAndRestoresOnCancel(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	if !ui.HasText("编辑历史消息") || v.Model.DraftKey() != "session:s" {
		t.Fatal("history editor absent")
	}
	ui.Click("编辑历史消息")
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("revised question")
	ui.Frame()
	if v.Model.Drafts["session:s"] != "ordinary unsent draft" {
		t.Fatal("edit replaced ordinary draft")
	}

	if err := ui.Click("取消历史编辑"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(400 * time.Millisecond)
	if v.Model.DraftKey() != "session:s" || v.Model.Drafts["session:s"] != "ordinary unsent draft" {
		t.Fatal("cancel lost ordinary draft", v.Model.DraftKey(), v.Model.Drafts, v.Model.HistoryEdits)
	}
}

func TestUserMessageActionsSitBelowTheBubbleAndCopyItsText(t *testing.T) {
	v, ui, _ := historyFixture()
	text, _ := ui.Find("old question")
	edit, editOK := ui.Find("修改并重新开始")
	copy, copyOK := ui.Find("复制消息")
	stamp, stampOK := ui.Find("发送时间：—")
	if !editOK || !copyOK || !stampOK || edit.Y < text.Y+text.H || copy.Y != edit.Y || stamp.Y < edit.Y || copy.X <= edit.X || stamp.X <= copy.X {
		t.Fatal("user actions are not together below the bubble", text, edit, copy, stamp)
	}
	if err := ui.Click("复制消息"); err != nil {
		t.Fatal(err)
	}
	if ui.Clipboard() != "old question" || !ui.HasText("已复制") || v.Model.Drafts["session:s"] != "ordinary unsent draft" {
		t.Fatal("copy changed the draft or copied the wrong message")
	}
}

func TestHistoryConfirmationReturnsToBubbleWhileSuffixIsShattering(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(time.Second)
	editor, _ := ui.Find("历史消息编辑器")
	v.Model.Drafts["history:s:user"] = "revised"
	ui.Frame()
	v.restartHistory()
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	message, ok := ui.Find("用户消息")
	bubble, bubbleOK := ui.Find("用户消息气泡")
	if ui.HasText("编辑历史消息") || !ui.HasText("revised") || !ui.HasText("Aligned reply") || !ok || message.H >= editor.H || !bubbleOK || bubble.W >= editor.W || bubble.W <= 100 {
		t.Fatal("editor did not close concurrently with the suffix exit", editor, message)
	}
	if v.Model.Details["s"].Items[0].Text != "old question" || v.Model.Drafts["history:s:user"] != "revised" || v.Model.Drafts["session:s"] != "ordinary unsent draft" {
		t.Fatal("local animation changed authoritative history or drafts")
	}
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.restart" {
			t.Fatal("new execution began before the suffix exit finished")
		}
	}
}

func TestHistoryPreviewKeepsUncertainResultActionAccessible(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	v.restartHistory()
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "session.restart" {
			job.Apply(v.Model, json.RawMessage(`{`), nil)
			ui.Frame()
			ui.Advance(300 * time.Millisecond)
			if !ui.HasText("核对结果") || ui.HasText("编辑历史消息") {
				t.Fatal("preview hid the uncertain operation's recovery action")
			}
			return
		}
	}
	t.Fatal("restart job missing")
}

func TestHistoryTimestampSurvivesPreviewAndRollsOnConfirmedReplacement(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprint("reduced=", reduced), func(t *testing.T) {
			v, ui, item := historyFixture()
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			oldTime := time.Date(2026, 11, 7, 11, 22, 0, 0, time.Local)
			item.SentAt = &oldTime
			d := v.Model.Details["s"]
			d.Items[0] = item
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			before, _ := ui.Find("发送时间：11:22")
			editBefore, _ := ui.Find("修改并重新开始")
			v.beginHistoryEdit(item)
			ui.Frame()
			ui.Advance(time.Second)
			v.Model.Drafts["history:s:user"] = "revised"
			v.restartHistory()
			ui.Frame()
			ui.Advance(100 * time.Millisecond)
			stamp, ok := ui.Find("发送时间：11:22")
			edit, editOK := ui.Find("修改并重新开始")
			if !ok || !editOK || stamp.X != before.X || edit.X != editBefore.X {
				t.Fatal("local preview lost its confirmed time or action slots", before, stamp, editBefore, edit)
			}
			ui.Advance(time.Second)
			id := v.Model.HistoryEdits["s"].OperationID
			keep := int64(-1)
			v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
			ui.Frame()
			if !ui.HasText("发送时间：11:22") {
				t.Fatal("reset bridge dropped the known time")
			}
			newTime := oldTime.Add(time.Minute)
			replacement := domain.Item{ID: "input:" + id, Kind: "user", TurnID: "new-turn", Status: "sent", Text: "revised", SentAt: &newTime, Final: true}
			v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &replacement, Operation: &domain.Operation{ID: id, SessionID: "s", State: "confirmed"}})
			ui.Frame()
			ui.Advance(80 * time.Millisecond)
			current, ok := ui.Find("发送时间：11:23")
			if !ok || current.X != before.X || current.W != before.W {
				t.Fatal("confirmed timestamp changed layout", before, current)
			}
			pixels := func() []byte {
				im := ui.Image()
				var p []byte
				for y := int(current.Y); y < int(current.Y+current.H); y++ {
					for x := int(current.X); x < int(current.X+current.W); x++ {
						c := im.RGBAAt(x, y)
						p = append(p, c.R, c.G, c.B, c.A)
					}
				}
				return p
			}
			middle := pixels()
			ui.Advance(400 * time.Millisecond)
			if bytes.Equal(middle, pixels()) != reduced {
				t.Fatal("timestamp did not roll, or ignored Reduce Motion")
			}
		})
	}
}
func TestHistoryEditorSendsOnlyPinnedRestartAndRetriesByQuery(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	v.Model.Drafts[v.Model.HistoryDraftKey()] = "revised"
	ui.Frame()
	v.restartHistory()
	ui.Advance(time.Second)
	var change domain.HistoryChange
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.submit" {
			t.Fatal("history edit sent an ordinary turn")
		}
		if j.Method == "session.restart" {
			change = j.Input.(domain.HistoryChange)
		}
	}
	if change.ItemID != "user" || change.Revision != 4 || change.Text != "revised" {
		t.Fatal("restart not pinned", change)
	}
	// A retry with uncertain result must only query the stored history action.
	v.historyBusy["s"] = false
	v.restartHistory()
	found := false
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.restart" {
			t.Fatal("uncertain restart resent")
		}
		found = found || j.Method == "session.history-operation"
	}
	if !found {
		t.Fatal("retry didn't query history operation")
	}
}

func TestForkRetryQueriesPersistedActionInsteadOfCreatingAnotherBranch(t *testing.T) {
	v, ui, _ := historyFixture()
	source := v.Model.Details["s"].Session
	v.forkSession(source, "turn")
	ui.Advance(time.Second)
	var first app.Job
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.fork" {
			first = j
		}
	}
	if first.Method == "" {
		t.Fatal("fork command missing")
	}
	first.Apply(v.Model, nil, errors.New("connection lost"))
	v.forkSession(source, "turn")
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.fork" {
			t.Fatal("fork retry created another branch")
		}
		if j.Method == "session.history-operation" {
			return
		}
	}
	t.Fatal("fork result wasn't queried")
}

func TestForkPendingRequestDoesNotInsertARecoveryNoticeOrMoveComposer(t *testing.T) {
	v, ui, _ := historyFixture()
	ui.Advance(time.Second)
	before, _ := ui.Find("消息编辑器")
	v.forkSession(v.Model.Details["s"].Session, "turn")
	ui.Advance(300 * time.Millisecond)
	during, _ := ui.Find("消息编辑器")
	if ui.HasText("会话操作结果待核对") || math.Abs(float64(during.Y-before.Y)) > 1 || during.H != before.H {
		t.Fatal("normal fork request inserted recovery UI and moved the composer", before, during)
	}
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method != "session.fork" {
			continue
		}
		job.Apply(v.Model, nil, errors.New("connection lost"))
		ui.Frame()
		ui.Advance(300 * time.Millisecond)
		if !ui.HasText("核对结果") || !ui.HasText("取消本次操作") {
			t.Fatal("uncertain fork lost its recovery actions")
		}
		return
	}
	t.Fatal("fork request missing")
}

func TestSavedForkCanExplicitlyFinishWithSameOperation(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	v.restartHistory()
	ui.Advance(time.Second)
	var sent app.Job
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.restart" {
			sent = j
		}
	}
	in := sent.Input.(domain.HistoryChange)
	pending := domain.HistoryOperation{ID: in.OperationID, Input: in, Source: v.Model.Details["s"].Session, Session: v.Model.Details["s"].Session, State: "forked", ThreadID: "new"}
	data, _ := json.Marshal(pending)
	sent.Apply(v.Model, data, nil)
	v.restartHistory()
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.restart" {
			if j.Input.(domain.HistoryChange) != in {
				t.Fatal("finish changed original request")
			}
			return
		}
	}
	t.Fatal("saved fork could not finish")
}
func TestPendingHistoryEditorIsReadOnly(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	v.Model.Drafts[v.Model.HistoryDraftKey()] = "submitted"
	ui.Frame()
	v.restartHistory()
	ui.Advance(time.Second)
	key := v.Model.HistoryDraftKey()
	ui.Click("编辑历史消息")
	ui.Type("unexpected")
	ui.Frame()
	if v.Model.Drafts[key] != "submitted" {
		t.Fatal("pending input was edited", v.Model.Drafts[key])
	}
}

func TestHistoryEditorHeightAnimatesThroughEntryAndCancellation(t *testing.T) {
	v, ui, item := historyFixture()
	before, _ := ui.Find("用户消息")
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(80 * time.Millisecond)
	middle, _ := ui.Find("用户消息")
	ui.Advance(500 * time.Millisecond)
	expanded, _ := ui.Find("用户消息")
	if middle.H <= before.H || middle.H >= expanded.H {
		t.Fatal("editor entry height jumped", before, middle, expanded)
	}
	ui.Click("取消历史编辑")
	ui.Advance(80 * time.Millisecond)
	closing, _ := ui.Find("用户消息")
	ui.Advance(500 * time.Millisecond)
	after, _ := ui.Find("用户消息")
	if closing.H >= expanded.H || closing.H <= after.H {
		t.Fatal("editor exit height jumped", expanded, closing, after)
	}
}

func TestHistoryInlineEditorLeavesOrdinaryComposerInPlace(t *testing.T) {
	v, ui, item := historyFixture()
	before, _ := ui.Find("消息编辑器")
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	editor, ok := ui.Find("编辑历史消息")
	after, _ := ui.Find("消息编辑器")
	if !ok || editor.Y >= after.Y || before != after {
		t.Fatal("history edit must be inline without changing the bottom composer", editor, before, after)
	}
	ui.Click("编辑历史消息")
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("inline revision")
	ui.Frame()
	if v.Model.Drafts["session:s"] != "ordinary unsent draft" || v.Model.Drafts["history:s:user"] != "inline revision" {
		t.Fatal("inline editing shares the ordinary draft", v.Model.Drafts)
	}
}

func TestHistoryRestartWaitsForSuffixExitBeforeDispatch(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	v.Model.Drafts["history:s:user"] = "revised"
	v.restartHistory()
	ui.Frame()
	ui.Advance(200 * time.Millisecond)
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.restart" {
			t.Fatal("restart began while the previous messages were still exiting")
		}
	}
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.restart" {
			if in := j.Input.(domain.HistoryChange); in.Text != "revised" {
				t.Fatal("restart did not use the inline draft", in)
			}
			return
		}
	}
	t.Fatal("restart did not begin after the exit")
}

func TestHistoryExitRestoresEditorWhenSourceChangesBeforeDispatch(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	v.Model.Drafts["history:s:user"] = "revised"
	v.restartHistory()
	d := v.Model.Details["s"]
	d.Session.Revision++
	v.Model.Details["s"] = d
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.restart" {
			t.Fatal("restart ignored a source change during its exit")
		}
	}
	if v.historyExit != nil || v.Model.HistoryEdits["s"].OperationID != "" || v.Model.Drafts["history:s:user"] != "revised" || !ui.HasText("Aligned reply") {
		t.Fatal("aborted exit did not restore the original conversation and edit")
	}
}

func TestReducedMotionHistoryRestartDoesNotWaitForShatter(t *testing.T) {
	v, ui, item := historyFixture()
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	ui.Frame()
	v.beginHistoryEdit(item)
	v.restartHistory()
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.restart" {
			return
		}
	}
	t.Fatal("Reduce Motion waited for the decorative exit")
}

func TestForkIsBesideCopyBeforeReplyMetadata(t *testing.T) {
	_, ui, _ := historyFixture()
	copy, copyOK := ui.Find("复制回复")
	fork, forkOK := ui.Find("从这里分叉")
	stamp, stampOK := ui.Find("结束时间：—")
	if !copyOK || !forkOK || !stampOK || fork.X <= copy.X || stamp.X <= fork.X || fork.Y != copy.Y {
		t.Fatal("fork is detached from the reply actions", copy, fork, stamp)
	}
}

func TestSecondaryHistoryRestartDispatchesAfterExitEvenWhenPaneCloses(t *testing.T) {
	v, ui, item := historyFixture()
	d := v.Model.Details["s"]
	d.Session.ID, d.Session.Title = "other", "Other history"
	v.Model.Details["other"] = d
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, d.Session)
	v.openParallel("other")
	ui.Frame()
	pane := v.secondary
	pane.beginHistoryEdit(item)
	pane.restartHistory()
	ui.Frame()
	// Closing the pane must not strand an already confirmed local action.
	v.closeParallel()
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.restart" && j.Input.(domain.HistoryChange).SessionID == "other" {
			return
		}
	}
	t.Fatal("secondary history restart was stranded after its exit")
}

func TestExternalHistoryReplacementKeepsAnAccessibleEditDraft(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	v.Model.Drafts["history:s:user"] = "unsent local revision"
	keep := int64(-1)
	if err := v.Model.ApplyEvent(domain.Event{Sequence: v.Model.Sequence + 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep}); err != nil {
		t.Fatal(err)
	}
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("复制修改稿"); err != nil {
		t.Fatal("external replacement stranded the local edit", err)
	}
	if ui.Clipboard() != "unsent local revision" || v.Model.Drafts["session:s"] != "ordinary unsent draft" {
		t.Fatal("recovered edit overwrote the ordinary draft")
	}
	if err := ui.Click("放弃历史编辑"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if len(v.Model.HistoryEdits) != 0 {
		t.Fatal("detached edit could not be dismissed")
	}
}

func TestInlineEditorStaysAboveTheDockInNarrowWindow(t *testing.T) {
	v, _, item := historyFixture()
	d := v.Model.Details["s"]
	d.Items = append([]domain.Item{{ID: "earlier-user", TurnID: "earlier", Kind: "user", Status: "sent", Text: "Earlier prompt"}, {ID: "earlier-reply", TurnID: "earlier", Kind: "assistant", Text: "Earlier reply", Final: true}}, d.Items...)
	d.Turns = append([]domain.Turn{{ID: "earlier", Status: "completed", Order: -1}}, d.Turns...)
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 850, 650)
	v.lists["s"].KeepRow(4, 450)
	ui.Frame()
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(time.Second)
	editor, ok := ui.Find("历史消息编辑器")
	dock, _ := ui.Find("消息编辑器")
	if !ok || editor.Y+editor.H > dock.Y-12 || ui.HasText("回到最新") {
		t.Fatal("inline edit controls overlap the dock or latest button", editor, dock)
	}
}

func TestHistoryReplacementPreservesTheEditedMessagePosition(t *testing.T) {
	v, ui, item := historyFixture()
	d := v.Model.Details["s"]
	d.Items = append([]domain.Item{{ID: "prefix", TurnID: "prefix-turn", Kind: "assistant", Text: strings.Repeat("Earlier context\n\n", 12), Final: true}}, d.Items...)
	d.Turns = append([]domain.Turn{{ID: "prefix-turn", Order: -1, Status: "completed"}}, d.Turns...)
	v.Model.Details["s"] = d
	ui.Frame()
	v.lists["s"].KeepRow(3, 260)
	ui.Frame()
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(time.Second)
	before, _ := ui.Find("历史消息编辑器")
	v.Model.Drafts["history:s:user"] = "revised prompt"
	v.restartHistory()
	ui.Advance(time.Second)
	id := v.Model.HistoryEdits["s"].OperationID
	keep := int64(-1)
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
	ui.Frame()
	bridge, ok := ui.Find("用户消息气泡")
	if !ok || !ui.HasText("revised prompt") || ui.HasText("编辑历史消息") || math.Abs(float64(bridge.Y-before.Y)) > 1 {
		t.Fatal("reset dropped the inline anchor before the replacement arrived", before, bridge)
	}
	newItem := domain.Item{ID: "input:" + id, Kind: "user", Text: "revised prompt", Status: "sent", TurnID: "new-turn", Order: 3, Final: true}
	v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &newItem, Operation: &domain.Operation{ID: id, SessionID: "s", State: "confirmed"}})
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	after, ok := ui.Find("revised prompt")
	if !ok || math.Abs(float64(after.Y-before.Y-12)) > 1 {
		t.Fatal("replacement jumped after the old row identity disappeared", before, after)
	}
}

func TestForkFadesSuffixAndKeepsTheSelectedReplyPosition(t *testing.T) {
	v, ui, _ := historyFixture()
	d := v.Model.Details["s"]
	d.Items = append(d.Items, domain.Item{ID: "later", TurnID: "later-turn", Kind: "assistant", Text: strings.Repeat("Later context\n\n", 14), Final: true})
	d.Turns = append(d.Turns, domain.Turn{ID: "later-turn", Order: 1, Status: "completed"})
	v.Model.Details["s"] = d
	ui.Frame()
	v.lists["s"].KeepRow(3, 270)
	ui.Frame()
	before, _ := ui.Find("回复信息")
	v.forkSession(d.Session, "turn")
	ui.Frame()
	ui.Advance(80 * time.Millisecond)
	var sent app.Job
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.fork" {
			t.Fatal("fork navigation began before the following messages faded")
		}
	}
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.fork" {
			sent = j
		}
	}
	if sent.Method == "" {
		t.Fatal("fork did not dispatch after fading")
	}
	in := sent.Input.(domain.HistoryChange)
	branch := d.Session
	branch.ID, branch.ThreadID = "branch", "branch-thread"
	data, _ := json.Marshal(domain.HistoryOperation{ID: in.OperationID, Input: in, Source: d.Session, Session: branch, State: "complete", KeepOrder: 0})
	sent.Apply(v.Model, data, nil)
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	after, ok := ui.Find("回复信息")
	if !ok || v.Model.SessionID != "branch" || math.Abs(float64(after.Y-before.Y)) > 1 {
		t.Fatal("fork lost the selected reply or jumped while hydrating", before, after, v.Model.SessionID)
	}
	// The controller's first full history arrives later than the receipt and
	// repeats its opening fold barrier. That must not release the new anchor.
	// Ordinary watch events may already have advanced the detail sequence.
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "session", SessionID: "branch", Session: &branch})
	ui.Frame()
	ui.Advance(150 * time.Millisecond)
	ui.Frame()
	loaded := v.Model.Details["branch"]
	loaded.Sequence = 12
	v.Model.Details["branch"] = loaded
	v.Model.FoldToolHistory["branch"] = true
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	hydrated, ok := ui.Find("回复信息")
	if !ok || math.Abs(float64(hydrated.Y-before.Y)) > 1 {
		t.Fatal("late history hydration released the fork reading anchor", before, hydrated)
	}
}

func TestForkFadeIncludesTheFollowingTurnFooter(t *testing.T) {
	v, _, _ := historyFixture()
	d := v.Model.Details["s"]
	d.Turns = append(d.Turns, domain.Turn{ID: "later-turn", Order: 1, Status: "completed"})
	v.Model.Details["s"] = d
	v.forkSession(d.Session, "turn")
	if !v.historySuffixRow(transcriptRow{footer: "later-turn"}) || v.historySuffixRow(transcriptRow{footer: "turn"}) {
		t.Fatal("fork fade omitted the following reply metadata or faded the selected reply")
	}
}

func TestForkUsesTheConfirmedSourcePrefixWhenAnotherClientRestartsIt(t *testing.T) {
	v, ui, _ := historyFixture()
	source := v.Model.Details["s"].Session
	v.forkSession(source, "turn")
	ui.Advance(time.Second)
	var sent app.Job
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.fork" {
			sent = j
		}
	}
	in := sent.Input.(domain.HistoryChange)
	changed := source
	changed.ThreadID, changed.Revision = "other-thread", 5
	keep := int64(-1)
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", Session: &changed, HistoryTurnOrder: &keep})
	newInput := domain.Item{ID: "other-input", TurnID: "other-turn", Kind: "user", Text: "another client's replacement"}
	v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &newInput})
	branch := source
	branch.ID, branch.ThreadID = "branch", "fork-thread"
	data, _ := json.Marshal(domain.HistoryOperation{ID: in.OperationID, Input: in, Source: source, Session: branch, State: "complete", KeepOrder: 0})
	sent.Apply(v.Model, data, nil)
	items := v.Model.Details["branch"].Items
	if len(items) != 2 || items[1].Text != "Aligned reply" {
		t.Fatal("fork mounted a different source thread's content", items)
	}
}

func TestSidebarForkDoesNotRequireCachedHistory(t *testing.T) {
	v, ui, _ := historyFixture()
	source := v.Model.Details["s"].Session
	source.ID = "unopened"
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, source)
	v.forkSession(source, "")
	ui.Advance(time.Second)
	for len(v.Controller.Jobs) > 0 {
		j := <-v.Controller.Jobs
		if j.Method == "session.fork" && j.Input.(domain.HistoryChange).SessionID == "unopened" {
			return
		}
	}
	t.Fatal("sidebar fork was blocked by an absent UI detail cache", v.Model.Error)
}

func TestConfirmedSecondaryForkSurvivesBeforeTheDeviceWatchEvent(t *testing.T) {
	v, ui, _ := historyFixture()
	d := v.Model.Details["s"]
	d.Session.ID, d.Session.Title = "other", "Other session"
	v.Model.Details["other"] = d
	v.Model.Snapshot.Sessions = append(v.Model.Snapshot.Sessions, d.Session)
	v.Model.DeviceStates["local"] = &app.DeviceState{Snapshot: v.Model.Snapshot, Connected: true, ConnectionEpoch: v.Model.ConnectionEpoch, WorkerMinor: 9, Readiness: v.Model.Readiness}
	v.openParallel("other")
	ui.Frame()
	v.secondary.forkSession(d.Session, "turn")
	ui.Advance(time.Second)
	var sent app.Job
	for len(v.Controller.Jobs) > 0 {
		if j := <-v.Controller.Jobs; j.Method == "session.fork" {
			sent = j
		}
	}
	in := sent.Input.(domain.HistoryChange)
	branch := d.Session
	branch.ID, branch.ThreadID, branch.ForkedFrom = "branch", "branch-thread", "other"
	data, _ := json.Marshal(domain.HistoryOperation{ID: in.OperationID, Input: in, Source: d.Session, Session: branch, State: "complete", KeepOrder: 0})
	sent.Apply(v.Model, data, nil)
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if v.Model.SecondarySessionID != "branch" || v.secondary.Model.SessionID != "branch" {
		t.Fatal("device aggregation dropped the confirmed branch before watch caught up", v.Model.SecondarySessionID, v.secondary.Model.SessionID)
	}
}
