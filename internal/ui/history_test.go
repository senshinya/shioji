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
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(400 * time.Millisecond)
	binding := v.composerDocuments[v.Model.HistoryDraftKey()]
	if binding == nil || binding.doc.Text().String() != item.Text || v.Model.Drafts[v.Model.HistoryDraftKey()] != item.Text {
		t.Fatal("reopening a canceled edit restores its discarded text", v.Model.Drafts)
	}
}

func TestHistoryEditorSpaceDoesNotPageTheConversation(t *testing.T) {
	for _, mods := range []n.Modifiers{0, n.Shift} {
		v, ui, item := historyFixture()
		d := v.Model.Details["s"]
		d.Items = append(d.Items, domain.Item{ID: "long-reply", TurnID: "turn", Kind: "assistant", Text: strings.Repeat("A retained paragraph below the edited message.\n\n", 45), Final: true})
		v.Model.Details["s"] = d
		v.beginHistoryEdit(item)
		v.lists["s"].KeepRow(1, 100)
		ui.Frame()
		ui.Advance(time.Second)
		if err := ui.Click("编辑历史消息"); err != nil {
			t.Fatal(err)
		}
		before, ok := ui.Find("编辑历史消息")
		if !ok || before.Y < 48 || before.Y > 500 {
			t.Fatal("editor is not visible before typing", before)
		}
		ui.Key(mods, n.KeySpace)
		ui.Type(" ")
		for frame := 0; frame < 30; frame++ {
			ui.Advance(time.Second / 60)
			after, ok := ui.Find("编辑历史消息")
			if !ok || math.Abs(float64(after.Y-before.Y)) > 1 {
				t.Fatalf("space pages the transcript while editing (mods=%v): %+v -> %+v", mods, before, after)
			}
		}
		if text := v.Model.Drafts[v.Model.HistoryDraftKey()]; strings.Count(text, " ") != strings.Count(item.Text, " ")+1 {
			t.Fatal("space is not inserted exactly once", text)
		}
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
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
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
			ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
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
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
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
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
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
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
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
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
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
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
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
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
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

func TestHistoryReplacementPreservesTheConstrainedMessagePosition(t *testing.T) {
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
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
	constrained, ok := ui.Find("用户消息气泡")
	if !ok || constrained.Y < before.Y {
		t.Fatal("collapsed suffix lost the edited message", before, constrained)
	}
	id := v.Model.HistoryEdits["s"].OperationID
	keep := int64(-1)
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
	ui.Frame()
	bridge, ok := ui.Find("用户消息气泡")
	if !ok || !ui.HasText("revised prompt") || ui.HasText("编辑历史消息") || math.Abs(float64(bridge.Y-constrained.Y)) > 1 {
		t.Fatal("reset dropped the inline anchor before the replacement arrived", constrained, bridge)
	}
	newItem := domain.Item{ID: "input:" + id, Kind: "user", Text: "revised prompt", Status: "sent", TurnID: "new-turn", Order: 3, Final: true}
	v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &newItem, Operation: &domain.Operation{ID: id, SessionID: "s", State: "confirmed"}})
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	after, ok := ui.Find("revised prompt")
	if !ok || math.Abs(float64(after.Y-constrained.Y-12)) > 1 {
		t.Fatal("replacement jumped after the old row identity disappeared", constrained, after)
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

func TestConfirmedHistoryReceiptDoesNotFlashUncertainStatus(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(time.Second)
	v.Model.Drafts[v.Model.HistoryDraftKey()] = "revised question"
	ui.Frame()
	v.restartHistory()
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method != "session.restart" {
			continue
		}
		in := job.Input.(domain.HistoryChange)
		source := v.Model.Details["s"].Session
		// The RPC can confirm before the independent watch delivers reset
		// and accepted input. This is a known success, not uncertainty.
		op := domain.HistoryOperation{ID: in.OperationID, Input: in, Source: source, Session: source, State: "complete"}
		payload, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := ui.Find("消息编辑区")
		job.Apply(v.Model, payload, nil)
		ui.Frame()
		ui.Advance(100 * time.Millisecond)
		if ui.HasText("会话操作结果待核对") || ui.HasText("原消息已被替换，修改稿已保留") {
			t.Fatal("confirmed edit flashes a recovery strip while waiting for the watch stream", ui.Texts())
		}
		if !ui.HasText("revised question") || v.Model.Drafts["session:s"] != "ordinary unsent draft" {
			t.Fatal("confirmed edit lost its preview or independent draft")
		}
		after, _ := ui.Find("消息编辑区")
		if math.Abs(float64(after.Y-before.Y)) > .5 || after.H != before.H {
			t.Fatal("confirmed receipt changes the ordinary composer geometry", before, after)
		}
		keep := int64(-1)
		v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
		ui.Frame()
		if !ui.HasText("revised question") || ui.HasText("会话操作结果待核对") {
			t.Fatal("known success lost its preview across reset")
		}
		replacement := domain.Item{ID: "input:" + in.OperationID, Kind: "user", TurnID: "new-turn", Status: "sent", Text: "revised question", Final: true}
		v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &replacement, Operation: &domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"}})
		ui.Frame()
		ui.Frame()
		if v.historyExit == nil || !ui.HasText("进行中") || ui.HasText("会话操作结果待核对") {
			t.Fatal("confirmed history transition lost its pending turn receipt")
		}
		return
	}
	t.Fatal("history restart was not dispatched")
}

func TestHistoryRestartShowsRunningAfterShatterBeforeCollapse(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprint("reduced=", reduced), func(t *testing.T) {
			v, ui, item := historyFixture()
			ui.SetPreferences(n.Preferences{ReduceMotion: reduced})
			v.beginHistoryEdit(item)
			ui.Frame()
			ui.Advance(time.Second)
			v.Model.Drafts[v.Model.HistoryDraftKey()] = "revised question"
			ui.Frame()
			v.restartHistory()
			ui.Frame()
			if !reduced {
				ui.Advance(1000 * time.Millisecond)
				if ui.HasText("进行中") {
					t.Fatal("running overlaps visible old reply fragments")
				}
				ui.Advance(300 * time.Millisecond)
				if !ui.HasText("进行中") {
					t.Fatal("suffix collapses before the running receipt appears")
				}
				for len(v.Controller.Jobs) > 0 {
					if job := <-v.Controller.Jobs; job.Method == "session.restart" {
						t.Fatal("runtime dispatched before the exit completed")
					}
				}
				ui.Advance(100 * time.Millisecond)
			}
			if !ui.HasText("进行中") || !ui.HasText("revised question") {
				t.Fatal("fragments vanished without a running receipt", ui.Texts())
			}
			ui.Advance(time.Second)
			in := v.historyExit.in
			keep := int64(-1)
			v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
			ui.Frame()
			if !ui.HasText("进行中") {
				t.Fatal("reset lost local running receipt")
			}
			replacement := domain.Item{ID: "input:" + in.OperationID, Kind: "user", Text: "revised question", Status: "dispatching", Final: true}
			v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &replacement, Operation: &domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"}})
			ui.Frame()
			ui.Frame()
			if !ui.HasText("进行中") {
				t.Fatal("accepted input lost running receipt before turn start", ui.Texts())
			}
			turn := domain.Turn{ID: "new-turn", Status: "running"}
			v.Model.ApplyEvent(domain.Event{Sequence: 3, Type: "turn", SessionID: "s", Turn: &turn})
			ui.Frame()
			if !ui.HasText("进行中") {
				t.Fatal("turn start before input binding lost running receipt")
			}
			reply := domain.Item{ID: "reply-new", TurnID: "new-turn", Kind: "assistant", Text: "已开始", Final: false}
			v.Model.ApplyEvent(domain.Event{Sequence: 4, Type: "item.upsert", SessionID: "s", Item: &reply})
			ui.Frame()
			ui.Frame()
			count := 0
			for _, text := range ui.Texts() {
				if text == "进行中" || text == "正在回复" {
					count++
				}
			}
			if count != 1 || v.historyExit != nil {
				t.Fatal("running receipt handoff duplicated or retained local preview", ui.Texts(), v.historyExit)
			}
		})
	}
}

func TestHistoryRunningHandsOffUncertainDeliveryToRecovery(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(time.Second)
	v.restartHistory()
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
	in := v.historyExit.in
	for len(v.Controller.Jobs) > 0 {
		job := <-v.Controller.Jobs
		if job.Method == "session.restart" {
			source := v.Model.Details["s"].Session
			payload, _ := json.Marshal(domain.HistoryOperation{ID: in.OperationID, Input: in, Source: source, Session: source, State: "complete"})
			job.Apply(v.Model, payload, nil)
		}
	}
	keep := int64(-1)
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
	replacement := domain.Item{ID: "input:" + in.OperationID, Kind: "user", Text: in.Text, Status: "dispatching", Final: true}
	v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &replacement, Operation: &domain.Operation{ID: in.OperationID, SessionID: "s", State: "accepted"}})
	ui.Frame()
	if !ui.HasText("进行中") {
		t.Fatal("accepted submission lost waiting state")
	}
	v.Model.ApplyEvent(domain.Event{Sequence: 3, Type: "operation", SessionID: "s", Operation: &domain.Operation{ID: in.OperationID, SessionID: "s", State: "indeterminate", Error: "lost acceptance reply"}})
	replacement.Status = "unknown"
	v.Model.ApplyEvent(domain.Event{Sequence: 4, Type: "item.upsert", SessionID: "s", Item: &replacement})
	ui.Frame()
	ui.Advance(300 * time.Millisecond)
	if ui.HasText("进行中") || v.historyExit != nil || !ui.HasText("核对结果") || v.Model.Drafts["session:s"] != "ordinary unsent draft" {
		t.Fatal("uncertain delivery remains falsely running or hides recovery", ui.Texts(), v.historyExit)
	}
}

func TestHistoryRunningFooterKeepsPositionWhenInputGetsItsTurn(t *testing.T) {
	v, ui, item := historyFixture()
	v.beginHistoryEdit(item)
	ui.Frame()
	ui.Advance(time.Second)
	v.Model.Drafts[v.Model.HistoryDraftKey()] = "revised question"
	ui.Frame()
	v.restartHistory()
	ui.Advance(historyShatterDuration + historyCollapseDuration + 100*time.Millisecond)
	local, ok := ui.Find("进行中")
	if !ok {
		t.Fatal("no local running footer")
	}
	in := v.historyExit.in
	keep := int64(-1)
	v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
	ui.Frame()
	reset, _ := ui.Find("进行中")
	replacement := domain.Item{ID: "input:" + in.OperationID, Kind: "user", Text: "revised question", Status: "dispatching", Final: true}
	v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &replacement, Operation: &domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"}})
	ui.Frame()
	ui.Frame()
	accepted, _ := ui.Find("进行中")
	turn := domain.Turn{ID: "new-turn", Status: "running"}
	v.Model.ApplyEvent(domain.Event{Sequence: 3, Type: "turn", SessionID: "s", Turn: &turn})
	ui.Frame()
	replacement.TurnID, replacement.Status = "new-turn", "sent"
	v.Model.ApplyEvent(domain.Event{Sequence: 4, Type: "item.upsert", SessionID: "s", Item: &replacement})
	ui.Frame()
	firstFrame, _ := ui.Find("进行中")
	ui.Frame()
	real, ok := ui.Find("进行中")
	if !ok || v.historyExit != nil {
		t.Fatal("real running footer did not take over")
	}
	for _, r := range []n.Rect{reset, accepted, firstFrame, real} {
		if math.Abs(float64(r.X-local.X)) > .5 || math.Abs(float64(r.Y-local.Y)) > .5 || math.Abs(float64(r.H-local.H)) > .5 {
			t.Fatal("metadata-only handoff moves the running label", local, reset, accepted, real)
		}
	}
	v.Model.ApplyEvent(domain.Event{Sequence: 5, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "first-reply", TurnID: "new-turn", Kind: "assistant", Text: "重开后的首行回复。"}})
	ui.Frame()
	ui.Advance(time.Second)
	firstReply, ok := ui.Find("重开后的首行回复。")
	if !ok || math.Abs(float64(firstReply.Y-real.Y)) > 1 {
		t.Fatal("first restarted reply misses the waiting position", real, firstReply)
	}
}

func TestHistorySuffixCollapseKeepsTheTranscriptAtItsNaturalEnd(t *testing.T) {
	for _, coalesced := range []bool{false, true} {
		t.Run(fmt.Sprint("coalesced=", coalesced), func(t *testing.T) {
			v, ui, item := historyFixture()
			d := v.Model.Details["s"]
			var prefix []domain.Item
			for i := 0; i < 30; i++ {
				prefix = append(prefix, domain.Item{ID: fmt.Sprint("earlier-", i), TurnID: "earlier", Kind: "assistant", Text: "Earlier conversation paragraph", Final: true})
			}
			d.Items = append(prefix, d.Items...)
			d.Turns = append([]domain.Turn{{ID: "earlier", Order: -1, Status: "completed"}}, d.Turns...)
			v.Model.Details["s"] = d
			ui.Frame()
			ui.Advance(time.Second)
			v.beginHistoryEdit(item)
			ui.Frame()
			ui.Advance(time.Second)
			revision := "仅测试：一次性运行 printf \"锚点批量检查 %02d\\n\" {1..32}，然后单独执行 sleep 20。不要读取或修改文件，不要提前输出说明，最后回复“检查完成”。"
			v.Model.Drafts[v.Model.HistoryDraftKey()] = revision
			ui.Frame()
			v.restartHistory()
			before := v.historyRowBounds[item.ID]
			runningBefore := ui.HasText("进行中")
			for frame := 0; frame < 110; frame++ {
				ui.Advance(time.Second / 60)
				after := v.historyRowBounds[item.ID]
				runningNow := ui.HasText("进行中")
				if runningBefore && !runningNow {
					t.Fatalf("running receipt flashes away during collapse at frame %d", frame)
				}
				if math.Abs(float64(after.Y-before.Y)) > 1 && !runningBefore {
					t.Fatalf("blank-space correction precedes the running receipt at frame %d: %+v -> %+v", frame, before, after)
				}
				if math.Abs(float64(after.Y-before.Y)) > 12 {
					t.Fatalf("receipt/layout handoff jumps instead of tracking the collapsing height at frame %d: %+v -> %+v", frame, before, after)
				}
				before = after
				runningBefore = runningNow
			}
			ui.Advance(time.Second)
			if !ui.HasText("进行中") {
				t.Fatal("missing local running receipt")
			}
			footer, ok := ui.Find("进行中")
			dock, _ := ui.Find("会话底部面板")
			if !ok || dock.Y-footer.Y-footer.H > 80 {
				t.Fatal("removed replies leave a large trailing gap", footer, dock)
			}
			assertHandoff := func(stage string) {
				t.Helper()
				for frame := 0; frame < 2; frame++ {
					ui.Advance(time.Second / 60)
					current, visible := ui.Find("进行中")
					if !visible || math.Abs(float64(current.Y-footer.Y)) > 1 {
						t.Fatalf("%s scrolls an unchanged running receipt at frame %d: %+v -> %+v", stage, frame, footer, current)
					}
				}
			}
			in := v.historyExit.in
			keep := int64(-1)
			v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
			if !coalesced {
				ui.Frame()
				assertHandoff("history reset")
			} else {
				// A history refresh can finish measuring retained prose in the
				// same frame that the accepted input takes the local row's place.
				detail := v.Model.Details["s"]
				detail.Items[len(detail.Items)-1].Text = "Refreshed retained context.\n\nRetained context measured during confirmation.\n\nAdditional retained context."
				v.Model.Details["s"] = detail
			}
			replacement := domain.Item{ID: "input:" + in.OperationID, Kind: "user", Text: revision, Status: "dispatching", Final: true}
			v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &replacement, Operation: &domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"}})
			if !coalesced {
				ui.Frame()
				assertHandoff("accepted input")
			}
			turn := domain.Turn{ID: "new-turn", Status: "running", Order: 1}
			v.Model.ApplyEvent(domain.Event{Sequence: 3, Type: "turn", SessionID: "s", Turn: &turn})
			if !coalesced {
				ui.Frame()
				assertHandoff("turn start")
			}
			replacement.TurnID, replacement.Status = "new-turn", "sent"
			v.Model.ApplyEvent(domain.Event{Sequence: 4, Type: "item.upsert", SessionID: "s", Item: &replacement})
			ui.Frame()
			assertHandoff("authoritative running receipt")
			previous := v.historyRowBounds[replacement.ID]
			for frame := 0; frame < 60; frame++ {
				if frame < 4 {
					text := []string{"", "正在", "正在执行", "正在执行。"}[frame]
					reply := domain.Item{ID: "new-prose", TurnID: "new-turn", Kind: "assistant", Text: text, Final: frame == 3}
					v.Model.ApplyEvent(domain.Event{Sequence: int64(5 + frame), Type: "item.upsert", SessionID: "s", Item: &reply})
					ui.Frame()
				}
				ui.Advance(time.Second / 60)
				current := v.historyRowBounds[replacement.ID]
				if current.Y > previous.Y+.5 {
					t.Fatalf("first real prose scrolls down before returning at frame %d: %+v -> %+v", frame, previous, current)
				}
				previous = current
			}
		})
	}
}

func TestHistoryRunningHandoffPreservesPaintAndAnimationPhase(t *testing.T) {
	for _, replyFirst := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprint("dark=", dark, "/reply-first=", replyFirst), func(t *testing.T) {
				v, ui, item := historyFixture()
				ui.SetDark(dark)
				v.beginHistoryEdit(item)
				ui.Frame()
				ui.Advance(time.Second)
				v.Model.Drafts[v.Model.HistoryDraftKey()] = "revised question"
				ui.Frame()
				v.restartHistory()
				ui.Advance(historyShatterDuration + historyCollapseDuration + 500*time.Millisecond)
				in := v.historyExit.in
				keep := int64(-1)
				v.Model.ApplyEvent(domain.Event{Sequence: 1, Type: "history.reset", SessionID: "s", HistoryTurnOrder: &keep})
				replacement := domain.Item{ID: "input:" + in.OperationID, Kind: "user", Text: "revised question", Status: "dispatching", Final: true}
				v.Model.ApplyEvent(domain.Event{Sequence: 2, Type: "submission", SessionID: "s", Item: &replacement, Operation: &domain.Operation{ID: in.OperationID, SessionID: "s", State: "confirmed"}})
				ui.Frame()
				ui.Advance(370 * time.Millisecond)
				ui.Frame() // Paint at Now(), after Advance may stop between display frames.
				region := func() n.Rect {
					// A real reply changes the activity label intentionally;
					// only the retained dot must paint identically then.
					if replyFirst {
						r, ok := ui.Find("进行中状态")
						if !ok {
							t.Fatal("missing live dot")
						}
						return r
					}
					r, ok := ui.Find("进行中")
					if !ok {
						t.Fatal("missing running footer")
					}
					r.X -= 12
					r.W += 12
					return r
				}
				before := contextPixels(ui, region())
				turn := domain.Turn{ID: "new-turn", Status: "running"}
				v.Model.ApplyEvent(domain.Event{Sequence: 3, Type: "turn", SessionID: "s", Turn: &turn})
				if replyFirst {
					v.Model.ApplyEvent(domain.Event{Sequence: 4, Type: "item.upsert", SessionID: "s", Item: &domain.Item{ID: "new-prose", TurnID: "new-turn", Kind: "assistant", Text: "新的回复"}})
				} else {
					replacement.TurnID, replacement.Status = "new-turn", "sent"
					v.Model.ApplyEvent(domain.Event{Sequence: 4, Type: "item.upsert", SessionID: "s", Item: &replacement})
				}
				for frame := 0; frame < 3; frame++ {
					ui.Frame() // Same clock: a metadata handoff must paint identically.
					if !bytes.Equal(before, contextPixels(ui, region())) {
						t.Fatalf("handoff flashes or restarts pulse/shimmer at frame %d", frame)
					}
				}
				if v.historyExit != nil {
					t.Fatal("handoff kept the local history motion alive")
				}
			})
		}
	}
}
